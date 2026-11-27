// 视觉验收：用真实形状的 fixture 渲染主要页面，输出明暗两套主题截图。
//
// 用途是**人工核对**，不是自动断言 —— 它只负责把图生出来，顺带报一行横向溢出。
// 自动断言在各自的 *_ui_test.cjs 里；这个脚本抓的是断言写不出来的东西：
// 行高跳变、控件错位、空态只剩四个字、某块永久停在「读取中」。批次 6 里
// 表单行高不齐、天气页六区块永久加载、季节名兜底缺失这三个真实缺陷都是它先看出来的。
//
// 跑法：
//   export RF4_TEST_BROWSER="C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"
//   node internal/admin/visual_shots.cjs        # 产物进 internal/admin/_shots/（已 gitignore）
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;
const OUT = path.join(__dirname, '_shots');
fs.mkdirSync(OUT, { recursive: true });

const F = {
  '/api/me': { name: 'admin', role: 'admin' },
  '/api/dashboard': { sessions: 3, players: 42, items: 861, mails: 12, audits: 240, tunables: 168, pg: 'up', build: 'v1.9' },
  '/api/players': [
    { uid: 1000001, name: '在线甲', level: 28, xp: 4200, silver: 128400, gold: 12, bank: 0, banned: false, online: true, online_known: true, updated_ts: 1764200000 },
    { uid: 1000002, name: '离线乙', level: 14, xp: 900, silver: 5200, gold: 0, bank: 0, banned: false, online: false, online_known: false, updated_ts: 1764100000 },
  ],
  '/api/players/detail': {
    uid: 1000001, name: '在线甲', level: 28, xp: 4200, silver: 128400, gold: 12, bank: 0, banned: false,
    membership: 1, membership_active: true, stars: 3, role: 'player', created_ts: 1760000000,
    item_count: 46, keepnet_count: 12, mail_count: 3,
    items: [
      { item_id: 'rod_1', name: '碳素手竿', guid: 'g-aaaa-1111', qty: 1, container: 'equip', durability: 0.87 },
      { item_id: 'bait_worm', name: '红虫', guid: 'g-bbbb-2222', qty: 120, container: 'bag' },
    ],
    keepnet: [{ echo_guid: 'k-1', species: 'rudd', name_zh: '拟鲤', weight_g: 820, cat: 3, keep_level: 'level_001', keep_bait: '红虫', ts: 1764190000 }],
    mails: [{ id: 11, text: '欢迎回来', kind: 0, read: true, created_ts: 1764100000 }],
    skills: [{ skill_id: 1, name: '浮子钓鱼', level: 12, progress: 0.4 }, { skill_id: 2, name: '纺车钓鱼', level: 6, progress: 0.2 }],
    skill_total: 40, skill_spent: 32, skill_bonus: 0, available_points: 8,
    happy_hour: { enabled: true, qualified: true, unlimited: false, remaining_seconds: 1800 },
    fish_records: [],
  },
  '/api/audit': [
    { ts: 1764200000, action: 'tunable', target: 'bite_delay_max', admin: 'admin', detail: '3.5 → 2.8' },
    { ts: 1764199000, action: 'grant_item', target: '1000001', admin: 'admin', detail: '红虫 × 120' },
  ],
  '/api/stores': [{ id: 'store_1', name: '铜湖渔具店', item_count: 48 }],
  '/api/shop/items': [
    { item_id: 'rod_1', name: '碳素手竿', base_silver: 12000, base_gold: 0, base_stock: 5, has_override: true, override_silver: 9999 },
    { item_id: 'bait_worm', name: '红虫', base_silver: 30, base_gold: 0, base_stock: 999, has_override: false },
  ],
  '/api/hooks/profiles': {
    profiles: [
      { item_id: 'triple_20686', name: '三本钩', size: 25, display_size: 'S25', family: 'treble', brand: 'ultra', strength_kg: 150, entry_capacity_g: 24000, length_mm: 18, solid_scale: 1, hold_scale: 1, small_fish_floor: 0, over_capacity_hold_floor: 0 },
      { item_id: 'clas_20042', name: '经典钩', size: 2, display_size: 'S2', family: 'classic', brand: 'syb', strength_kg: 3, entry_capacity_g: 480, length_mm: 2, solid_scale: 1.3, hold_scale: 0.9, small_fish_floor: 0.2, over_capacity_hold_floor: 0.1 },
    ],
    total: 2,
    sizes: [{ size: 2, display_size: 'S2', count: 1 }, { size: 25, display_size: 'S25', count: 1 }],
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
      { name_zh: '拟鲤', fish_key: 'rudd', tier3: 1, has_rule: true, source: 'override', enabled: true, locs: ['铜湖'], allow: ['红虫', '玉米'], deny: [], bottom: [] },
      { name_zh: '狗鱼', fish_key: 'pike', tier3: 2, has_rule: true, source: 'rule', enabled: true, locs: ['铜湖'], allow: ['亮片'], deny: ['红虫'], bottom: [] },
    ],
  },
  '/api/bait/categories': { categories: [{ term: '红虫', label: '红虫' }, { term: '玉米', label: '玉米' }] },
  '/api/weather/maps': { maps: [{ key: 'level_001', map_name: '铜湖' }, { key: 'level_020', map_name: '挪威海' }] },
  // 真实接口把单图结果放在 preview 里，外层还有 now_day / map_name / season_name。
  '/api/weather/preview': {
    now_day: 412, map_name: '铜湖', season_name: '春',
    preview: {
    map: 'level_001', now_day: 412, air_min_c: 4, air_max_c: 15, water_min_c: 2, water_max_c: 18,
    map_name: '铜湖', season_name: '春', game_clock: '14:20',
    days: Array.from({ length: 7 }, (_, i) => ({ day: 412 + i, high_c: 14 - i * 0.4, low_c: 5 - i * 0.2, water_low_c: 6, water_high_c: 11, rain_hours: i % 3, thunder_hours: 0 })),
    hours: Array.from({ length: 7 * 24 }, (_, i) => ({
      day: 412 + Math.floor(i / 24), hour: i % 24, is_now: i === 5,
      air_temp_c: 6 + Math.sin(i / 5) * 4, surface_c: 8 + Math.sin(i / 6) * 2,
      water_c: 9 + Math.sin(i / 6) * 1.5, deep_c: 7 + Math.sin(i / 6) * 0.8,
      rain_mm: 0, thunder: 0,
    })),
    bands: [{ from_c: 2, to_c: 4, count: 24, share: 0.14 }, { from_c: 4, to_c: 6, count: 60, share: 0.36 }, { from_c: 6, to_c: 8, count: 84, share: 0.5 }],
    seasons: [
      { season: '春', water_min_c: 6, water_max_c: 14, is_now: true },
      { season: '夏', water_min_c: 14, water_max_c: 22, is_now: false },
      { season: '秋', water_min_c: 8, water_max_c: 16, is_now: false },
      { season: '冬', water_min_c: 1, water_max_c: 6, is_now: false },
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
  '/api/fish/weights/preset': {
    weights: [100, 30, 5], active: true, rows: 480,
    tiers: [{ tier: 0, species: 180, rows: 320, avg_base: 96.4 }, { tier: 1, species: 60, rows: 120, avg_base: 28.1 }, { tier: 2, species: 8, rows: 40, avg_base: 5.2 }],
  },
  '/api/fish/ecology-species': {
    total: 2,
    items: [{ fish_key: 'rudd', name_zh: '拟鲤', overlaid: true, base: { temp_min_c: 8, temp_max_c: 18, temp_pref_c: 13, diel_mask: 3, enabled: 1, layer_min: 0.1, layer_pref: 0.4, layer_max: 0.7 }, override: {} }],
  },
  '/api/econ/preview': { ok: true },
  // 真实接口是扁平 map[string]string（store.TunableList 就是 SELECT key,value 拼的对象），
  // 不是 {items:[{key,value,def,group}]}。写成后者会让参数页每个输入框都显示
  // "[object Object]"，并且因为「原值 ≠ 当前值」被未保存守卫拦住导航。
  '/api/tunables': { bite_delay_max: '2.8', bite_delay_scale: '1', bite_probability: '1' },
};

function fixture(pathname) {
  for (const [k, v] of Object.entries(F)) if (pathname.endsWith(k)) return v;
  if (pathname.includes('/api/')) return {};
  return null;
}

const SHOTS = [
  ['dashboard', 'dashboard'], ['players/1000001', 'players'], ['shop', 'shop'],
  ['rules/switches', 'rules-switches'], ['rules/grade', 'rules-grade'],
  ['ecology/eco', 'ecology-eco'], ['ecology/econ', 'ecology-econ'], ['ecology/weight', 'ecology-weight'],
  ['waters/铜湖', 'waters'], ['hooks', 'hooks'], ['bait', 'bait'],
  ['weather', 'weather'], ['mail', 'mail'], ['audit', 'audit'],
];

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  for (const theme of ['light', 'dark']) {
    for (const [route, name] of SHOTS) {
      const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
      await page.route('**/*', (r) => {
        const u = new URL(r.request().url());
        if (fx.serveAsset(r, u)) return;
        const d = fixture(u.pathname);
        if (d === null) return r.fulfill({ status: 404, body: '' });
        return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(d) });
      });
      await page.addInitScript((t) => {
        localStorage.setItem('rf4_admin_token', 'fixture');
        localStorage.setItem('rf4.theme', t);
      }, theme);
      await page.goto('http://127.0.0.1:1/#/' + route, { waitUntil: 'networkidle', timeout: 30000 });
      await page.waitForTimeout(1500);
      const file = path.join(OUT, `${theme}-${name}.png`);
      await page.screenshot({ path: file, fullPage: true });
      // 顺带记录横向溢出：这是「长标识撑破页面」这类缺陷最直接的信号。
      const m = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, vw: innerWidth }));
      console.log(`${m.sw > m.vw + 2 ? '!! 溢出' : 'ok     '} ${theme.padEnd(5)} ${name.padEnd(16)} ${m.sw}x${m.vw}`);
      await page.close();
    }
  }
  await browser.close();
  console.log('\n截图目录: ' + OUT);
})();