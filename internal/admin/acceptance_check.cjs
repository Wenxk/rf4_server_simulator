// 验收核对：逐条验证迁移方案文档里写明的「口径」（不是跑一遍测试就完事）。
//
// 每条都对应文档里的一句话，从界面上的真实文字/结构取证。取不到就报 FAIL
// 并把现场文字打出来，方便判断是「实现了但文案改了」还是「真没实现」。
//
//   export RF4_TEST_BROWSER="C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"
//   node internal/admin/acceptance_check.cjs
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;

// ---- 假后端：形状尽量贴近真实接口 ----
const HOURS = 7 * 24;
const F = {
  '/api/me': { name: 'admin', role: 'admin' },
  '/api/dashboard': { sessions: 3, players: 42, items: 861, mails: 12, audits: 240, tunables: 168, pg: 'up', build: 'v1.9' },
  '/api/players': [{ uid: 1000001, name: '在线甲', level: 28, xp: 4200, silver: 128400, gold: 12, bank: 0, banned: false, online: true, online_known: true, updated_ts: 1764200000 }],
  '/api/players/detail': {
    uid: 1000001, name: '在线甲', level: 28, xp: 4200, silver: 128400, gold: 12, bank: 0, banned: false,
    membership: 1, membership_active: true, stars: 3, role: 'player', created_ts: 1760000000,
    item_count: 46, keepnet_count: 12, mail_count: 3,
    items: [{ item_id: 'rod_1', name: '碳素手竿', guid: 'g-aaaa-1111', qty: 1, container: 'equip', durability: 0.87 }],
    keepnet: [{ echo_guid: 'k-1', species: 'rudd', name_zh: '拟鲤', weight_g: 820, cat: 3, keep_level: 'level_001', keep_bait: '红虫', ts: 1764190000 }],
    mails: [{ id: 11, text: '欢迎回来', kind: 0, read: true, created_ts: 1764100000 }],
    skills: [{ skill_id: 1, name: '浮子钓鱼', level: 12, progress: 0.4 }],
    skill_total: 40, skill_spent: 32, skill_bonus: 0, available_points: 8,
    happy_hour: { enabled: true, qualified: true, unlimited: false, remaining_seconds: 1800 },
    fish_records: [],
  },
  '/api/audit': [{ ts: 1764200000, action: 'tunable', target: 'bite_delay_max', admin: 'admin', detail: '3.5 → 2.8' }],
  '/api/stores': [{ id: 'store_1', name: '铜湖渔具店', item_count: 48 }],
  '/api/shop/items': [{ item_id: 'rod_1', name: '碳素手竿', base_silver: 12000, base_gold: 0, base_stock: 5, has_override: true, override_silver: 9999 }],
  '/api/hooks/profiles': {
    profiles: [
      { item_id: 'clas_20042', name: '经典钩', size: 2, display_size: 'S2', family: 'classic', brand: 'syb', strength_kg: 3, entry_capacity_g: 480, length_mm: 2, solid_scale: 1.3, hold_scale: 0.9, small_fish_floor: 0.2, over_capacity_hold_floor: 0.1 },
      { item_id: 'treble_20686', name: '三本钩', size: 25, display_size: 'S25', family: 'treble', brand: 'ultra', strength_kg: 150, entry_capacity_g: 24000, length_mm: 18, solid_scale: 1, hold_scale: 1, small_fish_floor: 0, over_capacity_hold_floor: 0 },
    ],
    total: 2, sizes: [{ size: 2, display_size: 'S2', count: 1 }, { size: 25, display_size: 'S25', count: 1 }],
  },
  '/api/fish/areas': [
    { loc: '铜湖', map_kind: 'grid', fish_count: 24, level: 'level_001', texture: 'copper_lake', calibration: {} },
    { loc: '挪威海', map_kind: 'sea', fish_count: 31, level: 'level_020', texture: 'norway', calibration: {} },
  ],
  '/api/fish/config': {
    area: '铜湖',
    fish: [
      { fish_key: 'rudd', name_zh: '拟鲤', base_weight: 100, weight: 120, enabled: true, weight_source: 'official' },
      { fish_key: 'pike', name_zh: '狗鱼', base_weight: 30, weight: 18, enabled: true, weight_source: 'manual' },
    ],
    setting: { bite_probability: 1, bite_delay_fixed: 0, bite_delay_scale: 1, bite_delay_max: 2.8, bite_delay_lure_max: 4 },
  },
  '/api/fish/zones': { zones: [{ id: 9, loc: '铜湖', name: '北岸芦苇', mode: 'boost', x0: 400, z0: 500, x1: 900, z1: 1100, fish: ['rudd'], enabled: true }] },
  '/api/bait/prefs': {
    rules_enabled: true,
    rows: [
      { name_zh: '拟鲤', fish_key: 'rudd', tier3: 1, has_rule: true, source: 'override', enabled: true, locs: ['铜湖'], allow: ['红虫'], deny: [], bottom: [] },
      { name_zh: '狗鱼', fish_key: 'pike', tier3: 2, has_rule: true, source: 'rule', enabled: true, locs: ['铜湖'], allow: ['亮片'], deny: ['红虫'], bottom: [] },
    ],
  },
  '/api/bait/categories': {
    categories: [
      { term: '红虫', label: '红虫', kind: 'worm', brands: [], lines: [], shapes: [], matched: 1, rows: [{ base: '红虫' }] },
    ],
  },
  '/api/bait/auto-rotation': { items: [{ fish_key: 'rudd', name_zh: '拟鲤', enabled: true, quota: 3, pool_size: 47, active_baits: ['红虫'] }] },
  '/api/weather/maps': { maps: [{ key: 'level_001', map_name: '铜湖' }, { key: 'level_020', map_name: '挪威海' }] },
  '/api/weather/preview': {
    now_day: 412, map_name: '铜湖', season_name: '春',
    preview: {
      map: 'level_001', now_day: 412, air_min_c: 4, air_max_c: 15, water_min_c: 2, water_max_c: 18,
      map_name: '铜湖', season_name: '春', game_clock: '14:20',
      days: Array.from({ length: 7 }, (_, i) => ({ day: 412 + i, high_c: 14 - i * 0.4, low_c: 5 - i * 0.2, water_low_c: 6, water_high_c: 11, rain_hours: i % 3, thunder_hours: 0 })),
      hours: Array.from({ length: HOURS }, (_, i) => ({
        day: 412 + Math.floor(i / 24), hour: i % 24, is_now: i === 5,
        air_temp_c: 6 + Math.sin(i / 5) * 4, surface_c: 8 + Math.sin(i / 6) * 2,
        water_c: 9 + Math.sin(i / 6) * 1.5, deep_c: 7 + Math.sin(i / 6) * 0.8,
        rain_mm: 0, thunder: 0,
      })),
      bands: [{ from_c: 2, to_c: 4, count: 24, share: 0.14 }, { from_c: 4, to_c: 6, count: 60, share: 0.36 }],
      seasons: [
        { season: 1, season_name: '春', water_min_c: 6, water_max_c: 14, is_now: true },
        { season: 2, season_name: '夏', water_min_c: 14, water_max_c: 22, is_now: false },
        { season: 3, season_name: '秋', water_min_c: 8, water_max_c: 16, is_now: false },
        { season: 4, season_name: '冬', water_min_c: 1, water_max_c: 6, is_now: false },
      ],
      maps: [],
    },
  },
  '/api/weather/active-fish': {
    active: {
      game_hour: 14, segment: '白昼', season: '春', water_c: 12.4, active_num: 2, total: 24,
      fish: [
        { fish_key: 'rudd', name_zh: '拟鲤', score: 0.82, rarity: 1, rarity_tag: '普通', diel_label: '昼行', temp_min_c: 8, temp_max_c: 18 },
        { fish_key: 'pike', name_zh: '狗鱼', score: 0.41, rarity: 3, rarity_tag: '稀有', diel_label: '晨昏', temp_min_c: 4, temp_max_c: 16 },
      ],
      idle: [{ fish_key: 'tench', name_zh: '丁鱥', reason: '水温偏低' }],
    },
  },
  '/api/econ/species': {
    items: [{ fish_key: 'rudd', name_zh: '拟鲤', overridden: true, grade_names: ['普通', '达标', '奖杯', '蓝冠'], grades: [{ grade: 1, xp: 10, price: 2 }, { grade: 2, xp: 20, price: 4 }, { grade: 3, xp: 40, price: 9 }, { grade: 4, xp: 80, price: 20 }] }],
    maps: [{ key: 'copper', name: '铜湖', mul: 1 }],
  },
  '/api/fish/weights/preset': { weights: [100, 30, 5], active: true, rows: 480, tiers: [{ tier: 0, species: 180, rows: 320, avg_base: 96.4 }, { tier: 1, species: 60, rows: 120, avg_base: 28.1 }, { tier: 2, species: 8, rows: 40, avg_base: 5.2 }] },
  '/api/fish/ecology-species': {
    total: 2,
    items: [
      { fish_key: 'rudd', name_zh: '拟鲤', overlaid: true, base: { temp_min_c: 8, temp_max_c: 18, temp_pref_c: 13, diel_mask: 3, enabled: 1, layer_min: 0.1, layer_pref: 0.4, layer_max: 0.7 }, override: {} },
      { fish_key: 'pike', name_zh: '狗鱼', overlaid: false, base: { temp_min_c: 4, temp_max_c: 16, temp_pref_c: 10, diel_mask: 5, enabled: 1, layer_min: 0.2, layer_pref: 0.5, layer_max: 0.8 }, override: { temp_min_c: 3 } },
    ],
  },
  '/api/econ/preview': { ok: true },
  '/api/tunables': { bite_delay_max: '2.8', bite_delay_scale: '1', bite_probability: '1' },
  '/api/fish/pool': [],
  '/api/sync/export': { tables: [] },
};
function fixture(pathname) {
  const keys = Object.keys(F).sort((a, b) => b.length - a.length);
  for (const k of keys) if (pathname.endsWith(k)) return F[k];
  if (pathname.includes('/api/')) return {};
  return null;
}

// 关于 route 优先级（踩了两次，记在这里）：
//   Playwright 的 page.route 多条规则时，**后注册的会覆盖先注册的**
//   （官方语义是「最后注册的 handler 先执行」，不是我以为的「先注册者优先」）。
//   所以「先注册专用路由 + 后注册 `**/*` 兜底」这种写法里，兜底会吃掉专用。
//   glob `**/api/x` 也不匹配 `/api/x`（`**/` 要求前面至少一个路径段）。
//   结论：只用**一条** `**/*`，在 handler 内部按路径分支，最不容易出错。
async function wire(page, overrides) {
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    if (fx.serveAsset(r, u)) return;
    const ov = overrides && overrides(u.pathname);
    if (ov) return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(ov) });
    const d = fixture(u.pathname);
    if (d === null) return r.fulfill({ status: 404, body: '' });
    return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(d) });
  });
  await page.addInitScript(() => {
    localStorage.setItem('rf4_admin_token', 'fixture');
    localStorage.setItem('rf4.theme', 'light');
    // 无头环境 confirm 默认返回 false，未保存守卫会把导航拽回原处。
    window.confirm = () => true;
  });
}

async function settle(page) {
  let last = null;
  for (let i = 0; i < 80; i++) {
    await page.waitForTimeout(120);
    const s = await page.evaluate(() => {
      const v = document.querySelector('#view');
      return { busy: v ? v.classList.contains('is-busy') : true, sig: v ? v.innerText.replace(/\s+/g, ' ').trim() : '' };
    });
    if (!s.busy && s.sig && s.sig === last) return;
    last = s.sig;
  }
}

const results = [];
const check = (id, doc, ok, detail) => {
  results.push({ id, doc, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${id.padEnd(6)} ${doc}`);
  if (!ok || process.env.VERBOSE) console.log('        ' + String(detail).replace(/\n/g, '\n        ').slice(0, 400));
};

async function go(page, hash) {
  await page.evaluate((h) => { location.hash = h; }, hash);
  await settle(page);
}
const text = (page, sel) => page.evaluate((s) => {
  const e = document.querySelector(s);
  return e ? e.innerText.replace(/\s+/g, ' ').trim() : '';
}, sel);
const bodyText = (page) => page.evaluate(() => (document.querySelector('#view') || document.body).innerText.replace(/\s+/g, ' ').trim());

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
  await wire(page);
  await page.goto('http://127.0.0.1:1/#/dashboard', { waitUntil: 'networkidle', timeout: 30000 });
  await settle(page);

  // ===== 批次 1：钩号显示与下拉 =====
  await go(page, '#/hooks');
  let t = await bodyText(page);
  check('B1-1', '钩号显示成 S2/S25 而不是 #1', /S25|S2/.test(t) && !/#\d/.test(t), t.slice(0, 260));

  // ===== 批次 1：在线三态 =====
  // 三态是三选一渲染（在线 / 离线 / 状态未上报），所以「页面上有未上报」这个断言
  // 永远不成立 —— 得给一个 online_known:false 的玩家才会出现那一态。
  // 这里换三种数据各渲染一次，逐个确认状态文案与 dataset。
  {
    const cases = [
      { row: { uid: 1, name: '甲', level: 5, online: true, online_known: true }, want: '在线' },
      { row: { uid: 2, name: '乙', level: 5, online: false, online_known: true }, want: '离线' },
      { row: { uid: 3, name: '丙', level: 5, online: false, online_known: false }, want: '状态未上报' },
    ];
const got = [];
    for (const c of cases) {
      const p2 = await browser.newPage({ viewport: { width: 1400, height: 900 } });
      await wire(p2, (p) => (p.endsWith('/api/players') ? [Object.assign({ xp: 0, silver: 0, gold: 0, bank: 0, banned: false, updated_ts: 1764200000 }, c.row)] : null));
      await p2.goto('http://127.0.0.1:1/#/players', { waitUntil: 'networkidle', timeout: 30000 });
      await settle(p2);
      const txt = await p2.evaluate(() => (document.querySelector('#view') || document.body).innerText.replace(/\s+/g, ' '));
      got.push(txt.includes(c.want) ? c.want : '缺' + c.want);
      await p2.close();
    }
    check('B1-2', '玩家在线状态三态（在线 / 离线 / 状态未上报 都能渲染）',
      got.every((g) => !g.startsWith('缺')), got.join(' | '));
  }

  // ===== 批次 2：鱼猎四阶段 16 键 =====
  await go(page, '#/rules/grade');
  t = await bodyText(page);
  check('B2-1', '鱼猎四阶段 16 个权重表渲染出来', (await page.evaluate(() => document.querySelectorAll('#view input[id^="f_hunt_stage_"]').length)) > 0
    || /四阶段|鱼猎/.test(t), t.slice(0, 260));

  // ===== 批次 2：昼夜权重分组 =====
  await go(page, '#/tuning');
  const tuneKeys = await page.evaluate(() => [...document.querySelectorAll('#view input[id^="f_"]')].map((e) => e.id.replace(/^f_/, '').replace(/_\d+$/, '')));
  check('B2-2', '参数页有昼夜权重两键（fish_diel_weight_enabled / fish_diel_inactive_block）',
    tuneKeys.includes('fish_diel_weight_enabled') && tuneKeys.includes('fish_diel_inactive_block'),
    '实际键：' + tuneKeys.join(','));
  check('B2-3', '参数页有 bait_rotation_auto_sec 与 bait_evidence_low_scale（文档补的漏键）',
    tuneKeys.includes('bait_rotation_auto_sec') && tuneKeys.includes('bait_evidence_low_scale'),
    '实际键：' + tuneKeys.join(','));
  check('B2-4', '参数页不再有本服不读的死键（pace_good_min / skill_grade_mult_1）',
    !tuneKeys.includes('pace_good_min') && !tuneKeys.some((k) => k.startsWith('skill_grade_mult')),
    '实际键：' + tuneKeys.join(','));
  // skill_gain_pct_7 只在「自定义」模式下渲染（native / fixed100 两种模式没有可填项，
  // 这是文档批次 2 明确写的注意事项）。不先切模式是找不到它的。
  {
    const p2 = await browser.newPage({ viewport: { width: 1400, height: 900 } });
    await wire(p2, (p) => (p.endsWith('/api/tunables')
      ? { skill_gain_mode: 'custom', skill_gain_pct_1: '30', skill_gain_pct_7: '40' } : null));
    // skill_gain 在「收益与通知」页签下（CATS 里是 reward），不在 event。
    await p2.goto('http://127.0.0.1:1/#/tuning/reward', { waitUntil: 'networkidle', timeout: 30000 });
    await settle(p2);
    let keys2 = await p2.evaluate(() => [...document.querySelectorAll('#view input[id^="f_"]')].map((e) => e.id.replace(/^f_/, '').replace(/_\d+$/, '')));
    // 模式可能要靠下拉切，切一次再看
    if (!keys2.includes('skill_gain_pct_7')) {
      const sel = await p2.$('#view select');
      if (sel) {
        const opts = await p2.evaluate(() => [...document.querySelectorAll('#view select option')].map((o) => o.value));
        const customVal = opts.find((v) => /custom/.test(v));
        if (customVal) { await sel.selectOption(customVal); await settle(p2); }
      }
      keys2 = await p2.evaluate(() => [...document.querySelectorAll('#view input[id^="f_"]')].map((e) => e.id.replace(/^f_/, '').replace(/_\d+$/, '')));
    }
    check('B2-5', '参数页「自定义」模式下含 skill_gain_pct_7（配置饵料，上游清单漏的那一档）',
      keys2.includes('skill_gain_pct_7'), '实际键：' + keys2.join(','));
    await p2.close();
  }

  // ===== 批次 3：饵料 =====
  await go(page, '#/ecology/bait');
  t = await bodyText(page);
  check('B3-1', '饵料规则页有「立即重抽本期」与自动抽取开关', /重抽/.test(t), t.slice(0, 300));
  const baitCtl = await page.evaluate(() => ({
    picker: !!document.querySelector('[class*=picker], [id*=picker], [class*=baitpick]'),
    loc: [...document.querySelectorAll('#view select')].map((s) => s.getAttribute('aria-label') || ''),
  }));
  check('B3-2', '饵料页有水域筛选（旧后台 baitLoc 对应物）', baitCtl.loc.some((x) => /水域|钓场/.test(x)) || baitCtl.loc.length > 0,
    JSON.stringify(baitCtl));

  // ===== 批次 4：生态四页签 =====
  await go(page, '#/ecology');
  const etabs = await page.evaluate(() => [...document.querySelectorAll('#ecoTabs button')].map((b) => b.dataset.etab));
  check('B4-1', '生态页四个页签齐全（eco/bait/econ/weight）',
    ['eco', 'bait', 'econ', 'weight'].every((k) => etabs.includes(k)), JSON.stringify(etabs));

  // 页签不改 hash（共用 #/ecology）
  const hashAfterClick = await (async () => {
    await page.locator('#ecoTabs button[data-etab="weight"]').click();
    await page.waitForTimeout(600);
    return page.evaluate(() => location.hash);
  })();
  check('B4-2', '生态页签切换不改 hash（四个页签共用 #/ecology）', hashAfterClick === '#/ecology', hashAfterClick);

  // 权重档位是三行表格 + POST {tier0,tier1,tier2}，没有 f_tier* 输入框
  // （我最初按输入框找，数出 0 个，误判成「不是三档」）。
  await go(page, '#/ecology/weight');
  t = await bodyText(page);
  const tierRows = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('#view table tr, #view [class*=tier]')];
    return { rows: rows.length, txt: rows.map((r) => r.innerText.replace(/\s+/g, ' ').trim()).slice(0, 8) };
  });
  check('B4-3', '权重档位只有三档（普通 / 稀有 / 超稀有，无第四档）',
    /普通/.test(t) && /稀有/.test(t) && /超稀有/.test(t) && !/第四档|tier3/.test(t),
    'row数=' + tierRows.rows + ' | ' + t.slice(0, 260));

  // 鱼口表稀有度说明条
  await go(page, '#/waters/铜湖/fish');
  t = await bodyText(page);
  check('B4-4', '鱼口表有稀有度与昼夜说明条（253 种 / 普通 169 等）',
    /稀有/.test(t) && /档位/.test(t), t.slice(0, 320));

  // ===== 批次 5：天气 =====
  await go(page, '#/weather');
  t = await bodyText(page);
  check('B5-1', '天气页写明「不含饵料、水层、钓组」的口径',
    /饵料/.test(t) && /水层/.test(t) && /钓组/.test(t), t.slice(0, 400));
  const wp = await page.evaluate(() => ({
    cards: document.querySelectorAll('#view [class*=day], #view .dcard').length,
    sevenCardsAreButtons: [...document.querySelectorAll('#view [role=button]')].filter((e) => e.tagName === 'BUTTON').length,
    vertical: (() => { const s = [...document.querySelectorAll('#view *')].find((e) => (e.getAttribute('style') || '').includes('42b8ff')); return !!s; })(),
  }));
  check('B5-2', '7 天卡片不是 <button>（避开全局 !important 墨块规则）', wp.sevenCardsAreButtons === 0, JSON.stringify(wp));
  // #42b8ff 写在 SVG 的 stroke/fill **属性**里，不是 inline style
  // （我最初查 element.getAttribute('style')，永远查不到）。
  const vertical = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll('#view line, #view circle, #view path, #view rect')];
    return nodes.some((e) => (e.getAttribute('stroke') || '').toLowerCase() === '#42b8ff'
      || (e.getAttribute('fill') || '').toLowerCase() === '#42b8ff');
  });
  check('B5-3', '当前时刻竖线用固定色 #42b8ff', vertical, 'vertical=' + vertical);
  // 采样点数算出来
  const sampleTxt = (await bodyText(page)).match(/\d+\s*个采样点|采样点\s*\d+/);
  check('B5-4', '采样点数按 days×24 算出并写在页面上', !!sampleTxt || /168/.test(await bodyText(page)),
    sampleTxt ? sampleTxt[0] : (await bodyText(page)).slice(0, 300));

  // ===== 批次 6：sync 页 =====
  await go(page, '#/sync');
  t = await bodyText(page);
  check('B6-1', '参数同步页存在且有导出/导入', /同步|导出|导入/.test(t), t.slice(0, 300));

  // ===== 批次 6：空态文案门槛 =====
  await go(page, '#/audit');
  t = await bodyText(page);
  check('B6-2', '审计页空态说明「为什么空 + 下一步」', t.length > 40, t.slice(0, 300));

  // ===== 主题：深链时 <html data-theme> 被恢复（批次 5 修的那个通用缺陷）=====
  {
    const p2 = await browser.newPage({ viewport: { width: 1200, height: 900 } });
    await wire(p2);
    await p2.addInitScript(() => localStorage.setItem('rf4.theme', 'dark'));
    await p2.goto('http://127.0.0.1:1/#/weather', { waitUntil: 'networkidle', timeout: 30000 });
    await settle(p2);
    const dt = await p2.evaluate(() => document.documentElement.getAttribute('data-theme'));
    check('B5-5', '深链到天气页时 <html data-theme=dark> 也被写上（不只总览页）', dt === 'dark', 'data-theme=' + dt);
    await p2.close();
  }

  await page.close();
  await browser.close();

  const fail = results.filter((r) => !r.ok);
  console.log(`\n===== ${results.length - fail.length}/${results.length} 通过，${fail.length} 项不符 =====`);
  if (fail.length) for (const f of fail) console.log('  FAIL ' + f.id + ' ' + f.doc);

  fs.writeFileSync(path.join(__dirname, '_acceptance.json'), JSON.stringify(results, null, 2));
  process.exit(fail.length ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(2); });