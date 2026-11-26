// 批次 5 守门测试：天气预览。
//
// weather_ui_test.cjs 已经覆盖了六个区块的字段级正确性（7 天卡片 / 曲线 / 分档 /
// 四季 / 全区对比 / 活跃鱼种）。这里只守它守不到、但迁移期最容易翻车的四类事：
//
//   1) **采样点数是算出来的，不是写死的**：旧后台把「168 个采样点」写死在文案里
//      （= 7 天 × 24）。天数下拉一改成 3 或 14，那句话就开始骗人。
//   2) **地图清单只取一次**：切图/改天数只该重发 preview 与 active-fish。
//      每次都重拉 /api/weather/maps 等于每切一次图就把下拉重建一遍，正在选的
//      那一项会闪，而且多四次无意义往返。
//   3) **前端只画不算**：天气判鱼的水温响应、昼夜曲线都必须在 gameserver 里算完。
//      前端一旦自己抄一份公式，后台显示的「活跃鱼种」迟早和客户端咬口不一致——
//      这正是这一页存在的意义（与客户端同一套数据），抄一份就白做了。
//   4) **7 天卡片不是 <button>**：theme-aerial.css 有一条全局
//      `button:not(...){background:#101d1a!important}`，7 张深色砖在浅色主题下
//      和整页打架（2026-11-26 截图核对时实际踩到）。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const BROWSER = process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'] || '', 'Microsoft/Edge/Application/msedge.exe');
const webRoot = path.join(__dirname, 'web');
const readPage = (f) => fs.readFileSync(path.join(webRoot, 'app/pages', f), 'utf8');

const NOW_DAY = 99185;

function mkHours(day, high, low, kind) {
  const out = [];
  for (let h = 0; h < 24; h += 1) {
    const cyc = (1 - Math.cos((h - 4) * Math.PI / 12)) / 2;
    const air = +(high - (high - low) * (1 - cyc)).toFixed(1);
    out.push({
      day, hour: h, air_temp_c: air,
      surface_c: +(air * 0.62 + 3).toFixed(1), water_c: +(air * 0.42 + 6).toFixed(1),
      deep_c: +(air * 0.3 + 8).toFixed(1), cloud: 0.05, rain: 0, thunder: 0, wind_kmh: 12, fog: 0,
      kind: 0, kind_name: kind,
      segment: h < 5 ? 0 : (h < 9 ? 1 : (h < 16 ? 2 : 3)),
      segment_name: h < 5 ? '夜' : (h < 9 ? '晨' : (h < 16 ? '昼' : '昏')),
      is_now: day === NOW_DAY && h === 2,
    });
  }
  return out;
}

// 只给 3 天：用来验证「采样点数 = days × 24」是算出来的。
function mkPreview(map, mapName, dayCount) {
  const kinds = ['晴', '少云', '多云', '小雨', '大雨', '雷雨', '晴'];
  const days = [];
  let hours = [];
  for (let i = 0; i < dayCount; i += 1) {
    const high = 39 - i * 2, low = high - 14;
    days.push({ day: NOW_DAY + i, kind: 0, kind_name: kinds[i % 7], high_c: high, low_c: low,
      water_high_c: +(high * 0.42 + 6).toFixed(1), water_low_c: +(low * 0.42 + 6).toFixed(1),
      max_wind_kmh: 14 + i, rain_hours: 0 });
    hours = hours.concat(mkHours(NOW_DAY + i, high, low, kinds[i % 7]));
  }
  const mids = hours.map((x) => x.water_c);
  const lo = Math.min(...mids), hi = Math.max(...mids);
  const bands = [];
  for (let a = Math.floor(lo / 2) * 2; a < Math.ceil(hi / 2) * 2; a += 2) {
    const cnt = mids.filter((v) => v >= a && v < a + 2).length;
    bands.push({ from_c: a, to_c: a + 2, count: cnt, share: cnt / mids.length });
  }
  return {
    map, map_name: mapName, day_from: NOW_DAY, day_to: NOW_DAY + dayCount - 1,
    now_day: NOW_DAY, now_hour: 2, game_clock: '02:31',
    water_min_c: lo, water_max_c: hi, air_min_c: 12.3, air_max_c: 39,
    days, hours, bands,
    // 日号刻意取真实量级（99185）：2026-11-23 曾把日号重置成「开服第 4 天」，
    // 结果页面显示「1 月 5 日」而季节标签是秋。日号必须是世界时钟轴。
    seasons: [
      { season: 0, season_name: '春', water_min_c: 9.7, water_max_c: 26, air_min_c: 3, air_max_c: 31, is_now: false },
      { season: 1, season_name: '夏', water_min_c: 15, water_max_c: 32.2, air_min_c: 9, air_max_c: 39, is_now: false },
      { season: 2, season_name: '秋', water_min_c: 11.5, water_max_c: 27.4, air_min_c: 12.3, air_max_c: 29, is_now: true },
      { season: 3, season_name: '冬', water_min_c: 4.9, water_max_c: 21.7, air_min_c: 1.7, air_max_c: 18, is_now: false },
    ],
    season: 2, season_name: '秋',
  };
}

const MAPS = [
  { key: 'level_001_lake', map_name: '克马羚诺也湖', has_data: true },
  { key: 'level_009_ahtuba', map_name: '阿赫图巴河', has_data: true },
  { key: 'level_012_north_sea', map_name: '挪威海', has_data: true },
];
const OVERVIEW = [
  { map: 'level_001_lake', map_name: '克马羚诺也湖', air_min_c: 7.3, air_max_c: 21.2, water_min_c: 10.3, water_max_c: 23, season_name: '秋' },
  { map: 'level_009_ahtuba', map_name: '阿赫图巴河', air_min_c: 12.3, air_max_c: 39, water_min_c: 11.5, water_max_c: 32.2, season_name: '秋' },
  { map: 'level_012_north_sea', map_name: '挪威海', air_min_c: 1.7, air_max_c: 9, water_min_c: 7.3, water_max_c: 16.7, season_name: '秋' },
];
const ACTIVE = {
  map: 'level_009_ahtuba', game_hour: 2, segment: '夜', water_c: 18.4, season: '秋',
  total: 3, active_num: 2,
  fish: [
    { fish_key: 'perch', name_zh: '河鲈', rarity: 2, rarity_tag: '超稀有', diel_mask: 2, diel_label: '昼夜',
      temp_min_c: 8, temp_pref_c: 15, temp_max_c: 22, diel_factor: 1, thermal_fact: 0.95, score: 1.42, active: true, reason: '' },
    { fish_key: 'pike', name_zh: '白斑狗鱼', rarity: 1, rarity_tag: '稀有', diel_mask: 2, diel_label: '昼',
      temp_min_c: 6, temp_pref_c: 14, temp_max_c: 21, diel_factor: 0.95, thermal_fact: 0.93, score: 0.88, active: true, reason: '' },
  ],
  idle: [{ fish_key: 'bream', name_zh: '欧鳊', rarity: 0, rarity_tag: '普通', diel_mask: 2, diel_label: '昼',
    temp_min_c: 8, temp_pref_c: 15, temp_max_c: 22, diel_factor: 0.08, thermal_fact: 1, score: 0.12, active: false,
    reason: '非活跃时段（昼）' }],
};

// 打开 SPA 天气页。记录每个 /api/ 路径被请求了几次与每次的查询参数——
// 「地图清单只取一次」这类断言全靠它。
// 注意 page.route('**/*') 先注册者优先：记录与响应必须写在同一个 handler 里。
async function openWeather(browser, hash = '#/weather') {
  const page = await browser.newPage({ viewport: { width: 1560, height: 1100 } });
  const errors = [];
  const hits = {};
  const calls = [];
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    const p = u.pathname.replace(/^\/admin/, '').replace(/^\/+/, '/');
    if (p.startsWith('/api/')) {
      hits[p] = (hits[p] || 0) + 1;
      calls.push({ path: p, query: u.search });
      const ok = (v) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(v) });
      if (p === '/api/me') return ok({ username: 'admin', item_categories: {} });
      if (p === '/api/dashboard') return ok({ sessions: 0, players: 0, tunables: 0 });
      if (p === '/api/weather/maps') return ok({ maps: MAPS });
      if (p === '/api/weather/preview') {
        const m = u.searchParams.get('map');
        const days = Number(u.searchParams.get('days') || 7);
        // 概览（不传 map）：18 张图的逐时明细不下发，只给区间。
        if (!m) return ok({ now_day: NOW_DAY, game_now: 0, days, maps: OVERVIEW });
        const name = (MAPS.find((x) => x.key === m) || {}).map_name || m;
        return ok({ preview: mkPreview(m, name, days), now_day: NOW_DAY, now_hour: 2, game_now: 0 });
      }
      if (p === '/api/weather/active-fish') {
        const m = u.searchParams.get('map');
        return ok({ map_name: (MAPS.find((x) => x.key === m) || {}).map_name || m, game_now: 0, active: ACTIVE });
      }
      return ok({});
    }
    if (u.pathname === '/' || u.pathname.endsWith('/index.html')) {
      return r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: fs.readFileSync(path.join(webRoot, 'index.html')) });
    }
    const rel = decodeURIComponent(u.pathname.replace(/^\/+/, ''));
    const file = path.join(webRoot, rel);
    if (fs.existsSync(file) && fs.statSync(file).isFile()) {
      const ext = path.extname(file).toLowerCase();
      const mime = { '.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.jpg': 'image/jpeg',
        '.webp': 'image/webp', '.png': 'image/png', '.json': 'application/json' }[ext] || 'application/octet-stream';
      return r.fulfill({ status: 200, contentType: mime, body: fs.readFileSync(file) });
    }
    return r.fulfill({ status: 404, body: '' });
  });
  await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
  await page.goto('http://127.0.0.1:1/' + hash, { waitUntil: 'networkidle', timeout: 30000 });
  return { page, errors, hits, calls };
}

async function launch() {
  return chromium.launch({ headless: true, executablePath: BROWSER });
}

test('批次 5 · 采样点数是 days × 24 算出来的，不写死 168', async () => {
  const browser = await launch();
  try {
    const { page, errors } = await openWeather(browser);
    await page.locator('#wxDayCards .wx-day').first().waitFor();

    // 默认 7 天 → 7 × 24 = 168
    assert.match(await page.locator('#wxBandBox').innerText(), /168 个采样点/);

    // 改成 3 天 → 必须变成 72。文案若写死，这里会读到 168。
    await page.selectOption('#wxDays', '3');
    await page.waitForFunction(() => /72 个采样点/.test(document.querySelector('#wxBandBox').textContent || ''), null, { timeout: 10000 });
    const txt3 = await page.locator('#wxBandBox').innerText();
    assert.match(txt3, /72 个采样点/, '三天窗口应显示 72 个采样点');
    assert.doesNotMatch(txt3, /168 个采样点/, '三天窗口里不许残留「168 个采样点」');
    // 天数也要跟着走：3 张卡片，而不是 7 张。
    assert.equal(await page.locator('#wxDayCards .wx-day').count(), 3);

    assert.deepEqual(errors, []);
    await page.close();
  } finally { await browser.close(); }
});

test('批次 5 · 地图清单只取一次，切图不重建下拉', async () => {
  const browser = await launch();
  try {
    const { page, errors, hits, calls } = await openWeather(browser);
    await page.locator('#wxDayCards .wx-day').first().waitFor();
    assert.equal(hits['/api/weather/maps'], 1, '地图清单首屏只该取一次');

    // 切图：预览与活跃鱼各重发一次，地图清单不重发。
    await page.selectOption('#wxMap', 'level_012_north_sea');
    await page.waitForFunction(() => document.querySelector('#wxStatus').textContent.includes('挪威海'), null, { timeout: 10000 });
    assert.equal(hits['/api/weather/maps'], 1, '切图不该重拉地图清单');
    assert.ok(hits['/api/weather/preview'] >= 2, '切图要重发预览');
    assert.ok(hits['/api/weather/active-fish'] >= 2, '切图要重发活跃鱼种');
    // 下拉没有被重建：选项仍是 3 个且当前值就是刚选的那张图。
    assert.equal(await page.locator('#wxMap option').count(), 3);
    assert.equal(await page.locator('#wxMap').inputValue(), 'level_012_north_sea');

    // 改天数：只重发预览（活跃鱼种与天数无关，不该被拖着重取）。
    const fishBefore = hits['/api/weather/active-fish'];
    await page.selectOption('#wxDays', '14');
    await page.waitForFunction(() => document.querySelector('#wxStatus').textContent.includes('14 天'), null, { timeout: 10000 });
    assert.ok(calls.some((c) => c.path === '/api/weather/preview' && c.query.includes('days=14')), '改天数要把 days=14 传给后端');
    assert.equal(hits['/api/weather/active-fish'], fishBefore, '改天数不该重取活跃鱼种');

    assert.deepEqual(errors, []);
    await page.close();
  } finally { await browser.close(); }
});

test('批次 5 · 前端只画不算：不自己重算昼夜曲线与热响应', async () => {
  const browser = await launch();
  try {
    const { page, errors } = await openWeather(browser);
    await page.locator('#wxFishBox .wx-frow:not(.wx-fhead)').first().waitFor();

    // 页面上的「综合」必须逐字等于后端给的 score，不能是前端拿两个分项再乘一遍。
    // fixture 里 diel_factor × thermal_fact 与 score 刻意不严格相等
    // （1.00 × 0.95 = 0.95 ≠ 1.42），前端若自己乘就会露馅。
    const shown = await page.locator('#wxFishBox .wx-frow:not(.wx-fhead):not(.idle) .num.sc')
      .evaluateAll((ns) => ns.map((n) => n.textContent.trim()));
    assert.deepEqual(shown, ACTIVE.fish.map((f) => f.score.toFixed(2)), '综合系数必须直接用后端 score');

    // 昼夜/水温两个分项也必须原样显示。
    const nums = await page.locator('#wxFishBox .wx-frow:not(.wx-fhead):not(.idle) .num:not(.sc)')
      .evaluateAll((ns) => ns.map((n) => n.textContent.trim()));
    assert.deepEqual(nums, ACTIVE.fish.flatMap((f) => [f.diel_factor.toFixed(2), f.thermal_fact.toFixed(2)]));

    assert.deepEqual(errors, []);
    await page.close();
  } finally { await browser.close(); }
});

test('批次 5 · 口径文案写在页面上，且公式只是展示不是计算', async () => {
  const browser = await launch();
  try {
    const { page, errors } = await openWeather(browser);
    await page.locator('#wxFishBox .wx-frow:not(.wx-fhead)').first().waitFor();

    const box = await page.locator('#wxFishBox').innerText();
    // 「不含饵料…」必须写出来，否则管理员会把它当成「一定能钓到」。
    assert.match(box, /不含饵料/);
    assert.match(box, /水层/);
    assert.match(box, /钓组/);
    // 两个系数的口径（峰值 1.0 / 最低 0.08 / 禁口阈值 0.5）也要能读到。
    assert.match(box, /0\.5/);
    assert.match(box, /0\.08/);
    // 头部的「游戏时钟 1 小时 = 现实 150 秒」是这一页的核心前提，不能丢。
    const pageText = await page.locator('#view').innerText();
    assert.match(pageText, /150 秒/);
    // 日号是世界时钟轴：fixture 给的是 99185，页面必须原样显示（不许减成「第 X 天」）。
    assert.match(await page.locator('#wxSum').innerText(), /99185/);

    assert.deepEqual(errors, []);
    await page.close();
  } finally { await browser.close(); }
});

test('批次 5 · 源码守门：卡片不是 button，且不搬旧后台的深色硬编码', async () => {
  const src = readPage('weather.js');
  const stripComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
  const code = stripComments(src);

  // 7 天卡片必须是 div + role=button：<button> 会被主题里那条全局 !important 涂成深墨块。
  assert.match(code, /h\('div',\s*\{[\s\S]{0,200}class:\s*'wx-day/, '7 天卡片应是 div');
  assert.doesNotMatch(code, /h\('button',\s*\{[\s\S]{0,200}class:\s*'wx-day/, '7 天卡片不能是 button');

  // 旧后台的天气样式把颜色写死在深色底上；SPA 有明暗两套主题，必须走变量。
  for (const hex of ['#0e1b29', '#0c1826', '#0a1725', '#08131f', '#1b2e42', '#101d1b', '#172a3d']) {
    assert.ok(!code.includes(hex), `源码里不该再出现旧后台的深色硬编码 ${hex}`);
  }
  // 前端不得重算天气：出现 Math.exp / Math.cos 就是在自己实现热响应或日变化。
  assert.doesNotMatch(code, /Math\.exp\s*\(/, '前端不该自己算高斯热响应');
  assert.doesNotMatch(code, /Math\.cos\s*\(/, '前端不该自己算气温日变化');

  // 只允许调这三个天气接口，不许自己拼别的数据源。
  const apis = [...code.matchAll(/api\('(\/api\/[^'"?]+)/g)].map((m) => m[1]);
  const allowed = new Set(['/api/weather/maps', '/api/weather/preview', '/api/weather/active-fish']);
  for (const a of apis) assert.ok(allowed.has(a), `天气页不该调用 ${a}`);
  assert.equal(new Set(apis).size, 3, '三个天气接口都该被用到');
});