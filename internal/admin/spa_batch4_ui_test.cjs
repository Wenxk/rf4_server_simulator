// 批次 4 守门测试：鱼种档案（生态基线 / 饵料规则 / 经济四档 / 权重档位）。
//
// 三个既有验收测试（ecology_species / econ / fish_rarity）已经覆盖了字段级正确性，
// 这里只守它们守不到、但迁移期最容易翻车的四类事：
//
//   1) **页签是真的懒加载**：一进页面不许并发四个重查询。四个页签里有两个是
//      253 行 × 4 档的重查询，一次性全打出去会把首屏拖到几秒。
//   2) **形态是卡片，不是表格**：2026-11-26 用户明确把生态/经济页从宽表改成
//      卡片（与鱼饵页一致）。宽表在窄屏上要横滚，而这里真正要横向对照的只有
//      「窗宽」和「四档阶梯」，做成条形/一排四格比塞进单元格有用。
//   3) **五个偏好矩阵不下发**：diet/weather/action/substrate/color 是多键 JSON，
//      写进覆盖后没有回滚入口。保存时根本不下发这些键（不是发 null——发 null
//      会把已有矩阵清成「继承基线」，等于静默丢数据）。
//   4) **权重只有三档**：上游有四档 tier3，本服只有三档。多渲染一个输入框，
//      管理员填了也存不进去。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const BROWSER = process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'] || '', 'Microsoft/Edge/Application/msedge.exe');
const webRoot = path.join(__dirname, 'web');
const PAGES = path.join(webRoot, 'app/pages');
const readPage = (f) => fs.readFileSync(path.join(PAGES, f), 'utf8');

// 一条基线：江鳕。数值取真实量级（窗宽 16.9℃ 属「宽」，温度几乎不筛鱼）。
const BASE = {
  fish_key: 'burbot', layer_min: 0.3, layer_pref: 0.75, layer_max: 1,
  temp_min_c: 10.2, temp_pref_c: 18.9, temp_max_c: 27.1,
  diel_mask: 15, diel_curve: '[1,0.3,0.2,0.5]', season_mask: 15,
  diet_json: '{"worm":1}', weather_json: '{}', action_json: '{}', substrate_json: '{}', color_json: '{}',
  current_pref: 0.5, activity: 0.87, light_pref: 0.5, clarity_pref: 0.5, aggression: 1,
  enabled: 1, origin: 'baseline', updated_by: '', updated_ts: 0,
};

const ECO = {
  items: [
    { fish_key: 'burbot', name_zh: '江鳕', rarity_tier: 1, origin: '', overlaid: false, base: BASE, override: null, locs: ['旧奥斯特罗格湖'] },
    { fish_key: 'pike', name_zh: '狗鱼', rarity_tier: 0, origin: 'manual', overlaid: true, base: BASE,
      override: { ...BASE, fish_key: 'pike', season_mask: 9, origin: 'manual', updated_by: 'admin', updated_ts: 1732000000 }, locs: ['铜湖'] },
    { fish_key: 'beluga', name_zh: '欧洲鳇', rarity_tier: 2, origin: '', overlaid: false, base: BASE, override: null, locs: [] },
  ],
  count: 3, total: 3,
};

const GRADES = [1, 2, 3, 4].map((g) => ({
  grade: g,
  base_xp: [0.0735, 0.24, 1.4999, 2.9998][g - 1],
  base_price: [3.31, 4.965, 9.93, 19.86][g - 1],
  xp_c: null, price_per_kg: null,
}));
const ECON = {
  items: [{ fish_key: 'bream', name_zh: '欧鳊', min_g: 100, trophy_g: 700, trophy_line_g: 4400, super_trophy_line_g: 7000, grades: GRADES, overridden: false, price_missing: false }],
  count: 1,
  maps: [{ key: 'level_009_ahtuba', name: '阿赫图巴河', pct: 1 }, { key: 'level_001_lake', name: '克马羚诺也湖', pct: 0.831 }],
};
const TIERS = [
  { tier: 0, label: '普通', weight: 100, species: 169, rows: 200, avg_base: 5.2 },
  { tier: 1, label: '稀有', weight: 30, species: 45, rows: 60, avg_base: 2.1 },
  { tier: 2, label: '超稀有', weight: 5, species: 39, rows: 50, avg_base: 1.1 },
];

// 打开 SPA 鱼种档案页。记录每个 /api/ 路径被请求了几次——懒加载的断言全靠它。
// 注意 page.route('**/*') 先注册者优先：记录与响应必须写在同一个 handler 里。
async function openEcology(browser, hash = '#/ecology') {
  const page = await browser.newPage({ viewport: { width: 1520, height: 1000 } });
  const errors = [];
  const hits = {};
  const writes = {};
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    const p = u.pathname.replace(/^\/admin/, '').replace(/^\/+/, '/');
    if (p.startsWith('/api/')) {
      hits[p] = (hits[p] || 0) + 1;
      if (r.request().method() !== 'GET') {
        let payload = null;
        try { payload = r.request().postDataJSON(); } catch { payload = r.request().postData(); }
        writes[p] = payload;
      }
      const ok = (v) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(v) });
      if (p === '/api/me') return ok({ username: 'admin', item_categories: {} });
      if (p === '/api/fish/ecology-species') return ok(ECO);
      if (p === '/api/econ/species') return ok(ECON);
      if (p === '/api/econ/preview') {
        return ok({ xp: 2000, price_cents: 5000, price_silver: 50, base_xp_rate: 1.4999, base_price: 9.93,
          xp_c: Number(u.searchParams.get('xp_c') || 0), price_per_kg: 0, market_factor: 1.0209,
          map: 'level_009_ahtuba', map_pct: 1, weight_g: 1000, grade: 3, saved: false });
      }
      if (p === '/api/fish/weights/preset') return ok({ weights: [100, 30, 5], tiers: TIERS, rows: 310, active: true });
      if (p === '/api/fish/areas') return ok([]);
      if (p === '/api/bait/prefs') return ok({ rules_enabled: true, rows: [] });
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
  return { page, errors, hits, writes };
}

async function launch() {
  return chromium.launch({ headless: true, executablePath: BROWSER });
}

test('批次 4 · 四个页签懒加载，一进页面只查生态', async () => {
  const browser = await launch();
  try {
    const { page, errors, hits } = await openEcology(browser);
    await page.locator('#ecoTable .fishcard').first().waitFor();

    // 四个页签按钮都要在，且当前选中生态基线。
    const tabs = await page.locator('#ecoTabs button').allInnerTexts();
    assert.deepEqual(tabs, ['生态基线', '饵料规则', '经济四档', '权重档位'], '页签文案或顺序变了：' + tabs.join('|'));
    assert.equal(await page.locator('#ecoTabs button[data-etab="eco"]').evaluate((e) => e.classList.contains('on')), true);

    // 关键：进页面只该查生态，经济与权重一条都不该发出。
    // 两个重查询一起打会把首屏拖到几秒，这是懒加载唯一的存在理由。
    assert.equal(hits['/api/econ/species'], undefined, '一进页面就查了经济四档，懒加载失效');
    assert.equal(hits['/api/fish/weights/preset'], undefined, '一进页面就查了权重档位，懒加载失效');
    assert.equal(hits['/api/fish/ecology-species'], 1, '生态基线应只查一次');

    // 切到权重档位：只补权重，不该倒回去重查生态（生态数据还在内存里）。
    await page.locator('#ecoTabs button[data-etab="weight"]').click();
    await page.locator('#ecoWeightBox #tp_0').waitFor();
    assert.equal(hits['/api/fish/weights/preset'], 1, '权重档位应查一次');
    assert.equal(hits['/api/fish/ecology-species'], 1, '切页签不该重查生态');

    // 面包屑末级跟着页签走，否则看不出自己在哪一页签。
    assert.match(await page.locator('#crumb').innerText(), /权重档位/);

    assert.deepEqual(errors, []);
  } finally { await browser.close(); }
});

test('批次 4 · 生态与经济都是卡片式，不是宽表', async () => {
  const browser = await launch();
  try {
    const { page, errors } = await openEcology(browser);
    await page.locator('#ecoTable .fishcard').first().waitFor();

    // 生态：卡片数 == 鱼种数，且没有表格。
    assert.equal(await page.locator('#ecoTable .fishcard').count(), 3, '三个鱼种应渲染成三张卡片');
    assert.equal(await page.locator('#ecoTable table').count(), 0, '生态页不该再有 <table>（已改卡片式）');
    // 每张卡都要有四块：水层 / 活跃时段 / 活跃度 / 水域。
    const card = await page.locator('#ecoTable .fishcard').first().innerText();
    for (const block of ['栖息水层', '活跃时段', '活跃度 / 攻击性', '出现的水域']) {
      assert.ok(card.includes(block), `生态卡片缺少「${block}」：${card.slice(0, 200)}`);
    }
    // 窗宽判定要写出来：窗宽直接决定热响应曲线陡缓，光给数字看不出「温度根本不筛鱼」。
    assert.match(card, /窗宽判定/, '生态卡片缺少窗宽判定');
    assert.match(card, /热响应容差/, '窗宽判定要给出容差公式的结果');

    // 稀有度标记三档都要能渲染（普通/稀有/超稀有）。
    const all = await page.locator('#ecoTable').innerText();
    for (const t of ['普通', '稀有', '超稀有']) assert.ok(all.includes(t), `生态卡片缺少稀有度「${t}」`);

    // 经济：同样卡片式。
    await page.locator('#ecoTabs button[data-etab="econ"]').click();
    await page.locator('#econTable .fishcard').first().waitFor();
    assert.equal(await page.locator('#econTable table').count(), 0, '经济页不该再有 <table>（已改卡片式）');
    // 四档并排：每张卡两行（经验 / 鱼价），每行四格。
    assert.equal(await page.locator('#econTable .ecogradeline').count(), 2, '经济卡片应有经验、鱼价两行阶梯');
    assert.equal(await page.locator('#econTable .fishcard').first().locator('.ecograde').count(), 8, '两行 × 四档 = 8 格');
    assert.match(await page.locator('#econTable').innerText(), /1\.4999/, '经济卡片应显示内置上星经验值');

    assert.deepEqual(errors, []);
  } finally { await browser.close(); }
});

test('批次 4 · 五个偏好矩阵从不下发，留空一律 null', async () => {
  const browser = await launch();
  try {
    const { page, errors, writes } = await openEcology(browser);
    await page.locator('#ecoTable .fishcard').first().waitFor();
    await page.locator('#ecoTable .fishcard').first().locator('button:has-text("展开修改")').click();
    await page.locator('#eco_s_tpref').waitFor();

    // 界面上不该再有这五个矩阵的输入框。
    for (const id of ['eco_s_diet', 'eco_s_color', 'eco_s_weather', 'eco_s_action', 'eco_s_substrate']) {
      assert.equal(await page.locator('#' + id).count(), 0, `偏好矩阵输入框 ${id} 应已移除`);
    }
    assert.equal(await page.locator('textarea#eco_s_action').count(), 0, '拟饵动作矩阵输入框应已移除');

    // 只改一个温度就保存：没填的必须全是 null。
    await page.locator('#eco_s_tpref').fill('12');
    await page.locator('button:has-text("保存")').click();
    await page.waitForFunction(() => document.querySelector('#toast') && document.querySelector('#toast').textContent.includes('已保存'), null, { timeout: 6000 });

    const w = writes['/api/fish/ecology-species'];
    assert.ok(w, '没有发出保存请求');
    assert.equal(w.temp_pref_c, 12, '填了的值要按数值下发');
    // 留空 = 继承基线 = null，绝不能变成 0（0 是合法的覆盖值：水层 0 = 水面、
    // 季节掩码 0 = 任何季节都不活跃，含义与「继承」完全不同）。
    for (const k of ['layer_min', 'layer_pref', 'layer_max', 'temp_min_c', 'temp_max_c', 'activity', 'aggression', 'season_mask', 'diel_mask', 'enabled']) {
      assert.equal(w[k], null, `${k} 留空时应下发 null，实际是 ${JSON.stringify(w[k])}`);
    }
    // 五个矩阵根本不出现在 payload 里。发 null 会把已有覆盖清成「继承基线」，
    // 等于静默丢掉管理员之前辛苦配的权重表。
    for (const k of ['diet_json', 'weather_json', 'action_json', 'substrate_json', 'color_json']) {
      assert.equal(w[k], undefined, `${k} 不该下发（发 null 会清掉已有覆盖）`);
    }

    assert.deepEqual(errors, []);
  } finally { await browser.close(); }
});

test('批次 4 · 经济四档试算摊开当日行情，权重只有三档', async () => {
  const browser = await launch();
  try {
    const { page, errors, writes } = await openEcology(browser);
    await page.locator('#ecoTable .fishcard').first().waitFor();
    await page.locator('#ecoTabs button[data-etab="econ"]').click();
    await page.locator('#econTable .fishcard').first().waitFor();

    await page.locator('#econTable button:has-text("编辑")').first().click();
    await page.locator('#eco_xp_3').waitFor();
    // 没填过就是空（跟随内置），不能预填内置值——预填等于悄悄写了一份覆盖。
    assert.equal(await page.locator('#eco_xp_3').inputValue(), '');
    assert.equal(await page.locator('#eco_pr_3').inputValue(), '');

    await page.locator('#eco_xp_3').fill('2');
    await page.waitForFunction(() => (document.querySelector('#ecoPreview')?.textContent || '').includes('算式'), null, { timeout: 6000 });
    const pv = await page.locator('#ecoPreview').innerText();
    // 「1kg 卖价 ≠ 每公斤价」永远会被当成算错报上来，所以系数与算式必须写出来。
    assert.match(pv, /当日行情/, '试算缺少当日行情说明');
    assert.match(pv, /1\.0209/, '试算未显示行情系数数值');
    assert.match(pv, /算式/, '试算缺少算式展开');
    assert.match(pv, /水域倍率/, '算式应写出水域倍率');
    assert.match(pv, /新鲜度/, '算式应写出新鲜度');

    // 保存：四档都要带，没填的 xp/pr 是 null。
    await page.locator('button:has-text("保存")').click();
    await page.waitForFunction(() => document.querySelector('#toast') && document.querySelector('#toast').textContent.includes('已保存'), null, { timeout: 6000 });
    const save = writes['/api/econ/species/save'];
    assert.ok(save, '没有发出经济保存请求');
    assert.equal(save.fish_key, 'bream');
    assert.equal(save.grades.length, 4, '四档都要下发');
    assert.equal(save.grades[2].xp_c, 2);
    assert.equal(save.grades[0].xp_c, null, '没填的档位必须是 null');
    assert.equal(save.grades[0].price_per_kg, null);

    // 权重档位：只有三档。多渲染 tier3 就是照抄了上游的四档制。
    await page.locator('#ecoTabs button[data-etab="weight"]').click();
    await page.locator('#ecoWeightBox #tp_0').waitFor();
    assert.equal(await page.locator('#ecoWeightBox #tp_0').inputValue(), '100');
    assert.equal(await page.locator('#ecoWeightBox #tp_1').inputValue(), '30');
    assert.equal(await page.locator('#ecoWeightBox #tp_2').inputValue(), '5');
    assert.equal(await page.locator('#ecoWeightBox #tp_3').count(), 0, '本服只有三档，不该有 tp_3');

    await page.locator('button:has-text("确认重设")').click();
    await page.waitForFunction(() => document.querySelector('#toast') && document.querySelector('#toast').textContent.includes('已生效'), null, { timeout: 6000 });
    // POST body 必须严格只有三个键：多一个键后端不认，少一个键会清掉该档。
    assert.deepEqual(writes['/api/fish/weights/preset'], { tier0: 100, tier1: 30, tier2: 5 });

    assert.deepEqual(errors, []);
  } finally { await browser.close(); }
});

test('批次 4 · 源码层守门：卡片形态与三档制不被写回', () => {
  const ecology = readPage('ecology.js');
  const eco = readPage('ecology-eco.js');
  const econ = readPage('ecology-econ.js');
  const weight = readPage('ecology-weight.js');

  // 生态页不能出现「一张宽表」的写法：h('table') / <thead> 一旦加回来，
  // 253 行在窄屏上就要横滚，而这一页的横向对照需求只有窗宽与水层两项。
  assert.doesNotMatch(eco, /h\('table'/, '生态基线页不应使用 <table>（用户要求卡片式）');
  assert.doesNotMatch(eco, /h\('thead'/, '生态基线页不应有表头');
  // 经济四档的列表也是卡片；侧页编辑器里逐档填数仍用表格——那是「填表」不是
  // 「浏览 253 行」，卡片的优势用不上，所以这里只守列表容器不带表格。
  assert.doesNotMatch(econ, /listEl\.replaceChildren\([^)]*h\('table'/, '经济四档列表不应使用 <table>（用户要求卡片式）');

  // 矩阵键不得出现在保存 payload 里。做一个粗但有效的守门：
  // 保存函数所在文件里不该出现 diet_json 之类的下发写法。
  assert.doesNotMatch(eco, /diet_json:\s*(?!undefined)/, '不得下发 diet_json');
  assert.doesNotMatch(eco, /substrate_json:\s*(?!undefined)/, '不得下发 substrate_json');
  assert.doesNotMatch(eco, /color_json:\s*(?!undefined)/, '不得下发 color_json');

  // 三档制：任何地方渲染出第四档都说明照抄了上游的四档制。
  // 只在**去掉注释**后的代码里查：两份文件都在注释里写了「上游有 tier3，
  // 本服只有三档」这句来历说明，那是要留的，不该被这条断言判成违规。
  const stripComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
  for (const [name, src] of [['ecology.js', ecology], ['ecology-weight.js', weight], ['ecology-econ.js', econ]]) {
    assert.doesNotMatch(stripComments(src), /tier3|tier_3|TIER_NAMES\[3\]|'tp_3'|"tp_3"/, `${name} 出现了第四档`);
  }
  // 权重 POST 必须手写三键，不能 spread 整个表单值（那样会多带键）。
  assert.match(weight, /tier0:\s*w\[0\][\s\S]{0,80}tier1:\s*w\[1\][\s\S]{0,80}tier2:\s*w\[2\]/, '权重 POST 应严格手写三键');

  // 生态页四个页签的文字必须与页面标题一致（改文案时容易只改一处）。
  assert.match(ecology, /ecoTabs/, '页签容器必须带 #ecoTabs（验收测试的锚点）');
  assert.match(ecology, /ECON_TAB_META/, '页签标题应集中在一份元数据里');
});