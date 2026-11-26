// 批次 2 守门测试：玩法规则（rules）+ 游戏参数（tuning）两页。
//
// 重点不是「页面能不能打开」，而是**键位表是否与本服后端对齐**：
// 上游 SPA 抄了一份上游的 tunables 清单，其中一批键本服后端根本不读
// （interval 式等待区间、饵料硬门、鱼获档位倍数、海钓水层状态机），
// 而本服独有的一批键上游没有（鱼猎四阶段、两档制以外的转轮、昼夜、追踪）。
// 这些错位在界面上完全看不出来——调了没反应，或者根本找不到入口。
// 所以这里做两层断言：静态读源码查键位，浏览器里查是否真的渲染出来。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;
const webRoot = path.join(__dirname, 'web');
const spaHTML = fs.readFileSync(path.join(webRoot, 'spa.html'), 'utf8');
const PAGES = path.join(webRoot, 'app/pages');
const readPage = (f) => fs.readFileSync(path.join(PAGES, f), 'utf8');

// 本服后端确实不读的键（上游 SPA 有、我们删掉的）。逐个都在 Go 里核过：
//   pace_good_min/max、pace_bad_min/max、pace_mid_min/max —— bite_pace.go 走
//     「采样 × Factor 乘数 × clamp(6, pace_bottom_max)」，没有区间取随机的分支
//   bite_gate_diet_min / bite_gate_diet_floor / bite_gate_lure_diet_min ——
//     fish_bite_gates.go 注释明确写了 G1 与 G-diet 已删除，只剩水层地板
//   skill_grade_mult_1..4 —— skills_ops.go 增量固定 0.1/0.2，无档位倍数
//   marine_layer_enabled —— 本服没有 marine_layer.go，rigWaterLayer 只按钓组族分口径
const DEAD_KEYS = [
  'pace_good_min', 'pace_good_max', 'pace_bad_min', 'pace_bad_max',
  'pace_mid_min', 'pace_mid_max',
  'bite_gate_diet_min', 'bite_gate_diet_floor', 'bite_gate_lure_diet_min',
  'skill_grade_mult_1', 'skill_grade_mult_2', 'skill_grade_mult_3', 'skill_grade_mult_4',
  'marine_layer_enabled',
];

// 本服独有、必须在界面上找得到的键（上游没有）。
const MUST_HAVE_KEYS = [
  'pace_bottom_max',              // 底层杆等待上限，取代上游那套区间
  'bait_rotation_auto_sec',       // 自动轮换周期
  'bait_evidence_low_scale',      // 证据不足饵降权
  'fish_roll_trace_keep',         // 判鱼追踪保留条数
  'fish_diel_weight_enabled',     // 昼夜权重总开关
  'fish_diel_inactive_block',     // 非活跃时段禁口
  'skill_gain_pct_7',             // 配置饵料（上游清单漏了这一档）
  'hunt_stage_daily_below',       // 鱼猎四阶段
  'hunt_stage_gather_trophy',
  'hunt_stage_active_super',
  'hunt_stage_giant_qualified',
  'two_stage_daily_below',        // 两档制
  'two_stage_baited_super',
];

test('tuning.js 不再列出本服后端不读的参数键', () => {
  const src = readPage('tuning.js');
  const hits = DEAD_KEYS.filter((k) => src.includes(`'${k}'`));
  assert.deepEqual(hits, [], '这些键后端不读，列在界面上等于让人调了个没反应的开关: ' + hits.join(', '));
});

test('rules.js 不再列出本服不读的开关键', () => {
  const src = readPage('rules.js');
  assert.ok(!src.includes("'marine_layer_enabled'"), 'rules.js 不该再有 marine_layer_enabled（本服没有海钓水层状态机）');
});

test('本服独有的参数键都能在页面源码里找到', () => {
  const src = readPage('tuning.js') + readPage('rules.js');
  // 这两族都不是逐档位写死的：
  //   hunt_stage_ 由 `hunt_stage_${stage}_` 模板拼出（stage 由 HUNT_STAGES 给）；
  //   two_stage_  的完整键是 set.prefix + 档位名拼出来的，源码里只有前缀字面量。
  const KEYSET_PREFIXES = ['two_stage_daily_', 'two_stage_baited_'];
  const missing = MUST_HAVE_KEYS.filter((k) => {
    if (src.includes(k)) return false;
    if (k.startsWith('hunt_stage_')) return !src.includes('hunt_stage_${stage}_');
    return !KEYSET_PREFIXES.some((p) => k.startsWith(p) && src.includes(p));
  });
  assert.deepEqual(missing, [], '以下键在本服后端生效、但界面上找不到入口: ' + missing.join(', '));
});

test('tuning.js 里每个参数键，后端源码里都真的有引用', () => {
  // 反向检查：从 JS 里抽 [key, 标签, ...] 形态的行，逐个到 Go 源码里确认存在。
  const src = readPage('tuning.js');
  const keys = [...src.matchAll(/^\s*\['([a-z][a-z0-9_]+)',/gm)].map((m) => m[1]);
  assert.ok(keys.length > 60, '参数键数量异常偏少: ' + keys.length);
  const goSrc = walkGo(path.join(__dirname, '..', '..'));
  const orphans = keys.filter((k) => !goSrc.includes(`"${k}"`));
  assert.deepEqual(orphans, [], '这些键页面能改、但后端一个地方都没读: ' + orphans.join(', '));
});

test('鱼猎四阶段：四张表共 16 个键，默认值与后端 fourStageWeightKeys 一致', () => {
  const src = readPage('rules.js');
  // 后端默认值（internal/gameserver/fish_hunt.go fourStageWeightKeys）
  const expected = {
    daily: [74.7, 24, 1, 0.3],
    gather: [54.5, 43, 2, 0.5],
    active: [30, 61.2, 6.8, 2],
    giant: [10, 64, 20, 6],
  };
  // 键名在渲染时由 `hunt_stage_${stage}_` 前缀拼出，这里断言 stage 与默认值都登记了。
  for (const [stage, defs] of Object.entries(expected)) {
    assert.ok(new RegExp(`\\[\\s*'${stage}'`).test(src), `HUNT_STAGES 里缺阶段 ${stage}`);
    assert.ok(src.includes(defs.join(', ')) || src.includes(defs.join(',')),
      `阶段 ${stage} 的默认值 ${JSON.stringify(defs)} 没在 rules.js 里出现`);
  }
  assert.ok(src.includes('hunt_stage_${stage}_'), '渲染时没有拼出 hunt_stage_<stage>_ 前缀，四阶段表不会生效');
});

test('规则页：六项开关 + 「其他」里的轮换/追踪开关都在浏览器里渲染出来', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'rules/switches');
    const text = await page.locator('#view').innerText();
    for (const label of ['饵要对口', '鱼种习性', '天气与水温', '鱼群分布', '鱼会挑饵', '欢乐时光']) {
      assert.ok(text.includes(label), `玩法开关缺「${label}」`);
    }
    assert.ok(!text.includes('海钓水层影响'), '不该出现本服没有的「海钓水层影响」开关');
    assert.ok(text.includes('六项规则'), '标题应写「六项规则」，实得: ' + text.slice(0, 400));
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();

    const other = await openPage(browser, 'rules/other');
    const otext = await other.page.locator('#view').innerText();
    assert.ok(otext.includes('自动轮换开口饵'), '「其他」里缺自动轮换开关');
    assert.ok(otext.includes('记录判鱼追踪'), '「其他」里缺判鱼追踪开关');
    assert.deepEqual(other.errors, [], '页面有 JS 错误: ' + other.errors.join('; '));
    await other.page.close();
  } finally { await browser.close(); }
});

test('参数页：鱼猎四阶段的四张权重表在选「鱼猎四阶段」后渲染出来', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    // fish_hunt_enabled=1，其余两个开关关闭 → 页面判定当前为鱼猎四阶段。
    const { page, errors } = await openPage(browser, 'rules/grade', null, null, {
      fish_hunt_enabled: '1', grade_ratio_enabled: '0', two_stage_hunt_enabled: '0',
    });
    const text = await page.locator('#view').innerText();
    for (const stage of ['鱼猎 · 日常', '鱼猎 · 聚鱼', '鱼猎 · 发窝', '鱼猎 · 巨物发窝']) {
      assert.ok(text.includes(stage), `缺阶段表「${stage}」`);
    }
    // 四张表各 4 档 = 16 个数字输入框 + 选卡片的按钮
    const nums = await page.locator('#view input[type="number"]').count();
    assert.ok(nums >= 16, `四阶段应有至少 16 个权重输入框，实得 ${nums}`);
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('参数页：昼夜权重与自动轮换的数值项在浏览器里渲染出来', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    const { page, errors } = await openPage(browser, 'tuning/gameplay');
    const text = await page.locator('#view').innerText();
    assert.ok(text.includes('昼夜权重'), '缺「昼夜权重」分组');
    assert.ok(text.includes('按昼夜压权重'), '缺「按昼夜压权重」开关');
    assert.ok(text.includes('非活跃时段禁口'), '缺「非活跃时段禁口」开关');
    assert.ok(!text.includes('好饵等待下限'), '不该再出现本服没有的「好饵等待下限」');
    assert.ok(!text.includes('天然饵门槛'), '不该再出现本服已删除的「天然饵门槛」');
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

test('参数页：技能概率只列 9 个大类，不再有「按鱼获档位加成」', async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  try {
    // 逐项数值只在「自定义」模式下渲染（原版/百分百两种模式没有可填的项），
    // 所以要把模式切成 custom 才看得到 skill_gain_pct_* 九行。
    const { page, errors } = await openPage(browser, 'tuning/reward', null, null, {
      skill_gain_mode: 'custom', skill_gain_pct_7: '0',
    });
    const text = await page.locator('#view').innerText();
    assert.ok(text.includes('技能升级概率'), '缺技能升级概率区块');
    assert.ok(!text.includes('按鱼获档位加成'), '不该再有档位倍数（本服无此机制）');
    assert.ok(text.includes('配置饵料'), '技能大类里缺「配置饵料」(skill_gain_pct_7)');
    assert.ok(text.includes('未定名大类'), '技能大类里缺 801-815 那组（skill_gain_pct_5）');
    // 九个大类：浮子、纺车、底钓、海洋、饵料制作、烹饪、配置饵料、未定名、制作拟饵。
    // 注意 Form 生成的数值框是 type=text + inputmode=decimal（为了统一校验与单位后缀），
    // 不是 type=number，所以按 id 前缀 f_<key>_ 数。
    const pct = await page.locator('#view input[id^="f_skill_gain_pct_"]').count();
    assert.equal(pct, 9, `自定义模式应有 9 个技能概率输入框，实得 ${pct}`);
    assert.deepEqual(errors, [], '页面有 JS 错误: ' + errors.join('; '));
    await page.close();
  } finally { await browser.close(); }
});

// ---------- 工具 ----------

function walkGo(root, out = []) {
  for (const e of fs.readdirSync(root, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name === '.git' || e.name.startsWith('.')) continue;
    const p = path.join(root, e.name);
    if (e.isDirectory()) walkGo(p, out);
    else if (e.name.endsWith('.go') && !e.name.endsWith('_test.go')) {
      out.push(fs.readFileSync(p, 'utf8'));
    }
  }
  return out.join('\n');
}

// 与批次 1 同构的接口替身。tunables 可覆盖，用来把页面切到指定出鱼方式。
async function openPage(browser, route, extraPrepare, writeLog, tunOverrides) {
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e.message || e)));
  await page.route('**/*', async (r) => {
    const u = new URL(r.request().url());
    if (u.pathname.includes('/api/')) {
      if (writeLog && r.request().method() !== 'GET') {
        let payload = null;
        try { payload = r.request().postDataJSON(); } catch (e) { payload = r.request().postData(); }
        writeLog.push({ path: u.pathname, payload });
      }
      return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(fixtures(u.pathname, tunOverrides)) });
    }
    if (u.pathname === '/' || u.pathname.endsWith('/spa.html')) {
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
  if (extraPrepare) await extraPrepare(page);
  await page.goto('http://127.0.0.1:1/spa.html#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
  await page.waitForTimeout(900);
  return { page, errors };
}

function fixtures(pathname, tunOverrides) {
  if (pathname.endsWith('/api/me')) return { name: 'admin' };
  if (pathname.endsWith('/api/tunables')) return Object.assign({}, tunOverrides || {});
  if (pathname.endsWith('/api/fish/bite_mode')) return { mode: 'mixed' };
  if (pathname.endsWith('/api/fish/spawn_mode')) return { mode: 'grade_ratio' };
  return [];
}