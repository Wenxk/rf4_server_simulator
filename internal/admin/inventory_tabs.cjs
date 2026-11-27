// 逐页签盘点：把 SPA 里藏在二级页签后面的控件也数进来。
// inventory_check.cjs 只抓默认页签，生态页 4 个页签只有 1 个会被数到，
// 会让「旧后台有的 SPA 没有」这类误判凭空出现。
//
//   export RF4_TEST_BROWSER="C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"
//   node internal/admin/inventory_tabs.cjs
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;

// 与 visualize/inventory 共用的最小 fixture，够页面渲染出全部控件即可。
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
    profiles: [{ item_id: 'clas_20042', name: '经典钩', size: 2, display_size: 'S2', family: 'classic', brand: 'syb', strength_kg: 3, entry_capacity_g: 480, length_mm: 2, solid_scale: 1.3, hold_scale: 0.9, small_fish_floor: 0.2, over_capacity_hold_floor: 0.1 }],
    total: 1, sizes: [{ size: 2, display_size: 'S2', count: 1 }],
  },
  '/api/fish/areas': [{ loc: '铜湖', map_kind: 'grid', fish_count: 24, level: 'level_001', texture: 'copper_lake', calibration: {} }],
  '/api/fish/config': {
    area: '铜湖',
    fish: [{ fish_key: 'rudd', name_zh: '拟鲤', base_weight: 100, weight: 120, enabled: true, weight_source: 'official' }],
    setting: { bite_probability: 1, bite_delay_fixed: 0, bite_delay_scale: 1, bite_delay_max: 2.8, bite_delay_lure_max: 4 },
  },
  '/api/fish/zones': { zones: [{ id: 9, loc: '铜湖', name: '北岸芦苇', mode: 'boost', x0: 400, z0: 500, x1: 900, z1: 1100, fish: ['rudd'], enabled: true }] },
  '/api/bait/prefs': {
    rules_enabled: true,
    rows: [{ name_zh: '拟鲤', fish_key: 'rudd', tier3: 1, has_rule: true, source: 'override', enabled: true, locs: ['铜湖'], allow: ['红虫'], deny: [], bottom: [] }],
  },
  '/api/bait/categories': { categories: [{ term: '红虫', label: '红虫' }] },
  '/api/bait/auto-rotation': { items: [] },
  '/api/weather/maps': { maps: [{ key: 'level_001', map_name: '铜湖' }] },
  '/api/weather/preview': {
    now_day: 412, map_name: '铜湖', season_name: '春',
    preview: {
      map: 'level_001', now_day: 412, air_min_c: 4, air_max_c: 15, water_min_c: 2, water_max_c: 18,
      map_name: '铜湖', season_name: '春', game_clock: '14:20',
      days: Array.from({ length: 7 }, (_, i) => ({ day: 412 + i, high_c: 14, low_c: 5, water_low_c: 6, water_high_c: 11, rain_hours: 0, thunder_hours: 0 })),
      hours: Array.from({ length: 7 * 24 }, (_, i) => ({ day: 412 + Math.floor(i / 24), hour: i % 24, is_now: i === 5, air_temp_c: 6, surface_c: 8, water_c: 9, deep_c: 7, rain_mm: 0, thunder: 0 })),
      bands: [{ from_c: 2, to_c: 4, count: 24, share: 0.14 }],
      seasons: [{ season: '春', season_name: '春', water_min_c: 6, water_max_c: 14, is_now: true }],
      maps: [],
    },
  },
  '/api/weather/active-fish': { active: { game_hour: 14, segment: '白昼', season: '春', water_c: 12.4, active_num: 1, total: 24, fish: [{ fish_key: 'rudd', name_zh: '拟鲤', score: 0.82, rarity: 1, rarity_tag: '普通', diel_label: '昼行', temp_min_c: 8, temp_max_c: 18 }], idle: [] } },
  '/api/econ/species': { items: [{ fish_key: 'rudd', name_zh: '拟鲤', overridden: true, grade_names: ['普通', '达标', '奖杯', '蓝冠'], grades: [{ grade: 1, xp: 10, price: 2 }, { grade: 2, xp: 20, price: 4 }, { grade: 3, xp: 40, price: 9 }, { grade: 4, xp: 80, price: 20 }] }], maps: [{ key: 'copper', name: '铜湖', mul: 1 }] },
  '/api/fish/weights/preset': { weights: [100, 30, 5], active: true, rows: 480, tiers: [{ tier: 0, species: 180, rows: 320, avg_base: 96.4 }] },
  '/api/fish/ecology-species': { total: 1, items: [{ fish_key: 'rudd', name_zh: '拟鲤', overlaid: true, base: { temp_min_c: 8, temp_max_c: 18, temp_pref_c: 13, diel_mask: 3, enabled: 1, layer_min: 0.1, layer_pref: 0.4, layer_max: 0.7 }, override: {} }] },
  '/api/econ/preview': { ok: true },
  // 真实接口是扁平 map[string]string，不是 {v,src} 也不是 {items:[]}
  '/api/tunables': { bite_delay_max: '2.8', bite_delay_scale: '1', bite_probability: '1' },
  '/api/fish/pool': [],
  '/api/event/stage-gifts': { branches: [] },
  '/api/sync/export': {},
};
function fixture(pathname) {
  const keys = Object.keys(F).sort((a, b) => b.length - a.length);
  for (const k of keys) if (pathname.endsWith(k)) return F[k];
  if (pathname.includes('/api/')) return {};
  return null;
}

async function wire(page) {
  await page.route('**/*', (r) => {
    const u = new URL(r.request().url());
    if (fx.serveAsset(r, u)) return;
    const d = fixture(u.pathname);
    if (d === null) return r.fulfill({ status: 404, body: '' });
    return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(d) });
  });
  await page.addInitScript(() => {
    localStorage.setItem('rf4_admin_token', 'fixture');
    localStorage.setItem('rf4.theme', 'light');
    // 无头环境里 confirm 默认返回 false，未保存守卫会把导航拽回去，
    // 于是后面几页读到的还是上一页。盘点只看结构，一律放行。
    window.confirm = () => true;
  });
}

// 等切换落定：连续两次文字一致且不 busy。
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

const EXTRACT = (sel) => {
  const root = document.querySelector(sel);
  if (!root) return [];
  const out = new Set();
  const txt = (e) => (e.innerText || e.textContent || '').replace(/\s+/g, ' ').trim();
  for (const e of root.querySelectorAll('*')) {
    const tag = e.tagName.toLowerCase();
    const clickable = tag === 'button' || e.hasAttribute('onclick') || (tag === 'a' && e.getAttribute('href')) || e.getAttribute('role') === 'button';
    if (clickable) {
      const t = txt(e).slice(0, 44);
      if (t) out.add('click :: ' + t);
    } else if (tag === 'select') {
      out.add('select :: ' + (e.getAttribute('aria-label') || e.getAttribute('id') || '?'));
    } else if (tag === 'input' || tag === 'textarea') {
      out.add('input :: ' + (e.getAttribute('id') || e.getAttribute('aria-label') || e.getAttribute('placeholder') || e.type || '?'));
    }
  }
  return [...out].sort();
};

// 需要展开的二级页签：路由 / 页签名 / 文案
const TAB_PLAN = [
  ['rules/switches', '钓场-玩法开关'],
  ['rules/grade', '钓场-出鱼与奖杯'],
  ['rules/other', '钓场-其他'],
  ['waters/铜湖/fish', '水域-鱼种'],
  ['waters/铜湖/zones', '水域-鱼群区域'],
  ['ecology/eco', '生态-基线'],
  ['ecology/bait', '生态-饵料规则'],
  ['ecology/econ', '生态-经济四档'],
  ['ecology/weight', '生态-权重档位'],
];

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
  await wire(page);
  await page.goto('http://127.0.0.1:1/#/dashboard', { waitUntil: 'networkidle', timeout: 30000 });
  await settle(page);

  const out = {};
  for (const [route, label] of TAB_PLAN) {
    await page.evaluate((r) => { location.hash = '#/' + r; }, route);
    await settle(page);
    const h = await page.evaluate(() => (document.querySelector('#view h1') || {}).innerText || '-');
    out[label] = { route, h1: h, controls: await page.evaluate(
      ([sel, fnSrc]) => new Function('return (' + fnSrc + ')')()(sel),
      ['#view', EXTRACT.toString()]) };
    console.log(label.padEnd(18), 'h1=' + h.padEnd(8), String(out[label].controls.length).padStart(3) + ' 个可操作件');
  }

  // 生态页四个页签额外按 #ecoTabs 点一遍，确认页签本身齐全
  await page.evaluate(() => { location.hash = '#/ecology'; });
  await settle(page);
  const etabs = await page.evaluate(() => [...document.querySelectorAll('#ecoTabs button')].map((b) => b.dataset.etab + ':' + b.innerText.trim()));
  console.log('\n生态页签:', etabs.join(' | '));
  const rtabs = await page.evaluate(() => [...document.querySelectorAll('#tabs a')].map((a) => a.getAttribute('href') + ':' + a.innerText.trim()));
  console.log('生态页 #tabs:', rtabs.join(' | '));

  fs.writeFileSync(path.join(__dirname, '_inventory_tabs.json'), JSON.stringify(out, null, 2));
  console.log('\n明细写入 _inventory_tabs.json');
  await page.close();
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });