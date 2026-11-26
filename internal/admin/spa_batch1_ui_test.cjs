// 批次 1 守门测试：SPA 各页面在真实浏览器里能渲染，且不含裸 null 等文本泄漏。
// 用接口替身喂固定数据，不依赖真实数据库。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;
const SPA = path.join(__dirname, 'web/index.html');
const spaHTML = fs.readFileSync(SPA, 'utf8');
const webRoot = path.join(__dirname, 'web');

// 各页面用到的接口替身：只给页面渲染必需的字段，避免依赖真实库。
function fixtures(pathname) {
  if (pathname.endsWith('/api/me')) return { name: 'admin' };
  // 鱼饵页分页：故意给超过一页的条数，复现「再显示 N 种」按钮与列表尾部拼接。
  if (pathname.endsWith('/api/bait/prefs')) {
    return {
      rules_enabled: true,
      rows: Array.from({ length: 37 }, (_, i) => ({
        name_zh: '测试鱼' + (i + 1), fish_key: 'test.fish' + (i + 1), tier3: i % 3, has_rule: i % 2 === 0,
        source: i % 5 === 0 ? 'override' : 'rule', enabled: true, locs: ['铜湖'], allow: ['拟饵'], deny: [], bottom: [],
      })),
    };
  }
  if (pathname.endsWith('/api/fish/areas')) return [];
  if (pathname.endsWith('/api/bait/categories')) return { categories: [] };
  if (pathname.endsWith('/api/hooks/profiles')) {
    return {
      profiles: [
        { item_id: 'hook_a', size: 1, display_size: 'S1/0', family: 'classic', brand: 'Test', strength_kg: 5.2, entry_capacity_g: 1200, length_mm: 8, solid_scale: 1, hold_scale: 1, small_fish_floor: 0, over_capacity_hold_floor: 0 },
        { item_id: 'hook_b', size: 24, display_size: 'S24', family: 'carp', brand: 'Test', strength_kg: 9.1, entry_capacity_g: 4000, length_mm: 12, solid_scale: 1.1, hold_scale: 1, small_fish_floor: 0, over_capacity_hold_floor: 0 },
      ],
      total: 2,
      sizes: [{ size: 1, display_size: 'S1/0', count: 1 }, { size: 24, display_size: 'S24', count: 1 }],
    };
  }
  if (pathname.endsWith('/api/dashboard')) return { sessions: 1, players: 3, items: 9, mails: 2, audits: 5, tunables: 7, pg: 'up' };
  // detail 必须先于列表判断，否则 '/api/players' 的 endsWith 会把它一并吃掉，
  // 导致详情页拿到一个空数组、渲染出一片 undefined。
  if (pathname.endsWith('/api/players/detail')) {
    const uid = Number(new URL('http://x/?uid=0').searchParams.get('uid') || 1000002);
    const known = uid !== 1000002;
    return {
      uid, name: known ? '在线甲' : '未知乙', level: known ? 10 : 20, xp: 100, silver: 5, gold: 0, bank: 0,
      banned: false, membership: 0, membership_exp: '', membership_active: true, stars: 0, email: '',
      role: 'player', created_ts: 0,
      item_count: 0, keepnet_count: 0, mail_count: 0,
      items: [], keepnet: [], mails: [], skills: [],
      skill_total: 0, skill_spent: 0, skill_bonus: 0, available_points: 0,
      happy_hour: {},
    };
  }
  if (pathname.endsWith('/api/players')) {
    // online_known=false 的玩家必须显示「状态未上报」，不能显示成离线。
    return [
      { uid: 1000001, name: '在线甲', level: 10, xp: 100, silver: 5, gold: 0, bank: 0, banned: false, online: true, online_known: true, updated_ts: 0 },
      { uid: 1000002, name: '未知乙', level: 20, xp: 200, silver: 6, gold: 0, bank: 0, banned: false, online: false, online_known: false, updated_ts: 0 },
    ];
  }
  if (pathname.endsWith('/api/audit')) return [];
  if (pathname.endsWith('/api/stores')) return [];
  if (pathname.endsWith('/api/tunables')) return {};
  if (pathname.endsWith('/api/fish/bite_mode')) return {};
  return [];
}

async function openPage(browser, route, extraPrepare, writeLog) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  await page.route('**/*', async (r) => {
    const u = new URL(r.request().url());
    if (u.pathname.includes('/api/')) {
      // 记录写请求，供「按钮是否真的接了接口」这类断言使用。
      if (writeLog && r.request().method() !== 'GET') {
        let payload = null;
        try { payload = r.request().postDataJSON(); } catch (e) { payload = r.request().postData(); }
        writeLog.push({ path: u.pathname, payload });
      }
      if (u.pathname.endsWith('/api/players/create')) {
        return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ uid: 1000009, name: '新玩家' }) });
      }
      return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(fixtures(u.pathname)) });
    }
    // 静态资源走磁盘
    if (u.pathname === '/' || u.pathname.endsWith('/index.html')) {
      return r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: spaHTML });
    }
    const rel = decodeURIComponent(u.pathname.replace(/^\/+/, ''));
    const file = path.join(webRoot, rel);
    if (fs.existsSync(file) && fs.statSync(file).isFile()) {
      const ext = path.extname(file).toLowerCase();
      const mime = { '.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.jpg': 'image/jpeg', '.webp': 'image/webp', '.png': 'image/png' }[ext] || 'application/octet-stream';
      return r.fulfill({ status: 200, contentType: mime, body: fs.readFileSync(file) });
    }
    return r.fulfill({ status: 404, body: '' });
  });
  await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
  if (extraPrepare) await extraPrepare(page);
  await page.goto('http://127.0.0.1:1/#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
  await page.waitForTimeout(900);
  return { page, errors };
}

// 裸 null / undefined / NaN 出现在可见文本里，都是渲染缺陷。
async function leakedTokens(page) {
  const text = await page.evaluate(() => (document.getElementById('view') || document.body).innerText || '');
  return ['null', 'undefined', 'NaN'].filter((t) => new RegExp('\\b' + t + '\\b').test(text));
}

test('鱼饵页：超过一页时「再显示」按钮在列表之后，且不渲染裸 null', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'bait');
    const cards = await page.locator('.fishcard').count();
    assert.equal(cards, 30, `首屏应显示 30 张卡片，实得 ${cards}`);
    const more = page.getByRole('button', { name: /再显示/ });
    assert.equal(await more.count(), 1, '超过一页时应有「再显示」按钮');
    assert.deepEqual(await leakedTokens(page), [], '页面可见文本里出现了 null/undefined/NaN');
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('钩子页：钩号用游戏显示值 S1/0，不是内部刻度 #1', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'hooks');
    const body = await page.locator('#view').innerText();
    assert.ok(body.includes('S1/0'), '应显示游戏钩号 S1/0，实际文本: ' + body.slice(0, 300));
    assert.ok(body.includes('S24'), '应显示游戏钩号 S24');
    assert.ok(!/#1\b/.test(body), '不该再出现内部刻度式写法 #1');
    // 下拉里的选项也必须是游戏钩号
    const opts = await page.locator('select[aria-label="钩号"] option').allInnerTexts();
    assert.ok(opts.some((t) => t.includes('S1/0')), '钩号下拉应用 S1/0，实际: ' + opts.join('|'));
    assert.ok(opts.some((t) => t.includes('S24')), '钩号下拉应用 S24');
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('玩家页：online_known=false 显示「状态未上报」，不误报离线', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'players/1000002');
    const body = await page.locator('#view').innerText();
    assert.ok(body.includes('状态未上报'), '未知状态应显示「状态未上报」，实际: ' + body.slice(0, 300));
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('批次 1 七个页面都能渲染且无裸 null', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const pages = ['dashboard', 'players', 'shop', 'hooks', 'mail', 'audit', 'release', 'about'];
  const bad = [];
  try {
    for (const p of pages) {
      const { page, errors } = await openPage(browser, p);
      const leaked = await leakedTokens(page);
      const html = await page.locator('#app').innerHTML();
      if (leaked.length) bad.push(`${p}: 文本泄漏 ${leaked.join(',')}`);
      if (errors.length) bad.push(`${p}: JS 错误 ${errors.join('; ')}`);
      if (html.length < 200) bad.push(`${p}: 渲染内容过少(${html.length})`);
      await page.close();
    }
  } finally { await browser.close(); }
  assert.deepEqual(bad, [], '以下页面有问题:\n' + bad.join('\n'));
});

test('玩家页有「新建玩家」，点了能提交 /api/players/create', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const sent = [];
  try {
    // 带 uid 进入：无 uid 时页面会先 navigate 到第一个玩家，弹窗时机不稳。
    const { page, errors } = await openPage(browser, 'players/1000001', null, sent);
    await page.locator('button', { hasText: '新建玩家' }).first().click();
    await page.waitForTimeout(500);
    assert.ok(await page.getByText('没有密码').count(), '应弹出建档说明');
    // Form 生成的输入框 id 形如 f_name_8，前缀稳定。页面上同时存在「档案编辑」和
    // 「新建玩家」两个同名表单，弹窗后插入 DOM，所以取最后一个。
    await page.locator('input[id^="f_name_"]').last().fill('新玩家');
    await page.getByRole('button', { name: '创建玩家', exact: true }).click();
    await page.waitForTimeout(600);
    assert.equal(sent.length, 1, '应当提交一次创建请求');
    assert.equal(sent[0].path, '/api/players/create', '应打到创建接口');
    assert.equal(sent[0].payload.name, '新玩家', '昵称应带到请求里');
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('玩家页背包里能打开「发放物品」', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'players/1000001');
    // 页签是 #tabs 里的 <a>，不是 button。
    await page.locator('#tabs a', { hasText: '背包' }).first().click();
    await page.waitForTimeout(700);
    const grant = page.getByRole('button', { name: '发放物品' });
    assert.equal(await grant.count(), 1, '背包页应有「发放物品」按钮');
    await grant.click();
    await page.waitForTimeout(400);
    assert.ok(await page.getByText('还没有选物品').count(), '应打开发放面板');
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});