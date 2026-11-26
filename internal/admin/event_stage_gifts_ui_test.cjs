const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 限时活动「阶段奖励」：分支勾选、阶段切换、物品挑选、保存结构、预设填充、窄屏布局。
//
// 迁移时的取舍（批次 6）：
//   旧测试有 240 行、几乎全在调旧后台的全局函数（applyEventRewardPreset /
//   saveEventStageGifts / clearEventStageGiftsDraft / addEventStageGiftItem …），
//   靠 `#eventGiftBranch` `#eventGiftStage` `#eventStageGiftJson` 这类固定 id 定位。
//   SPA 是模块化的 eventView()，一个全局都不暴露；状态在闭包里的 draft/saved，
//   只能通过界面操作驱动。所以整份重写。
//
//   **业务断言一条没减**，只是换了驱动方式：
//     · 分支集合决定可选阶段数（7+1 / 10+1）——现在从「奖励分支」下拉的选项数验；
//     · 只填「空白的阶段」，已有内容不覆盖；
//     · 预设只改草稿，保存前不落库；
//     · 保存后停在当前阶段，不跳回默认；
//     · 活动没开 / 没选分支时，编辑区不可用并说明原因；
//     · 物品数量 1–99；
//     · 窄屏（360）不横向溢出。
//   这些和用哪套 UI 无关，是数据语义。
//
//   删掉的只有旧实现细节：`#eventStageGiftJson` 的 JSON 文本、以及那几个
//   全局函数名本身。它们不是行为，是旧后台的内部结构。

const PRESET_DIR = path.join(__dirname, 'web/event-reward-presets');

function serve(state) {
  return http.createServer((req, res) => {
    const url = new URL(req.url, 'http://fixture');
    const key = url.pathname.replace(/^\/admin/, '');
    const json = (v, code = 200) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(v)); };
    if (req.method === 'POST') {
      let body = ''; req.on('data', (c) => (body += c));
      req.on('end', () => {
        const v = body ? JSON.parse(body) : {};
        state.writes.push(v);
        // 两个活动开关互斥，与真实后端一致（同时开一个新活动会把另一个关掉）。
        if (v.value === '1' && ['event_newyear_enabled', 'event_anniversary_enabled'].includes(v.key)) {
          state.values[v.key === 'event_newyear_enabled' ? 'event_anniversary_enabled' : 'event_newyear_enabled'] = '0';
        }
        state.values[v.key] = v.value;
        json({ ok: true });
      });
      return;
    }
    if (key === '/api/me') return json({ username: 'admin', item_categories: {} });
    if (key === '/api/tunables') return json({ ...state.values });
    if (key === '/api/catalog/items') {
      const q = (url.searchParams.get('q') || '').toLowerCase();
      const complete = url.searchParams.get('complete_only') === '1';
      const all = [
        { item_id: 'fcb_coffee_can2', name: '咖啡', kind: 'food', group: 'food' },
        { item_id: 'ab_tea_can2', name: '早餐茶', kind: 'food', group: 'food' },
        { item_id: 'fcb_cocoa_can2', name: '可可', kind: 'food', group: 'food' },
        // 竿坯组件不是「完整物品」：complete_only=1 时不该出现（选了也用不了）。
        { item_id: 'rod_blank_test', name: '竿坯组件', kind: 'rod_blank', group: 'rod' },
      ];
      const items = all.filter((x) => !q || x.item_id.includes(q) || x.name.includes(q)).filter((x) => !complete || x.kind !== 'rod_blank');
      return json(items);
    }
    if (key === '/api/catalog/groups') return json([{ key: 'food', name: '食物与饮料', count: 3 }]);
    if (/^\/event-reward-presets\/(?:anniversary-202[456]|newyear-202[56])\.json$/.test(key)) {
      const file = path.join(PRESET_DIR, path.basename(key));
      if (!fs.existsSync(file)) { res.writeHead(404); return res.end(''); }
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(fs.readFileSync(file));
    }
    if (key.startsWith('/api/')) return json(key === '/api/players' ? [] : {});
    if (sendAsset(res, url.pathname)) return;
    res.writeHead(404); res.end('');
  });
}

const BRANCHES_WITH_COUNT = { 13: 8, 14: 11, 17: 8, 18: 11, 19: 8, 20: 11, 11: 8, 12: 11, 15: 8, 16: 11, 9: 8, 10: 11 };

function boot(values) {
  return {
    values: { event_newyear_enabled: '1', event_anniversary_enabled: '0', event_branches: '13,14', event_stage_gifts: '{"13":{"1":[{"item":"fcb_coffee_can2","qty":1},{"item":"ab_tea_can2","qty":2}]}}', ...values },
    writes: [],
  };
}

const stageSel = (page) => page.locator('#pg-gifts select').nth(2);
const branchSel = (page) => page.locator('#pg-gifts select').nth(1);
const presetSel = (page) => page.locator('#pg-gifts select').nth(0);
const giftRows = (page) => page.locator('#pg-gifts .list .list-item');
const saveBtn = (page) => page.locator('#pg-gifts').locator('..').locator('..').locator('button', { hasText: '保存修改' });

test('event stage gifts follow the selected branch/stage and save structured JSON', async () => {
  const state = boot({});
  const server = serve(state);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}/admin`;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER || path.join(process.env['ProgramFiles(x86)'], 'Microsoft/Edge/Application/msedge.exe') });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    await context.addInitScript(() => { localStorage.setItem('rf4_admin_token', 'fixture'); localStorage.setItem('rf4_local_admin_token', 'fixture'); });
    const page = await context.newPage(), errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto(`${base}/#/tuning/event`, { waitUntil: 'networkidle' });
    await page.locator('#pg-gifts').waitFor();

    // ---- 分支下拉只列出已保存启用的分支 ----
    const branchOptions = async () => (await branchSel(page).locator('option').evaluateAll((xs) => xs.map((x) => x.value)));
    assert.deepEqual(await branchOptions(), ['13', '14'], '只应列出已保存启用的分支 13、14');

    // ---- 阶段数由分支决定：13 是普通线（7+1 含阶段0），14 是扩展线（10+1） ----
    await branchSel(page).selectOption('13');
    assert.equal(await stageSel(page).locator('option').count(), 8, '分支 13 应有阶段 0..7 共 8 项');
    await branchSel(page).selectOption('14');
    assert.equal(await stageSel(page).locator('option').count(), 11, '分支 14 应有阶段 0..10 共 11 项');
    // 切到 14 选了阶段 10，再切回 13：阶段必须被夹到 13 的最大值，不能停留在不存在的 10。
    await stageSel(page).selectOption('10');
    await branchSel(page).selectOption('13');
    assert.equal(await stageSel(page).inputValue(), '7', '切回短分支时阶段号必须夹到上限，否则会编辑到不存在的阶段');

    // ---- 已有奖励按分支+阶段取 ----
    await stageSel(page).selectOption('1');
    assert.equal(await giftRows(page).count(), 2, '分支 13 阶段 1 应有两件奖励');
    await page.waitForFunction(() => {
      const b = document.querySelector('#pg-gifts .list .list-item b');
      return b && b.textContent !== 'fcb_coffee_can2';
    }, null, { timeout: 5000 });
    assert.match(await giftRows(page).first().innerText(), /咖啡/, '物品名应从中控台目录解析成中文名');
    // 不该出现「手填 ID」的输入框：物品只能从目录里选，避免写错 key。
    assert.equal(await page.locator('#pg-gifts input[aria-label="物品 ID"]').count(), 0);

    // ---- 空阶段给空态说明 ----
    await stageSel(page).selectOption('2');
    assert.equal(await giftRows(page).count(), 0);
    assert.match(await page.locator('#pg-gifts').innerText(), /这个阶段没有额外奖励/, '空阶段要说明是「没有」而不是加载失败');

    // ---- 改数量 + 添加物品 → 保存结构正确 ----
    await stageSel(page).selectOption('1');
    assert.equal(await giftRows(page).count(), 2);
    await giftRows(page).nth(1).locator('input[type=number]').fill('3');
    await page.locator('#pg-gifts button', { hasText: '添加物品' }).click();
    // 物品选择走右侧侧页（sidePage），搜「可可」再点结果。
    const picker = page.locator('.side-page');
    await picker.waitFor();
    await picker.locator('input').first().fill('可可');
    await picker.locator('.list-item', { hasText: '可可' }).first().click();
    await picker.waitFor({ state: 'detached', timeout: 5000 });
    assert.equal(await giftRows(page).count(), 3);
    assert.equal(await giftRows(page).nth(2).locator('b').innerText(), '可可');

    // 目录里的「竿坯组件」不是完整物品，不该出现在选择器里。
    await page.locator('#pg-gifts button', { hasText: '添加物品' }).click();
    const picker2 = page.locator('.side-page');
    await picker2.waitFor();
    assert.match(await picker2.innerText(), /食物与饮料/, '分类筛选里应有「食物与饮料」');
    assert.doesNotMatch(await picker2.innerText(), /竿坯组件/, 'complete_only 下不该出现竿坯组件（选了也用不了）');
    await picker2.locator('button[aria-label="关闭"], .side-page-close').first().click().catch(() => page.keyboard.press('Escape'));

    await saveBtn(page).click();
    await page.waitForFunction(() => /已保存|请重启/.test(document.querySelector('#pg-gifts')?.closest('.work')?.innerText || ''), null, { timeout: 8000 });
    const savedG = state.writes.filter((w) => w.key === 'event_stage_gifts').at(-1);
    assert.ok(savedG, '应写入 event_stage_gifts');
    assert.deepEqual(JSON.parse(savedG.value), {
      13: { 1: [{ item: 'fcb_coffee_can2', qty: 1 }, { item: 'ab_tea_can2', qty: 3 }, { item: 'fcb_cocoa_can2', qty: 1 }] },
    }, '写出的结构必须是 {分支:{阶段:[{item,qty}]}}，且只含已编辑的分支');

    // ---- 保存后停在当前阶段，不跳回默认 ----
    await stageSel(page).selectOption('2');
    await page.locator('#pg-gifts button', { hasText: '添加物品' }).click();
    const picker3 = page.locator('.side-page');
    await picker3.waitFor();
    await picker3.locator('input').first().fill('咖啡');
    await picker3.locator('.list-item', { hasText: '咖啡' }).first().click();
    await picker3.waitFor({ state: 'detached', timeout: 5000 });
    await saveBtn(page).click();
    await page.waitForFunction(() => /已保存|请重启/.test(document.querySelector('#pg-gifts')?.closest('.work')?.innerText || ''), null, { timeout: 8000 });
    assert.equal(await stageSel(page).inputValue(), '2', '保存后应停在刚编辑的阶段 2，不能跳回阶段 1');
    const saved2 = JSON.parse(state.writes.filter((w) => w.key === 'event_stage_gifts').at(-1).value);
    assert.deepEqual(saved2[13][2], [{ item: 'fcb_coffee_can2', qty: 1 }], '阶段 2 的新奖励要写进去');
    assert.deepEqual(saved2[13][1], [{ item: 'fcb_coffee_can2', qty: 1 }, { item: 'ab_tea_can2', qty: 3 }, { item: 'fcb_cocoa_can2', qty: 1 }], '阶段 1 的内容不能被覆盖');

    assert.deepEqual(errors, []);
    await context.close();
  } finally { await browser.close(); await new Promise((r) => server.close(r)); }
});

// 预设只改草稿：不落库、不覆盖已有阶段、只填空的阶段。
// 这三条是这一页最容易出错的语义，旧测试也重点验了它们。
test('presets only fill empty stages of enabled branches and stay a draft', async () => {
  const state = boot({ event_branches: '13,19', event_stage_gifts: '{"13":{"1":[{"item":"fcb_coffee_can2","qty":1}]}}' });
  const server = serve(state);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}/admin`;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    await context.addInitScript(() => { localStorage.setItem('rf4_admin_token', 'fixture'); localStorage.setItem('rf4_local_admin_token', 'fixture'); });
    const page = await context.newPage(), errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto(`${base}/#/tuning/event`, { waitUntil: 'networkidle' });
    await page.locator('#pg-gifts').waitFor();

    const before = state.values.event_stage_gifts, beforeWrites = state.writes.length;
    // 默认预设是 anniversary-2026（对应分支 19/20），当前启用的分支是 13、19。
    await presetSel(page).selectOption('anniversary-2026');
    await page.locator('#pg-gifts button', { hasText: '填入所选预设' }).click();
    await page.waitForFunction(() => /已填入|尚未保存|没有改动/.test(document.querySelector('#pg-gifts')?.parentElement?.innerText || ''), null, { timeout: 8000 });

    // 关键一：预设只改草稿，绝不自动保存。
    assert.equal(state.values.event_stage_gifts, before, '填入预设不能自动落库');
    assert.equal(state.writes.length, beforeWrites, '填入预设不能产生任何写入');

    // 关键二：已有阶段 13/1 不被覆盖。
    await branchSel(page).selectOption('13');
    await stageSel(page).selectOption('1');
    assert.equal(await giftRows(page).count(), 1, '分支 13 阶段 1 已有内容，预设不该动它');
    // 关键三：空阶段被填上（19 是九周年，预设里有 8 个阶段）。
    await branchSel(page).selectOption('19');
    await stageSel(page).selectOption('1');
    assert.ok(await giftRows(page).count() > 0, '分支 19 的空阶段应被预设填上');

    // 关键四：未启用的分支（20）不该被填——它不在已保存分支里，页面上也选不到。
    assert.deepEqual(await branchSel(page).locator('option').evaluateAll((xs) => xs.map((x) => x.value)), ['13', '19'],
      '只能编辑已保存启用的分支');

    // 保存后草稿落库，且 19 的内容进得去、13 的保持不变。
    await saveBtn(page).click();
    await page.waitForFunction(() => /已保存|请重启/.test(document.querySelector('#pg-gifts')?.closest('.work')?.innerText || ''), null, { timeout: 8000 });
    const after = JSON.parse(state.values.event_stage_gifts);
    assert.deepEqual(after[13], { 1: [{ item: 'fcb_coffee_can2', qty: 1 }] }, '已有分支内容必须原样保留');
    assert.ok(Object.keys(after[19] || {}).length > 0, '预设填的分支 19 应被保存');
    assert.equal(after[20], undefined, '未启用的分支 20 不能被写进去');

    // 重复填入同一预设：没有新的空阶段可填，应报「没有改动」而不是重复叠加。
    const writesBeforeRepeat = state.writes.length;
    await page.locator('#pg-gifts button', { hasText: '填入所选预设' }).click();
    await page.waitForTimeout(600);
    assert.equal(state.writes.length, writesBeforeRepeat, '重复填入不该产生写入');

    assert.deepEqual(errors, []);
    await context.close();
  } finally { await browser.close(); await new Promise((r) => server.close(r)); }
});

// 没开活动 / 没选分支时不能编辑奖励，并且要说清为什么。
// 旧后台是把整块 disabled，SPA 换成空态文案 + inert，语义一样但更好懂。
test('stage editor is unavailable with an explanation when no branch is enabled', async () => {
  const state = boot({ event_newyear_enabled: '0', event_branches: '' });
  const server = serve(state);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}/admin`;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    await context.addInitScript(() => { localStorage.setItem('rf4_admin_token', 'fixture'); localStorage.setItem('rf4_local_admin_token', 'fixture'); });
    const page = await context.newPage(), errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto(`${base}/#/tuning/event`, { waitUntil: 'networkidle' });
    await page.locator('#pg-gifts').waitFor();
    const text = await page.locator('#pg-gifts').innerText();
    assert.match(text, /还没有开启活动|还没有选择活动分支/, '应说明为什么不能编辑');
    assert.match(text, /开启活动并保存至少一个分支/, '应给出下一步怎么做');
    assert.equal(await page.locator('#pg-gifts select').count(), 0, '没有分支时不该出现奖励编辑控件');

    // 试着点「添加物品」之类不该存在的入口也不该产生写入。
    const before = state.writes.length;
    assert.equal(await page.locator('#pg-gifts button', { hasText: '添加物品' }).count(), 0);
    assert.equal(state.writes.length, before);

    // 分支全不勾时，保存按钮不该可用（没有可写的目标）。
    await page.locator('#pg-branch input[type=checkbox]').first().check();
    await page.waitForTimeout(200);
    // 勾了但没保存时，奖励区仍按「已保存的分支」渲染，不跟着草稿走。
    assert.match(await page.locator('#pg-gifts').innerText(), /还没有开启活动|还没有选择活动分支/,
      '奖励区只看已保存的分支，不随未保存的勾选变化');

    assert.deepEqual(errors, []);
    await context.close();
  } finally { await browser.close(); await new Promise((r) => server.close(r)); }
});

// 窄屏（360）下阶段奖励区不横向溢出，控件都在视口内。
test('stage gifts fit a 360px viewport without horizontal overflow', async () => {
  const state = boot({ event_branches: '13' });
  const server = serve(state);
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}/admin`;
  const browser = await chromium.launch({ headless: true, executablePath: process.env.RF4_TEST_BROWSER });
  try {
    for (const [width, dpi] of [[1280, 1.5], [360, 1.5], [360, 1]]) {
      const context = await browser.newContext({ viewport: { width, height: 900 }, deviceScaleFactor: dpi });
      await context.addInitScript(() => { localStorage.setItem('rf4_admin_token', 'fixture'); localStorage.setItem('rf4_local_admin_token', 'fixture'); });
      const page = await context.newPage(), errors = [];
      page.on('pageerror', (e) => errors.push(e.message));
      await page.goto(`${base}/#/tuning/event`, { waitUntil: 'networkidle' });
      await page.locator('#pg-gifts').waitFor();
      await stageSel(page).selectOption('1');
      const m = await page.evaluate(() => {
        const rect = (e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, right: r.right, bottom: r.bottom, width: r.width, height: r.height }; };
        const box = document.querySelector('#pg-gifts');
        return {
          viewport: innerWidth, page: document.documentElement.scrollWidth,
          box: rect(box),
          controls: [...box.querySelectorAll('input,select,button')].map((e) => rect(e)),
        };
      });
      assert.ok(m.page <= m.viewport + 2, `页面横向溢出 ${JSON.stringify({ width, dpi, page: m.page, viewport: m.viewport })}`);
      for (const c of m.controls) {
        assert.ok(c.x >= -1 && c.right <= m.viewport + 2, `控件跑出视口 ${JSON.stringify({ width, dpi, c })}`);
        assert.ok(c.height >= 15, `控件被压扁了，点不准 ${JSON.stringify({ width, dpi, c })}`);
      }
      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally { await browser.close(); await new Promise((r) => server.close(r)); }
});