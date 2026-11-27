// 只读盘点：把旧后台（legacy.html）每个视图的操作入口、和 SPA 每个页面的控件，
// 各导出一份清单，用来做「旧后台能做的，新平台也能做」的逐项对照。
//
// 这不是测试，是核对工具 —— 它不改任何东西，只打印两张表。
//
//   export RF4_TEST_BROWSER="C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"
//   node internal/admin/inventory_check.cjs
const path = require('node:path');
const fs = require('node:fs');
const { chromium } = require('playwright');
const fx = require('./web_fixture.cjs');

const BROWSER = process.env.RF4_TEST_BROWSER || undefined;

// 两侧共用的假后端：够让页面结构渲染出来即可，不需要业务正确。
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
  // /api/tunables 真实返回 `map[string]string`（store.TunableList 就是
  // `SELECT key, value` 拼成的扁平对象，直接 writeJSON），**不是** `{v,src}` 结构。
  // 早先我按 `{v, src}` 写，值进了输入框变成字面量 "[object Object]"，
  // 于是「原值 ≠ 当前值」被判成脏、离开页面被未保存守卫拦住 —— 一整个排查环路
  // 都是这条 fixture 造出来的，产品本身没问题。这里按真实结构写。
  '/api/tunables': { bite_delay_max: '2.8', bite_delay_scale: '1', bite_probability: '1' },
  '/api/fish/pool': [],
};
function fixture(pathname) {
  for (const [k, v] of Object.entries(F)) if (pathname.endsWith(k)) return v;
  if (pathname.includes('/api/')) return {};
  return null;
}

async function wire(page, theme) {
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
  }, theme || 'light');
}

// 把一段 DOM 里的「可操作件」导出成「角色 :: 文字」的字符串集合。
// 只看能点/能填的：button、带 onclick 的元素、a、select、input、textarea。
//
// 注意选择器只给一个具体容器（`#view`），不要写成 `'#view, main, body'`。
// querySelector 按**文档顺序**返回第一个命中项，body 是祖先、永远排最前，
// 那样写等于一直在数整个页面（包括左栏导航），每页都会得出同一个大数字。
const EXTRACT = (sel) => {
  const root = document.querySelector(sel);
  if (!root) return [];
  const out = new Set();
  const txt = (e) => (e.innerText || e.textContent || '').replace(/\s+/g, ' ').trim();
  for (const e of root.querySelectorAll('*')) {
    const tag = e.tagName.toLowerCase();
    const clickable = tag === 'button' || e.hasAttribute('onclick') || (tag === 'a' && e.getAttribute('href'));
    if (clickable) {
      const t = txt(e).slice(0, 40);
      const o = e.getAttribute('onclick') || '';
      out.add('click :: ' + (t || o.replace(/\(.*/, '') || tag));
    } else if (tag === 'select') {
      out.add('select :: ' + (e.getAttribute('id') || e.getAttribute('aria-label') || e.name || '?'));
    } else if (tag === 'input' || tag === 'textarea') {
      out.add('input :: ' + (e.getAttribute('id') || e.getAttribute('aria-label') || e.getAttribute('placeholder') || e.type || '?'));
    }
  }
  return [...out].sort();
};

// 等一次切换真正落定。
//
// 不能固定 sleep：`core.render()` 刻意**不先清空**旧页（否则 hero 照片会闪），
// 数据没回来之前 `#view` 里还是上一页的内容。等太短就会把上一页当成本页，
// 盘点结果里每页都长一个样 —— 我第一次跑就是这么被骗的（release/about 数出和
// tuning 一样的 44 个控件）。
//
// 判据必须是「连续两次采样**文字完全一致**且不 busy」，不能只比长度：
// 两页文字长度碰巧相同就会误判为已稳定（release 208 与 about 230 差得不多，
// 再多几页总有一对相等）。
async function settle(page) {
  let last = null;
  for (let i = 0; i < 80; i++) {
    await page.waitForTimeout(120);
    const s = await page.evaluate(() => {
      const v = document.querySelector('#view');
      return { busy: v ? v.classList.contains('is-busy') : true, sig: v ? v.innerText.replace(/\s+/g, ' ').trim() : '' };
    });
    if (!s.busy && s.sig && s.sig === last) return i;
    last = s.sig;
  }
  return -1;
}

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: BROWSER });
  const result = { legacy: {}, spa: {} };

  // ---- 旧后台：13 个 #view-* ----
  // 这些视图是 display:none 叠在一起的，但元素都在 DOM 里，所以直接按 id 取即可，
  // 不需要真的切过去（切过去反而会触发一堆加载）。params / audit 数出 0 是因为
  // 它们的控件是运行时才生成的 —— 那正是「旧后台也要靠 JS 建界面」的体现，
  // 这两页改用「动作函数名」对照（见下面的 ACTIONS 表）。
  {
    const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
    await wire(page);
    await page.goto('http://127.0.0.1:1/legacy.html', { waitUntil: 'domcontentloaded', timeout: 30000 });
    await page.waitForTimeout(800);
    const views = await page.evaluate(() =>
      [...document.querySelectorAll('[id^="view-"]')].map((e) => e.id));
    for (const v of views) {
      result.legacy[v] = await page.evaluate(
        ([sel, fnSrc]) => {
          // eslint-disable-next-line no-new-func
          const fn = new Function('return (' + fnSrc + ')')();
          return fn(sel);
        }, ['#' + v, EXTRACT.toString()]);
    }
    await page.close();
  }

  // ---- SPA：按左栏导航逐页 ----
  {
    const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } });
    await wire(page);
    await page.goto('http://127.0.0.1:1/#/dashboard', { waitUntil: 'networkidle', timeout: 30000 });
    await settle(page);
    const routes = ['dashboard', 'players', 'shop', 'rules', 'ecology', 'waters', 'hooks', 'bait',
      'weather', 'mail', 'tuning', 'release', 'sync', 'about', 'audit'];
    for (const r of routes) {
      // 用页面内改 hash，不用 page.goto。
      // goto 到同一文档的另一个 hash 时 Playwright 只改 hash、**不重新加载文档**，
      // 而 SPA 的 hashchange 监听是异步的；连着 goto 会在还没渲染完时就读 DOM，
      // 结果每页都读到上一页的残留（第一次跑就是这么把 5 个页面读成同一个 44）。
      // 这里显式派发 hashchange 并等它落定。
      await page.evaluate((rr) => { location.hash = '#/' + rr; }, r);
      await settle(page);
      result.spa[r] = await page.evaluate(
        ([sel, fnSrc]) => {
          const fn = new Function('return (' + fnSrc + ')')();
          return fn(sel);
        }, ['#view', EXTRACT.toString()]);
    }
    await page.close();
  }
  await browser.close();

  fs.writeFileSync(path.join(__dirname, '_inventory.json'), JSON.stringify(result, null, 2));
  for (const [k, v] of Object.entries(result.legacy)) {
    console.log(`legacy ${k.padEnd(16)} ${String(v.length).padStart(4)} 个可操作件`);
  }
  for (const [k, v] of Object.entries(result.spa)) {
    console.log(`spa    ${k.padEnd(16)} ${String(v.length).padStart(4)} 个可操作件`);
  }
  console.log('\n明细写入 _inventory.json');
})().catch((e) => { console.error(e); process.exit(1); });