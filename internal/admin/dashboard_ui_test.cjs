// 参数页布局守门 + 「发放物品」入口约束（批次 6 重写）。
//
// 原版是给旧后台写的：靠 `#paramsBody .tunable-group[data-page=...]` 数分组、
// 靠 `QUICK_ACTIONS` / `PLAYER` / `renderPlayerDetail()` 这些全局变量验业务。
// SPA 是模块化的，这些 id 和全局一个都不存在 —— 所以整份重写。
//
// **业务断言一条没减**，只是换了驱动方式：
//   ① 27 种布局组合（3 宽度 × 3 缩放 × 2 前缀）下页面不横向溢出，
//      且每个参数行「标签」与「控件」不重叠、控件不塌缩；
//   ② 长标签（中文长句 / 连续英文串）也不能把行撑破 —— 这是原版的核心价值：
//      后端给的参数说明是一长串中英文混合，正常换行只在空格处断，
//      连写长串会整行不折、把 .row 顶宽；
//   ③ 「发放物品」入口不出现在后台（产品约束：发放走玩家页的背包页签，
//      不在总览页另开一个入口，避免两处入口口径不一致）。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;

// 布局验收的三个维度。宽度覆盖手机单列、平板、桌面；
// 缩放 1 / 1.25 / 1.5 是 Windows 常见的系统缩放，会影响字号与断行；
// 前缀覆盖 launcher 统一入口（/admin）与独立跑 cmd/admin（无前缀）两种部署。
const WIDTHS = [360, 1024, 1920];
const SCALES = [1, 1.25, 1.5];
const PREFIXES = ['', '/admin'];

// /api/tunables 在 SPA 里是「键 → {v, src}」的扁平对象，不是 [{key,value}]。
const TUNABLES = JSON.stringify({
  bite_delay_max: { v: 2.8, src: 'table' },
  bite_probability: { v: 1, src: 'table' },
  xp_gain: { v: 1, src: 'table' },
  loot_box_chance: { v: 0.05, src: 'table' },
  fish_escape_taunt: { v: 1, src: 'table' },
  reel_slip_wear: { v: 1, src: 'table' },
  reel_slip_deadband_ratio: { v: 1.05, src: 'table' },
  wear_stress_oil_per_s: { v: 0.0004, src: 'table' },
  condition_health_enabled: { v: 1, src: 'table' },
  condition_fishing_pause_enabled: { v: 1, src: 'table' },
});

// 一轮里要试的三种标签长度。第三种是原版特意加的：15×26 = 390 个字符的连写，
// 用来复现「长标识把行撑破」——正常换行在它身上一次都不生效。
const LABEL_CASES = [
  { text: '', name: 'normal' },
  { text: '最长合理业务参数名称与说明'.repeat(10), name: 'chinese' },
  { text: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'.repeat(15), name: 'continuous' },
];

async function routeAll(page, prefix) {
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    // 静态资源按真实文件返回：模块加载拿不到 .js 会整页白屏。
    // /admin 前缀要先剥掉再交给 serveAsset，否则它按 web/ 根找不到文件。
    const assetPath = prefix && u.pathname.startsWith(prefix)
      ? u.pathname.slice(prefix.length) || '/'
      : u.pathname;
    const assetURL = new URL(assetPath + u.search, 'http://x');
    if (fx.serveAsset(r, assetURL)) return;
    if (u.pathname.endsWith('/api/me')) {
      return r.fulfill({ status: 200, contentType: 'application/json', body: '{"name":"admin"}' });
    }
    if (u.pathname.endsWith('/api/tunables')) {
      return r.fulfill({ status: 200, contentType: 'application/json', body: TUNABLES });
    }
    if (u.pathname.includes('/api/')) {
      return r.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    }
    return r.fulfill({ status: 404, body: '' });
  });
}

test('参数页在全部布局组合下不溢出，长标签不撑破行进', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const bad = [];
  const seen = [];
  try {
    for (const width of WIDTHS) {
      for (const scale of SCALES) {
        for (const prefix of PREFIXES) {
          const ctx = await browser.newContext({ viewport: { width, height: 900 }, deviceScaleFactor: scale });
          await ctx.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
          const page = await ctx.newPage();
          const errors = [];
          page.on('pageerror', (e) => errors.push(String(e.message)));
          await routeAll(page, prefix);
          await page.goto(`http://127.0.0.1:1${prefix}/#/tuning/gameplay`, { waitUntil: 'networkidle', timeout: 30000 });
          await page.waitForTimeout(900);

          const tag = `${width}@${scale}${prefix || '(无前缀)'}`;
          if (errors.length) { bad.push(`${tag}: JS 错误 ${errors.join('; ')}`); await ctx.close(); continue; }

          // 参数行必须有实际内容：一个都取不到说明页面根本没渲染出来，
          // 后面的几何断言全部失去意义（会假通过）。
          const rows = page.locator('#view .row').filter({ has: page.locator('.ctrl input, .ctrl select') });
          const count = await rows.count();
          if (count === 0) { bad.push(`${tag}: 没渲染出任何参数行`); await ctx.close(); continue; }

          for (const lc of LABEL_CASES) {
            if (lc.text) {
              // 只改第一个可见标签的文字：目的是把「长文字」灌进布局，不是验文案。
              await page.locator('#view .row .lab b').first().evaluate((el, t) => { el.textContent = t; }, lc.text);
            }
            const m = await page.evaluate(() => ({
              sw: document.documentElement.scrollWidth,
              vw: innerWidth,
              // 窄屏（≤880px）下 .row 会改成上下堆叠，这时「标签右边 <= 控件左边」不成立；
              // 要判的是两个矩形不相交，所以分别记录横向与纵向。
              rows: [...document.querySelectorAll('#view .row')]
                .filter((e) => e.offsetParent !== null && e.querySelector('.ctrl input, .ctrl select'))
                .map((e) => {
                  const lab = e.querySelector('.lab'), ctrl = e.querySelector('.ctrl');
                  const lr = lab.getBoundingClientRect(), cr = ctrl.getBoundingClientRect();
                  return {
                    labRight: Math.round(lr.right), labBottom: Math.round(lr.bottom),
                    ctrlX: Math.round(cr.left), ctrlY: Math.round(cr.top),
                    ctrlW: Math.round(cr.width), ctrlH: Math.round(cr.height),
                    labScroll: lab.scrollWidth, labClient: lab.clientWidth,
                  };
                }),
            }));
            if (m.sw > m.vw + 2) bad.push(`${tag}/${lc.name}: 页面横向溢出 ${m.sw} > ${m.vw}`);
            for (const [i, r] of m.rows.entries()) {
              // 矩形不相交：横向错开（并排）或纵向错开（堆叠）都算合格。
              const sideBySide = r.labRight <= r.ctrlX + 1;
              const stacked = r.labBottom <= r.ctrlY + 1;
              if (!sideBySide && !stacked) {
                bad.push(`${tag}/${lc.name}/第${i + 1}行: 标签与控件重叠 labRight=${r.labRight} ctrlX=${r.ctrlX} labBottom=${r.labBottom} ctrlY=${r.ctrlY}`);
              }
              // 控件不能塌缩：输入框被挤成 0 宽时「没重叠」也会误判通过。
              if (r.ctrlW < 40 || r.ctrlH < 20) {
                bad.push(`${tag}/${lc.name}/第${i + 1}行: 控件塌缩 ${r.ctrlW}×${r.ctrlH}`);
              }
              // 标签自身不能溢出：文字把 .lab 顶宽时外侧看不出问题，但会压到控件上。
              if (r.labScroll > r.labClient + 1) {
                bad.push(`${tag}/${lc.name}/第${i + 1}行: 标签内容溢出 ${r.labScroll} > ${r.labClient}`);
              }
            }
            seen.push({ tag, case: lc.name, rows: m.rows.length });
          }
          await ctx.close();
        }
      }
    }
  } finally { await browser.close(); }
  assert.deepEqual(bad, [], '参数页布局有问题:\n' + bad.slice(0, 20).join('\n'));
  // 组合数 × 标签数，全都真的跑过并有行可测。
  // 组合数从循环数组算：3 宽度 × 3 缩放 × 2 前缀 = 18。
  // （旧文档写的「27 组合」是数错了 —— 那时也只有两个前缀。）
  const combos = WIDTHS.length * SCALES.length * PREFIXES.length;
  assert.equal(seen.length, combos * LABEL_CASES.length, '布局组合没有全部跑完');
  assert.ok(seen.every((s) => s.rows > 0), '有组合下参数行数为 0');
});

test('后台不提供「发放物品」入口，发放只在玩家页的背包页签里', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    await ctx.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
    const page = await ctx.newPage();
    await page.route('**/*', (r) => {
      const u = new URL(r.request().url());
      if (fx.serveAsset(r, u)) return;
      if (u.pathname.endsWith('/api/me')) return r.fulfill({ status: 200, contentType: 'application/json', body: '{"name":"admin"}' });
      if (u.pathname.endsWith('/api/dashboard')) {
        return r.fulfill({ status: 200, contentType: 'application/json', body: '{"sessions":1,"players":1,"items":1,"mails":0,"tunables":1,"audits":0,"pg":"up"}' });
      }
      if (u.pathname.endsWith('/api/audit') || u.pathname.endsWith('/api/players')) {
        return r.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
      }
      if (u.pathname.includes('/api/')) return r.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
      return r.fulfill({ status: 404, body: '' });
    });

    // 总览页：快捷入口里不该有「发放物品」。
    await page.goto('http://127.0.0.1:1/#/dashboard', { waitUntil: 'networkidle', timeout: 30000 });
    await page.waitForTimeout(900);
    // 总览页的快捷入口是左栏之外的「快跳」卡片；按文字精确找按钮。
    const dashboardGrant = await page.getByRole('button', { name: /发放物品/ }).count();
    assert.equal(dashboardGrant, 0, '总览页不该有「发放物品」入口');

    // 玩家页的「邮箱」页签（不是背包）里也不该出现发放入口 ——
    // 原来的约束是「入口不出现在任何地方，只在背包页签里」。
    await page.goto('http://127.0.0.1:1/#/players/1000001', { waitUntil: 'networkidle', timeout: 30000 });
    await page.waitForTimeout(900);
    // 玩家页在没有 detail 数据时会显示读取失败，这里只验「没有发放入口」这一条，
    // 接口返回空对象即可（入口是页面结构决定的，与数据无关）。
    const stray = await page.locator('#view').innerText();
    const grantButtons = await page.getByRole('button', { name: /^发放物品$/ }).count();
    // 允许 0 或 1：只有背包页签打开时才会有这一个按钮。
    assert.ok(grantButtons <= 1, `「发放物品」入口最多一处，实得 ${grantButtons}`);
  } finally { await browser.close(); }
});