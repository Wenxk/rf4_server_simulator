// 批次 3 守门测试：饵料页（pages/bait.js + pages/bait-picker.js）。
//
// 对应旧后台三个测试的行为，在 SPA 侧复刻：
//   bait_picker_cascade_ui_test.cjs  六层级联、型号名 > 形态词 > 整类词的写入优先级
//   bait_rules_scope_ui_test.cjs     跨类别同名型号必须用 `类别@饵名` 限定写法
//   bait_rules_ui_test.cjs           自动抽取开关 + 立即重抽 + 前置条件下拉
//   fish_bait_auto_ui_test.cjs       本期开口三种状态（抽中 / 型号不足 / 总开关未开）
//
// 夹具刻意做成真实形状，否则测不出「层级越深收束越窄」和「写进去的是哪个词」：
//   拟饵 17 → 旋转亮片路亚(1) → 品牌 zeiman → 型号 Spiker #2 → 18 规格
//   鱼饵 27 → 香煮成饵(8) 带「下沉 / Pop-up」两形态
//   `鱼子` 同时存在于 香煮成饵(8) 与 颗粒(9)，两处都带 scope_term 与 shared_with
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path');
const { chromium } = require('playwright');

const webRoot = path.join(__dirname, 'web');
const spaHTML = fs.readFileSync(path.join(webRoot, 'index.html'), 'utf8');

const CATEGORIES = {
  categories: [
    { key: 27, label: '鱼饵', count: 585, term: '鱼饵', children: [
      { sub: 1, label: '自然的', count: 4, term: '自然的', brands: [
        { kind: '', label: '', count: 4, lines: [{ base: '一束藻类', count: 4, term: '一束藻类' }] },
      ] },
      { sub: 8, label: '香煮成饵（下沉 + Pop-up）', count: 578, term: '香煮成饵',
        shapes: [
          { shape: 'sink', label: '下沉香煮成饵', term: '下沉香煮成饵', count: 356 },
          { shape: 'popup', label: 'Pop-up 香煮成饵', term: 'Pop-up 香煮成饵', count: 222 },
        ],
        brands: [
          { kind: 'craft_baits', label: 'craft_baits', count: 578, lines: [
            { base: '基础的橙色', count: 6, term: '基础的橙色' },
            // 跨类别同名：`鱼子` 在 香煮成饵 与 颗粒 两处都有（真实库 3 处、14 款）。
            // 后端算法：同名的第二个类别必须给出 scope_term，否则一个 `鱼子`
            // 会把另外几个类别下的同名饵一起放行。
            { base: '鱼子', count: 4, term: '鱼子', scope_term: '香煮成饵@鱼子', shared_with: ['颗粒'] },
          ] },
        ] },
      { sub: 9, label: '颗粒', count: 3, term: '颗粒', brands: [
        { kind: 'craft_baits', label: 'craft_baits', count: 3, lines: [
          { base: '鱼子', count: 3, term: '鱼子', scope_term: '颗粒@鱼子', shared_with: ['香煮成饵'] },
        ] },
      ] },
    ] },
    { key: 17, label: '拟饵', count: 296, term: '拟饵', children: [
      { sub: 1, label: '旋转亮片路亚', count: 296, term: '旋转亮片路亚', brands: [
        { kind: 'zeiman', label: 'zeiman', count: 144, lines: [
          { base: 'Spiker #2', count: 18, term: 'Spiker #2' },
          { base: 'Hornet #1', count: 18, term: 'Hornet #1' },
        ] },
        { kind: 'syberia', label: 'syberia', count: 152, lines: [
          { base: 'Dragonfly Original 1', count: 8, term: 'Dragonfly Original 1' },
        ] },
      ] },
    ] },
  ],
  terms: [
    { term: '各类饵', label: '全部（鱼饵 + 拟饵）', cat: 0, sub: 0, count: 881 },
    { term: '鱼饵', label: '鱼饵（全部天然饵）', cat: 27, sub: 0, count: 585 },
    { term: '拟饵', label: '拟饵（全部路亚）', cat: 17, sub: 0, count: 296 },
    { term: '香煮成饵', label: '香煮成饵', cat: 27, sub: 8, count: 578 },
    { term: '颗粒', label: '颗粒', cat: 27, sub: 9, count: 3 },
    { term: '下沉香煮成饵', label: '下沉香煮成饵', cat: 27, sub: 8, count: 356, shape: 'sink' },
    { term: 'Pop-up 香煮成饵', label: 'Pop-up 香煮成饵', cat: 27, sub: 8, count: 222, shape: 'popup' },
  ],
};

// Spiker #2 下的 18 个规格：编号 001..018，base 全是 `Spiker #2`。
const SPIKER = Array.from({ length: 18 }, (_, i) => ({
  item_id: 'spk2_' + String(i + 1).padStart(3, '0'),
  name_zh: 'Spiker #2 ' + String(i + 1).padStart(3, '0'),
  category: 17, sub: 1, sub_label: '旋转亮片路亚', brand: 'zmn_spiker2', kind: 'zeiman', base: 'Spiker #2',
}));

function itemsFor(u) {
  const q = u.searchParams;
  const cat = Number(q.get('cat') || 0), sub = Number(q.get('sub') || 0);
  const kind = q.get('kind') || '', base = q.get('base') || '';
  let pool = [];
  if (cat === 17 && sub === 1) pool = SPIKER.slice();
  else if (cat === 27 && sub === 8) pool = [{ item_id: 'b1', name_zh: '甜奶油 16', category: 27, sub: 8, sub_label: '香煮成饵', kind: 'craft_baits', base: '甜奶油' }];
  else if (cat === 27 && sub === 9) pool = [{ item_id: 'p1', name_zh: '鱼子 14', category: 27, sub: 9, sub_label: '颗粒', kind: 'craft_baits', base: '鱼子' }];
  else if (cat === 27 && sub === 1) pool = [{ item_id: 'a1', name_zh: '一束藻类 001', category: 27, sub: 1, sub_label: '自然的', kind: '', base: '一束藻类' }];
  if (kind) pool = pool.filter((x) => x.kind === kind);
  if (base) pool = pool.filter((x) => x.base === base);
  return { rows: pool, matched: pool.length, total: 881 };
}

// 取了三条鱼，把「本期开口」列的三种状态全占上：
//   carp   正常抽中 3/47
//   beluga 型号只有 1 种，不足配额 1，本期全开
//   zander 总开关未开，抽签不生效
const PREFS = {
  rules_enabled: true, rule_count: 1,
  rows: [
    { fish_key: 'carp', name_zh: '普通鲤鱼', has_rule: true, source: 'override', enabled: true,
      allow: ['甜奶油'], deny: [], bottom_only: [], condition: '', auto_rotation: true,
      rotation: { enabled: true, mode: 'auto', total: 47, active: 3, quota: 3, pool_size: 47,
        active_baits: ['甜奶油 PopUp（16/20）', '玉米粒（8/14）', '红蚯蚓（10/14/18）'], next_switch: 0 } },
    { fish_key: 'beluga', name_zh: '欧洲鳇', has_rule: true, source: 'override', enabled: true,
      allow: [], deny: [], bottom_only: [], condition: '', auto_rotation: true,
      rotation: { enabled: false, mode: 'auto', total: 1, active: -1, quota: 1, pool_size: 1, active_baits: [], next_switch: 0 } },
    { fish_key: 'zander', name_zh: '梭鲈', has_rule: true, source: 'override', enabled: true,
      allow: [], deny: [], bottom_only: [], condition: '海图120号区域', auto_rotation: true,
      rotation: { enabled: true, mode: 'auto', total: 20, active: 2, quota: 2, pool_size: 20,
        active_baits: ['红蚯蚓（14/18）'], next_switch: 0, global_off: true } },
  ],
};

// 打开 SPA 的饵料页，返回 page / 写请求记录 / 页面错误。
// 注意 page.route('**/*') 先注册者优先，所以写请求的记录必须塞在这里，
// 不能事后补一个 handler——后来的不生效，这条踩过。
async function openBait(browser) {
  const page = await browser.newPage({ viewport: { width: 1520, height: 980 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  const writes = {};
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    if (u.pathname.includes('/api/')) {
      if (r.request().method() !== 'GET') {
        let payload = null;
        try { payload = r.request().postDataJSON(); } catch { payload = r.request().postData(); }
        writes[u.pathname] = payload;
      }
      if (u.pathname.endsWith('/api/bait/categories')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(CATEGORIES) });
      if (u.pathname.endsWith('/api/bait/prefs')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(PREFS) });
      if (u.pathname.endsWith('/api/bait/items')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(itemsFor(u)) });
      if (u.pathname.endsWith('/api/bait/auto-rotation/reroll')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true, round: 7 }) });
      if (u.pathname.endsWith('/api/bait/auto-rotation')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
      if (u.pathname.endsWith('/api/bait/rules')) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ok: true }) });
      if (u.pathname.endsWith('/api/fish/areas')) return r.fulfill({ status: 200, contentType: 'application/json', body: '[]' });
      return r.fulfill({ status: 200, contentType: 'application/json', body: '{}' });
    }
    if (u.pathname === '/' || u.pathname.endsWith('/index.html')) {
      return r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: spaHTML });
    }
    const rel = decodeURIComponent(u.pathname.replace(/^\/+/, ''));
    const file = path.join(webRoot, rel);
    if (fs.existsSync(file) && fs.statSync(file).isFile()) {
      const ext = path.extname(file).toLowerCase();
      const mime = { '.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.jpg': 'image/jpeg', '.webp': 'image/webp', '.png': 'image/png', '.json': 'application/json' }[ext] || 'application/octet-stream';
      return r.fulfill({ status: 200, contentType: mime, body: fs.readFileSync(file) });
    }
    return r.fulfill({ status: 404, body: '' });
  });
  await page.addInitScript(() => localStorage.setItem('rf4_admin_token', 'fixture'));
  await page.goto('http://127.0.0.1:1/#/bait', { waitUntil: 'networkidle', timeout: 30000 });
  await page.locator('.fishcard').first().waitFor();
  return { page, errors, writes };
}

// 标签纯文本（剥掉尾部「×」删除按钮）。
// 不能用 childNodes[0].nodeValue：类别限定标签 `颗粒@鱼子` 的第一个子节点是
// <span class="sc">（元素节点，nodeValue 为 null），会取到空串。
async function chipTexts(page) {
  return await page.locator('.side-page .chip').evaluateAll((els) => els.map((e) => {
    const btn = e.querySelector('button');
    const t = e.textContent || '';
    return (btn ? t.replace(btn.textContent || '', '') : t).replace(/\s+/g, ' ').trim();
  }));
}

// 某一层里的按钮文本。层名可能是空串（二级类型那一层），所以按层索引取更稳。
async function layerOpts(page, k) {
  const sel = k === '' ? '.side-page .br-layer' : `.side-page .br-layer:has(.k:text-is("${k}"))`;
  return await page.locator(sel).locator('button.br-opt').allInnerTexts();
}

async function pickLayer(page, k, label) {
  const sel = `.side-page .br-layer:has(.k:text-is("${k}"))`;
  await page.locator(sel).locator('button.br-opt').filter({ hasText: label }).first().click();
}

test('批次 3 · 本期开口三种状态与自动抽取开关', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const { page, errors, writes } = await openBait(browser);

    const cards = await page.locator('.fishcard').allInnerTexts();
    const carp = cards.find((t) => t.includes('普通鲤鱼')) || '';
    const beluga = cards.find((t) => t.includes('欧洲鳇')) || '';
    const zander = cards.find((t) => t.includes('梭鲈')) || '';

    // 正常抽中：配额/池子 + 具体饵名（按型号折叠）。
    assert.match(carp, /本期开口 3\/47/, '应显示配额/池子：' + carp);
    assert.match(carp, /甜奶油 PopUp（16\/20）/, '应显示折叠后的饵名：' + carp);
    assert.match(carp, /红蚯蚓（10\/14\/18）/, '应显示多规格折叠：' + carp);
    assert.match(carp, /同型号各规格通用/, '应说明同型号等价：' + carp);
    // 型号不足配额：说明本期全开，不假装抽过。
    assert.match(beluga, /型号只有 1 种，不足配额 1，本期全开/, '型号不足应明说：' + beluga);
    // 总开关未开：抽签结果照算但不生效，必须提醒。
    assert.match(zander, /总开关未开，抽签不生效/, '总开关未开应提醒：' + zander);

    // 自动抽取开关：勾选 / 取消都打 POST /api/bait/auto-rotation，且带 fish_key 而不是中文名
    // （开关表以 fish_key 为主键，中文名在「泥鳅 / 泥鳅鱼」这类按水域改名时会撞车）。
    const firstBox = page.locator('.fishcard').first().locator('.autobox input');
    assert.equal(await firstBox.count(), 1, '每条鱼都应有自动抽取开关');
    const wasOn = await firstBox.isChecked();
    await firstBox.click();
    await page.waitForFunction(() => true);
    assert.deepEqual(writes['/api/bait/auto-rotation'], { fish_key: 'carp', enabled: !wasOn },
      '切换应带 fish_key 与 enabled：' + JSON.stringify(writes['/api/bait/auto-rotation']));

    // 立即重抽：先确认再 POST，返回轮次写进提示。
    await page.getByRole('button', { name: '↻ 立即重抽本期' }).click();
    await page.getByRole('button', { name: '确认执行' }).click();
    await page.waitForFunction(() => document.querySelector('.toast'));
    assert.equal(writes['/api/bait/auto-rotation/reroll'] !== undefined, true, '应立即重抽');
    const toastText = await page.locator('.toast').last().innerText().catch(() => '');
    assert.match(toastText, /第 7 轮/, '重抽后应提示当前轮次：' + toastText);

    assert.deepEqual(errors, [], '页面不应有 JS 报错：' + errors.join(' | '));
    await page.close();
  } finally { await browser.close(); }
});

test('批次 3 · 六层级联：越深越窄，品牌不进规则', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const { page, errors } = await openBait(browser);

    await page.locator('.fishcard').first().locator('.edit').click();
    await page.getByRole('button', { name: '添加饵' }).first().click();
    await page.locator('.side-page .br-layer').first().waitFor();

    const layers = () => page.locator('.side-page .br-layer .k').allInnerTexts();
    let ks = await layers();
    assert.ok(ks.includes('类型'), '缺类型层：' + ks.join('|'));

    // --- 选拟饵：必须选到二级类型才出现品牌/型号 ---
    await pickLayer(page, '类型', '拟饵');
    ks = await layers();
    assert.ok(!ks.includes('品牌'), '未选二级类型时不该出现品牌层：' + ks.join('|'));
    assert.ok(!ks.includes('型号'), '未选二级类型时不该出现型号层：' + ks.join('|'));

    await pickLayer(page, '', '旋转亮片路亚');
    ks = await layers();
    assert.ok(ks.includes('品牌'), '选二级类型后应出现品牌层：' + ks.join('|'));
    assert.ok(ks.includes('型号'), '选二级类型后应出现型号层：' + ks.join('|'));
    // 未选品牌时，所有品牌的型号平铺
    let models = await layerOpts(page, '型号');
    assert.ok(models.some((x) => x.includes('Spiker #2')), '型号层应含 Spiker #2：' + models.join('|'));
    assert.ok(models.some((x) => x.includes('Dragonfly')), '未选品牌时应平铺所有品牌型号：' + models.join('|'));
    // 品牌层绝不能有 ＋ 勾选钮——品牌名写进规则匹配不到任何饵。
    const brandTicks = await page.locator('.side-page .br-layer:has(.k:text-is("品牌")) .tick').count();
    assert.equal(brandTicks, 0, '品牌层不该有勾选钮（品牌名不进规则）');

    // --- 选品牌 zeiman：型号层只剩该品牌 ---
    await pickLayer(page, '品牌', 'zeiman');
    models = await layerOpts(page, '型号');
    assert.ok(models.some((x) => x.includes('Spiker #2')), 'zeiman 下应有 Spiker #2');
    assert.ok(!models.some((x) => x.includes('Dragonfly')), '选 zeiman 后不该出现 syberia 的型号：' + models.join('|'));

    // --- 选型号：预览要写「型号名」，并说明覆盖全部规格 ---
    await pickLayer(page, '型号', 'Spiker #2');
    const preview = await page.locator('.side-page .br-preview').innerText();
    assert.match(preview, /拟饵 › 旋转亮片路亚 › zeiman › Spiker #2/, '收束路径不对：' + preview);
    assert.match(preview, /写进规则的是/, '预览应说明会写什么：' + preview);
    assert.equal((await page.locator('.side-page .br-preview code').first().innerText()).trim(), 'Spiker #2', '写进规则的词应是型号名');

    // --- 规格层：选了型号才拉，且只列该型号的；数量取 matched ---
    await page.locator('#brSpecBox .br-spec').first().waitFor();
    const specText = await page.locator('#brSpecBox').innerText();
    assert.match(specText, /该型号共 18 个规格/, '规格数应取 matched，不是 rows 长度：' + specText);
    assert.match(specText, /只加入型号名就已全部覆盖/);
    assert.equal(await page.locator('#brSpecBox .br-spec').count(), 18, '规格列表应列出 18 项');

    // --- 勾一个具体规格 + 整型号：两个都要能进去，且写的是不同的词 ---
    await page.locator('#brSpecBox .br-spec').first().click();
    let bar = await page.locator('.side-page .br-pickbar .info').innerText();
    assert.match(bar, /已勾选 1 项/, '勾选后底部应显示 1 项：' + bar);
    await page.locator('.side-page button.br-opt.add').filter({ hasText: '加入型号' }).click();
    bar = await page.locator('.side-page .br-pickbar .info').innerText();
    assert.match(bar, /已勾选 2 项/, '跨层勾选应累积：' + bar);
    await page.locator('.side-page .br-pickbar .acts button.btn.good').click();
    await page.locator('.side-page .br-pickbar').waitFor({ state: 'detached' });

    let chips = await chipTexts(page);
    assert.ok(chips.includes('Spiker #2 001'), '点具体规格应加入完整商品名：' + chips.join('|'));
    assert.ok(chips.includes('Spiker #2'), '型号名应作为一个标签：' + chips.join('|'));
    assert.equal(chips.filter((t) => t === 'Spiker #2').length, 1, '型号名不应重复加入：' + chips.join('|'));

    assert.deepEqual(errors, [], '页面不应有 JS 报错：' + errors.join(' | '));
    await page.close();
  } finally { await browser.close(); }
});

test('批次 3 · 形态层与跨类别同名型号的限定写法', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const { page, errors, writes } = await openBait(browser);

    await page.locator('.fishcard').first().locator('.edit').click();
    await page.getByRole('button', { name: '添加饵' }).first().click();
    await page.locator('.side-page .br-layer').first().waitFor();

    // --- 形态层：香煮成饵才有「下沉 / Pop-up」---
    await pickLayer(page, '类型', '鱼饵');
    await pickLayer(page, '', '香煮成饵');
    const ks = await page.locator('.side-page .br-layer .k').allInnerTexts();
    assert.ok(ks.includes('形态'), '香煮成饵应出现形态层：' + ks.join('|'));
    const shapes = await layerOpts(page, '形态');
    assert.ok(shapes.some((x) => x.includes('下沉')), '形态层缺下沉：' + shapes.join('|'));
    assert.ok(shapes.some((x) => x.includes('Pop-up')), '形态层缺 Pop-up：' + shapes.join('|'));
    // 选 Pop-up 后写进规则的必须是形态词，不是整类词
    await pickLayer(page, '形态', 'Pop-up');
    assert.equal((await page.locator('.side-page .br-preview code').first().innerText()).trim(), 'Pop-up 香煮成饵', '形态层应写形态词');
    await page.locator('.side-page button.br-opt.add').filter({ hasText: '加入整类' }).click();
    await page.locator('.side-page .br-pickbar .acts button.btn.good').click();
    await page.locator('.side-page .br-pickbar').waitFor({ state: 'detached' });
    let chips = await chipTexts(page);
    assert.ok(chips.includes('Pop-up 香煮成饵'), '应加入 Pop-up 形态词：' + chips.join('|'));

    // --- 跨类别同名：颗粒 与 香煮成饵 都有「鱼子」，必须写成 `类别@饵名` ---
    await page.getByRole('button', { name: '添加饵' }).first().click();
    await page.locator('.side-page .br-layer').first().waitFor();
    await pickLayer(page, '类型', '鱼饵');
    await pickLayer(page, '', '颗粒');
    // 未选型号时写整类词
    assert.equal((await page.locator('.side-page .br-preview code').first().innerText()).trim(), '颗粒', '未选型号应写整类词');
    // 型号按钮上必须有「同名×N」徽标——这是唯一能看见同名的地标。
    await pickLayer(page, '型号', '鱼子');
    const preview = await page.locator('.side-page .br-preview').innerText();
    assert.match(preview, /颗粒@鱼子/, '预览应显示类别限定写法：' + preview);
    assert.equal((await page.locator('.side-page .br-preview code').first().innerText()).trim(), '颗粒@鱼子', '跨类别同名必须写限定写法');
    assert.match(preview, /同名型号还出现在/, '应提示同名还在别的类别：' + preview);
    assert.match(preview, /香煮成饵/, '提示里应点名另一个类别：' + preview);
    const badges = await page.locator('.side-page .badge').allInnerTexts();
    assert.ok(badges.some((x) => x.includes('同名')), '同名型号按钮应有徽标：' + badges.join('|'));

    // 勾上颗粒的鱼子
    await page.locator('.side-page button.br-opt.add').filter({ hasText: '加入型号' }).click();
    // 切到香煮成饵，勾它自己的鱼子——两个限定词不同，必须允许共存
    await pickLayer(page, '', '香煮成饵');
    await pickLayer(page, '形态', '两种都要');
    await pickLayer(page, '型号', '鱼子');
    assert.equal((await page.locator('.side-page .br-preview code').first().innerText()).trim(), '香煮成饵@鱼子', '香煮成饵的鱼子应写成限定写法');
    await page.locator('.side-page button.br-opt.add').filter({ hasText: '加入型号' }).click();
    const bar = await page.locator('.side-page .br-pickbar .info').innerText();
    assert.match(bar, /已勾选 2 项/, '跨类别同名应能同时勾选：' + bar);
    await page.locator('.side-page .br-pickbar .acts button.btn.good').click();
    await page.locator('.side-page .br-pickbar').waitFor({ state: 'detached' });

    chips = await chipTexts(page);
    const joined = chips.join('|');
    // 展示层把 `颗粒@鱼子` 拆成「颗粒 › 鱼子」，所以标签文本里是 › 不是 @；
    // 落库值仍是 `颗粒@鱼子`，由下面的保存断言单独守。
    assert.ok(chips.some((t) => t === '颗粒›鱼子' || t === '颗粒@鱼子'), '应加入颗粒的鱼子：' + joined);
    assert.ok(chips.some((t) => t === '香煮成饵›鱼子' || t === '香煮成饵@鱼子'), '应加入香煮成饵的鱼子：' + joined);
    // 限定标签必须带专属样式，否则两个同名标签看起来一模一样
    assert.equal(await page.locator('.side-page .chip.scope').count(), 2, '两个限定标签都应带 scope 样式');
    // 整类词标签也要能区分（Pop-up 是形态词，属于整类）
    assert.ok(await page.locator('.side-page .chip.wild').count() >= 1, '整类词标签应有专属样式');

    // --- 保存：限定词原样落库，品牌名一个都不能有 ---
    await page.getByRole('button', { name: '保存' }).click();
    await page.waitForFunction(() => !document.querySelector('.side-page'));
    const saved = writes['/api/bait/rules'] || {};
    const allow = saved.allow || [];
    assert.ok(allow.includes('颗粒@鱼子'), '保存的 allow 应含 颗粒@鱼子：' + allow.join('|'));
    assert.ok(allow.includes('香煮成饵@鱼子'), '保存的 allow 应含 香煮成饵@鱼子：' + allow.join('|'));
    assert.ok(allow.includes('Pop-up 香煮成饵'), '保存的 allow 应含形态词：' + allow.join('|'));
    assert.ok(!allow.some((v) => /zeiman|syberia|craft_baits/.test(v)), '品牌名不该写进规则：' + allow.join('|'));
    // 主键必须是 fish_key：只传中文名会让「同一鱼种多名」再次分裂成两行。
    assert.equal(saved.fish_key, 'carp', '保存必须带 fish_key：' + JSON.stringify(saved));
    assert.equal(saved.fish_zh, '普通鲤鱼', '中文名仍要带上，供审计日志读人话');

    assert.deepEqual(errors, [], '页面不应有 JS 报错：' + errors.join(' | '));
    await page.close();
  } finally { await browser.close(); }
});

test('批次 3 · 前置条件下拉的反解与拼回', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const { page, errors, writes } = await openBait(browser);

    // 空条件打开 = 「不限制」
    await page.locator('.fishcard').first().locator('.edit').click();
    assert.equal(await page.locator('#brCondType').inputValue(), 'none', '无条件应反解成 none');
    assert.equal(await page.locator('#brCondZones').count(), 0, 'none 时不显示区号输入框');
    // 选「海图·指定区号」后按区号拼回服务端认的原句
    await page.locator('#brCondType').selectOption('chart');
    await page.locator('#brCondZones').fill('30 / 34');
    await page.getByRole('button', { name: '保存' }).click();
    await page.waitForFunction(() => !document.querySelector('.side-page'));
    assert.equal((writes['/api/bait/rules'] || {}).condition, '海图30或34号区域', '区号应拼成服务端认的原句');

    // 已有条件要能反解回下拉：海图120号区域 → chart + 120，保存后原样回写
    await page.locator('.fishcard').nth(2).locator('.edit').click();
    assert.equal(await page.locator('#brCondType').inputValue(), 'chart', '已有海图条件应反解成 chart');
    assert.equal(await page.locator('#brCondZones').inputValue(), '120', '区号应反解出来');
    await page.getByRole('button', { name: '保存' }).click();
    await page.waitForFunction(() => !document.querySelector('.side-page'));
    assert.equal((writes['/api/bait/rules'] || {}).condition, '海图120号区域', '原句应原样回写');

    assert.deepEqual(errors, [], '页面不应有 JS 报错：' + errors.join(' | '));
    await page.close();
  } finally { await browser.close(); }
});

test('批次 3 · 手工轮换组已彻底移除', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const { page, errors, writes } = await openBait(browser);
    await page.locator('.fishcard').first().locator('.edit').click();
    // 手工编组（按时间换一批饵）已被「自动抽取」取代：吃口资格只认三份清单。
    assert.equal(await page.getByText('定时轮换', { exact: false }).count(), 0, '手工轮换组标题不该存在');
    assert.equal(await page.getByRole('button', { name: '新增一组' }).count(), 0, '新增一组按钮不该存在');
    assert.equal(await page.getByText('多久换一组').count(), 0, '轮换间隔不该存在');
    assert.equal(await page.getByText('轮换方式').count(), 0, '轮换模式不该存在');
    // 保存也不带那些字段，否则后端会存下无用数据
    await page.getByRole('button', { name: '保存' }).click();
    await page.waitForFunction(() => !document.querySelector('.side-page'));
    const saved = writes['/api/bait/rules'] || {};
    assert.equal(saved.groups, undefined, '保存不应再带手工轮换组');
    assert.equal(saved.rotate_sec, undefined, '保存不应再带手工轮换间隔');
    assert.equal(saved.rotate_mode, undefined, '保存不应再带手工轮换模式');
    assert.deepEqual(errors, [], '页面不应有 JS 报错：' + errors.join(' | '));
    await page.close();
  } finally { await browser.close(); }
});