// 空态文案与「数据缺失」渲染的守门测试（批次 6 收尾）。
//
// 背景：旧后台的空态大多只有一句话（「没有记录」「没有数据」），
// 用户看不出到底是「本来就没有」「筛太窄」还是「接口坏了」——这三件事的
// 处置完全不同（去导入数据 / 放宽筛选 / 报障）。
// 批次 6 把每个空态都补成「标题 + 一句为什么 + 下一步去哪」，
// 这份测试就是防止以后再退回一句话。
//
// 同时守另一条：接口返回「字段缺失」或「显式为 null」时，页面不能把
// null / undefined / NaN / [object Object] 直接渲染出来。空数组走空态分支，
// 暴露不了这类问题，所以必须用「对象在、字段没了」的脏数据来测。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;

// 覆盖到每个页面的每个页签：不同页签是不同的 loader，空态也各写各的。
const ROUTES = [
  ['dashboard', '总览'],
  ['players', '玩家管理'],
  ['shop', '商店与库存'],
  ['rules/switches', '玩法开关'],
  ['rules/grade', '出鱼比例'],
  ['rules/other', '咬钩与其它'],
  ['ecology/eco', '生态基线'],
  ['ecology/econ', '经济四档'],
  ['ecology/weight', '权重档位'],
  ['waters', '水域鱼种'],
  ['hooks', '钩子手感'],
  ['bait', '鱼饵偏好'],
  ['weather', '天气预览'],
  ['mail', '邮件'],
  ['tuning/gameplay', '游戏参数'],
  ['release', '发布说明'],
  ['sync', '参数同步'],
  ['about', '关于'],
  ['audit', '审计日志'],
];

// 一个「键都在、值全 null」的对象。
// 它比缺字段更接近数据库里的 NULL 行，也能顺带测出 String(null) → "null"。
function nullObj() {
  const keys = [
    'uid', 'name', 'level', 'xp', 'silver', 'gold', 'bank', 'banned', 'online', 'online_known',
    'membership', 'item_count', 'keepnet_count', 'mail_count', 'created_ts', 'updated_ts',
    'item_id', 'qty', 'guid', 'durability', 'container', 'fish_key', 'name_zh', 'species',
    'weight', 'weight_g', 'min_g', 'max_g', 'base_weight', 'enabled', 'tier', 'size',
    'display_size', 'family', 'strength_kg', 'entry_capacity_g', 'solid_scale',
    'small_fish_floor', 'over_capacity_hold_floor', 'day', 'high_c', 'low_c',
    'water_low_c', 'water_high_c', 'loc', 'area', 'count', 'share', 'rows', 'total',
    'action', 'target', 'admin', 'ts', 'detail', 'text', 'kind', 'status', 'label',
    'quota', 'pool_size', 'bait_id', 'pct', 'preview_version', 'channel',
  ];
  return Object.fromEntries(keys.map((k) => [k, null]));
}

// 接口 → 脏数据。形状必须对（该是数组的给数组），否则页面走的是完全不同的分支。
const DIRTY = {
  '/api/dashboard': nullObj(),
  '/api/players': [nullObj()],
  '/api/players/detail': {
    ...nullObj(), items: [nullObj()], keepnet: [nullObj()],
    mails: [nullObj()], skills: [nullObj()], happy_hour: {},
  },
  '/api/audit': [nullObj()],
  '/api/shop/stores': [nullObj()],
  '/api/shop/items': [nullObj()],
  '/api/hooks/profiles': { profiles: [nullObj()], total: 1, sizes: [nullObj()] },
  '/api/fish/areas': [nullObj()],
  '/api/fish/config': { ...nullObj(), fish: [nullObj()], setting: {} },
  '/api/fish/zones': { zones: [nullObj()] },
  '/api/bait/prefs': { rows: [nullObj()], rules_enabled: true },
  '/api/bait/categories': { categories: [nullObj()] },
  '/api/weather/preview': {
    ...nullObj(), days: [nullObj()], hours: [nullObj()],
    maps: [nullObj()], bands: [nullObj()], seasons: [nullObj()],
  },
  '/api/weather/active-fish': { active: { ...nullObj(), fish: [nullObj()], idle: [nullObj()] } },
  '/api/econ/species': { items: [nullObj()], maps: [nullObj()] },
  '/api/fish/weights/preset': { weights: [0, 0, 0], tiers: [nullObj()], active: null, rows: null },
  '/api/tunables': { items: [nullObj()], groups: [] },
};

function dirtyFor(pathname) {
  for (const [k, v] of Object.entries(DIRTY)) if (pathname.endsWith(k)) return v;
  if (pathname.endsWith('/api/me')) return { name: 'admin' };
  if (pathname.includes('/api/')) return {};
  return null;
}

const LEAK = ['null', 'undefined', 'NaN', '[object Object]'];
function leaks(text) {
  return LEAK.filter((t) => new RegExp('\\b' + t.replace(/[[\]]/g, '\\$&') + '\\b').test(text));
}

async function open(browser, route) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    if (fx.serveAsset(r, u)) return;
    const d = dirtyFor(u.pathname);
    if (d === null) return r.fulfill({ status: 404, body: '' });
    return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(d) });
  });
  await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
  await page.goto('http://127.0.0.1:1/#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
  await page.waitForTimeout(800);
  return { page, errors };
}

// 可见文本里的裸值泄漏：null/undefined/NaN 出现在任何位置都是渲染缺陷。
test('脏数据下没有页面把 null / undefined / NaN 渲染成文字', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const bad = [];
  try {
    for (const [route, label] of ROUTES) {
      const { page, errors } = await open(browser, route);
      const text = await page.evaluate(() => (document.getElementById('view') || document.body).innerText || '');
      const hit = leaks(text);
      if (hit.length) {
        const idx = text.indexOf(hit[0]);
        bad.push(`${label}(${route}): 渲染出 ${hit.join('/')} → …${text.slice(Math.max(0, idx - 40), idx + 20).replace(/\s+/g, ' ')}…`);
      }
      if (errors.length) bad.push(`${label}(${route}): JS 错误 ${errors.join('; ')}`);
      await page.close();
    }
  } finally { await browser.close(); }
  assert.deepEqual(bad, [], '以下页面把缺失字段渲染成了文字:\n' + bad.join('\n'));
});

// 空态必须能回答「为什么空」。只写四个字的空态（「没有记录」「没有数据」）
// 用户无法判断该去导入数据、放宽筛选，还是报障。
const EMPTY_ROUTES = [
  // 接口全部回空数组/空对象：逼出每个页面的「一条都没有」空态。
  ['audit', '审计日志'],
  ['dashboard', '总览'],
  ['waters', '水域鱼种'],
  ['hooks', '钩子手感'],
  ['bait', '鱼饵偏好'],
  ['ecology/eco', '生态基线'],
  ['ecology/econ', '经济四档'],
  ['ecology/weight', '权重档位'],
  ['shop', '商店与库存'],
  ['release', '发布说明'],
  ['about', '关于'],
];

test('空态都写清了「为什么空」和「下一步」，不是一句话', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const bad = [];
  try {
    for (const [route, label] of EMPTY_ROUTES) {
      const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
      await page.route('**/*', (r) => {
        const u = new URL(r.request().url());
        if (fx.serveAsset(r, u)) return;
        if (u.pathname.includes('/api/')) {
          return r.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
        }
        return r.fulfill({ status: 404, body: '' });
      });
      await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
      await page.goto('http://127.0.0.1:1/#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
      await page.waitForTimeout(900);
      // 加载态（「读取中…」）不算空态：它出现时数据还在路上。
      const empties = await page.evaluate(() => [...document.querySelectorAll('#view .empty')]
        .map((el) => (el.innerText || '').replace(/\s+/g, ' ').trim())
        .filter((t) => t && !/读取中|正在读取|加载/.test(t)));
      for (const t of empties) {
        // 门槛定在 14 字：一句话的标题通常是 4–10 字，
        // 加上「为什么 + 怎么办」后必然超过。这条不是文案审美，是可判断性下限。
        if (t.length < 14) bad.push(`${label}(${route}): 空态只有 ${t.length} 字「${t}」`);
      }
      await page.close();
    }
  } finally { await browser.close(); }
  assert.deepEqual(bad, [], '以下空态说不出为什么空:\n' + bad.join('\n'));
});
// 「一条数据都没有」时页面不能停在加载态。
//
// 背景：weather 页原来在没有可用水域时只改顶部一行小字（`status.textContent`），
// 下面六个区块各自是独立容器，谁也没人收尾 —— 于是一整页全是「读取中…」，
// 用户以为页面卡死了。这类缺陷空态测试抓不到（它只看 .empty 的文字长度），
// 必须专门判「加载态有没有残留」。
test('空数据时页面不会永久停在「读取中」', async () => {
  const CASES = [
    // [说明, 路由, 额外接口替身]
    ['天气页 · 一张地图都没有', 'weather', { '/api/weather/maps': { maps: [] }, '/api/weather/preview': {} }],
    ['天气页 · 有地图但没有预览数据', 'weather', {
      '/api/weather/maps': { maps: [{ key: 'level_001', map_name: '铜湖' }] },
      '/api/weather/preview': { preview: null, now_day: 1 },
    }],
    ['水域页 · 水域列表为空', 'waters', { '/api/fish/areas': [] }],
    ['玩家页 · 玩家列表为空', 'players', { '/api/players': [] }],
  ];
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const bad = [];
  try {
    for (const [label, route, overrides] of CASES) {
      const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
      await page.route('**/*', (r) => {
        const u = new URL(r.request().url());
        if (fx.serveAsset(r, u)) return;
        for (const [k, v] of Object.entries(overrides)) {
          if (u.pathname.endsWith(k)) {
            return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(v) });
          }
        }
        if (u.pathname.endsWith('/api/me')) {
          return r.fulfill({ status: 200, contentType: 'application/json', body: '{"name":"admin"}' });
        }
        if (u.pathname.includes('/api/')) {
          return r.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
        }
        return r.fulfill({ status: 404, body: '' });
      });
      await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
      await page.goto('http://127.0.0.1:1/#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
      await page.waitForTimeout(1500);
      const stuck = await page.evaluate(() => [...document.querySelectorAll('#view .empty')]
        .map((e) => (e.innerText || '').replace(/\s+/g, ' ').trim())
        .filter((t) => /读取中|正在读取|加载中/.test(t)));
      if (stuck.length) bad.push(`${label}: 还有 ${stuck.length} 处停在加载态（${stuck[0]}）`);
      await page.close();
    }
  } finally { await browser.close(); }
  assert.deepEqual(bad, [], '以下页面停在了加载态:\n' + bad.join('\n'));
});
