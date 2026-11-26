// 生态基线页签：按鱼种维护水温 / 季节 / 昼夜 / 水层 / 活跃度。
//
// 三层来源：资产基线（fish_ecology）→ 本页覆盖（fish_ecology_species）→
// 水域覆盖（钓场页弹窗）。本页改的是「这个物种的习性」，对全服所有水域生效。
//
// 两处最容易出错、也最需要守住的地方：
//   1) **基线灰、覆盖金**：override 与 base 是两份独立对象，取值时不能串位。
//      列表单元格统一走 cellOf()，一处写错整列颜色就反了。
//   2) **昼夜曲线 vs 昼夜勾选**：曲线非空时判鱼只认曲线，勾选框仅用于界面展示。
//      两者打架时界面写「已覆盖」而咬口毫无变化，是最难排查的「改了没生效」。
//      保存时必须拦住（2026-11-26 实际踩过这个坑）。
import { h, api, toast, errText, btn, empty, loading, searchBox, debounce, sidePage, confirmDialog, callout } from '../core.js';

const SEASON_NAMES = ['春', '夏', '秋', '冬'];
const DIEL_NAMES = ['夜', '晨', '昼', '昏'];
const RARITY = ['普通', '稀有', '超稀有'];
// 曲线里 ≥0.5 的时段算「真的开口」。判鱼侧用同一个口径划分活跃段，
// 但那个阈值是隐式行为（曲线只压不抬），所以这里定一个显式常量对齐。
const DIEL_ACTIVE_LINE = 0.5;

let ROWS = [];
let Q = '';
let FILTER = '';
const PAGE = 40;
let SHOWN = PAGE;

// 位掩码 → 文字。m 为空返回空串（= 该字段未覆盖）。
function maskText(m, names) {
  if (m === null || m === undefined) return '';
  const on = names.filter((_, i) => m & (1 << i));
  return on.length ? on.join('/') : '无';
}

// 数值单元格：override 里显式给了值就标金色，否则灰显基线值。
// 「显式给了值」的判据是 != null，不能用真值判断——0 是合法的覆盖值
// （比如水层下限 0 就是水面、季节掩码 0 就是任何季节都不活跃）。
function cellOf(r, field, digits) {
  const ov = r.override ? r.override[field] : null;
  const base = r.base ? r.base[field] : null;
  const v = ov != null ? ov : base;
  const span = h('span', { class: ov != null ? 'overridden' : 'muted', text: num(v, digits) });
  return span;
}

// 生效值：基线叠覆盖（只取非 null 字段）。
// 与 gameserver.fishEcologyFor 同口径，只是不含水域层——本页展示的就是「物种本身」这一层。
// 后端注释明确说了不要在后端抄一份这套合并，交给前端用同一份 base+override 现算。
function effective(r) {
  const out = { ...(r.base || {}) };
  const ov = r.override || {};
  for (const k of Object.keys(ov)) {
    if (k === 'fish_key' || k === 'origin' || k === 'updated_by' || k === 'updated_ts') continue;
    if (ov[k] !== null && ov[k] !== undefined) out[k] = ov[k];
  }
  return out;
}

// 偏好温度的四分位（0~1）。用于「只看到冷水鱼 / 暖水鱼」两档筛选。
// 阈值必须跟着现有数据走：当前 253 种偏好温度实际只落在 9.9~21.2℃，
// 写死 22 会让「暖水鱼」永远 0 行——看起来像功能坏了。
function tempQuartile(q) {
  const vals = ROWS.map((r) => Number(effective(r).temp_pref_c))
    .filter((v) => isFinite(v)).sort((a, b) => a - b);
  if (!vals.length) return q < 0.5 ? 0 : Infinity;
  const i = Math.min(vals.length - 1, Math.max(0, Math.floor(vals.length * q)));
  return vals[i];
}

function layerBar(r) {
  const ov = r.override ? r.override.layer_pref : null;
  const e = effective(r);
  const lo = Number(e.layer_min), pf = Number(e.layer_pref), hi = Number(e.layer_max);
  if (!isFinite(lo) || !isFinite(hi)) return h('span', { class: 'muted', text: '—' });
  const gold = ov != null;
  const width = Math.max(2, (Math.min(1, hi) - Math.max(0, lo)) * 100);
  const txt = `${lo.toFixed(2)}~${hi.toFixed(2)} · 偏好 ${isFinite(pf) ? pf.toFixed(2) : '—'}`;
  // 深色带是 ±0.15 容差：钓层落在带外时权重 ×0.01，所以「带宽」直接决定
  // 这条鱼对钓组的挑不挑。原来只显示 layer_pref 一个数，看不出范围。
  return h('div', { style: { minWidth: '78px' } },
    h('div', { title: `活动 ${lo.toFixed(2)}~${hi.toFixed(2)} 偏好 ${isFinite(pf) ? pf.toFixed(2) : '—'}（0=水面 1=水底）`, style: { position: 'relative', height: '12px', borderRadius: '6px', background: 'var(--surface-4)', marginBottom: '3px' } },
      h('div', { style: { position: 'absolute', left: Math.max(0, (lo - 0.15) * 100) + '%', right: Math.max(0, 100 - Math.min(1, hi + 0.15) * 100) + '%', top: '1px', bottom: '1px', background: 'var(--accent-soft, var(--surface-3))', borderRadius: '5px', opacity: 0.55 } }),
      h('div', { style: { position: 'absolute', left: Math.max(0, lo * 100) + '%', width: width + '%', top: '1px', bottom: '1px', background: 'linear-gradient(90deg,var(--g2),var(--accent))', borderRadius: '5px' } }),
      isFinite(pf) ? h('div', { style: { position: 'absolute', left: `calc(${Math.max(0, Math.min(1, pf) * 100)}% - 1.5px)`, top: 0, bottom: 0, width: '3px', background: '#fff', borderRadius: '2px' } }) : null),
    h('div', { class: gold ? 'mono overridden' : 'mono muted', style: { fontSize: '11.5px' }, text: txt }));
}

export async function ecoTab() {
  const wrap = h('div');
  // id 是验收测试的锚点（#ecoStatus / #ecoTable / #ecoFilter）。卡片式布局没有
  // <table>，但「列表容器 + 每行一张 .fishcard」这层结构必须稳定，测试靠它数行。
  const status = h('span', { class: 'muted small', id: 'ecoStatus' });
  const listEl = h('div', { class: 'fishlist', id: 'ecoTable' });
  const banner = h('div');

  async function load() {
    listEl.replaceChildren(h('div', { style: { padding: '18px' } }, loading(5)));
    try {
      const d = await api('/api/fish/ecology-species' + (Q ? '?q=' + encodeURIComponent(Q) : ''));
      ROWS = d.items || [];
      status.textContent = statusLine(d);
      draw();
    } catch (e) { listEl.replaceChildren(empty('鱼种档案读取失败', errText(e) + '。这是列表接口出错，不是筛选问题；重试一次，仍失败就看服务端日志。')); }
  }

  function statusLine(d) {
    const ovr = ROWS.filter((r) => r.overlaid).length;
    return `${ROWS.length} / ${d.total || ROWS.length} 个鱼种 · ${ovr} 个已覆盖`;
  }

  function visible() {
    let rows = ROWS;
    if (FILTER === 'overlaid') rows = rows.filter((r) => r.overlaid);
    // 冷暖分批用全量数据的四分位当阈值，不用硬编码温度。
    else if (FILTER === 'cold') { const cut = tempQuartile(0.25); rows = rows.filter((r) => { const e = effective(r); return e.temp_pref_c != null && Number(e.temp_pref_c) <= cut; }); }
    else if (FILTER === 'warm') { const cut = tempQuartile(0.75); rows = rows.filter((r) => { const e = effective(r); return e.temp_pref_c != null && Number(e.temp_pref_c) >= cut; }); }
    return rows;
  }

  function draw() {
    const rows = visible();
    if (!rows.length) {
      // 分两种：库里有鱼但筛没了，和库里根本没数据（状态行会写「0 / N」）。
      listEl.replaceChildren(empty(ROWS.length ? '没有符合条件的鱼种' : '还没有鱼种档案',
        ROWS.length
          ? '当前的筛选（类型 / 搜索词）把这个都筛掉了。把筛选改回「全部」，或者清空搜索词。'
          : '后端一条鱼种生态档案都没返回。先去「参数同步」把鱼种数据导进来，或者确认服务端数据目录是完整的。'));
      return;
    }
    const page = rows.slice(0, SHOWN);
    const more = rows.length > page.length
      ? h('div', { style: { display: 'flex', justifyContent: 'center', padding: '14px 0 4px' } },
        btn(`再显示 ${Math.min(PAGE, rows.length - page.length)} 种（还有 ${rows.length - page.length} 种）`, () => { SHOWN += PAGE; draw(); }, 'ghost'))
      : null;
    listEl.replaceChildren(...page.map(card), ...(more ? [more] : []));
  }

  // 卡片：上行是身份与水层条，下行是四组数据（水温窗 / 窗宽判定 / 季节昼夜 / 活跃度）。
  // 用卡片不用表格：253 行的宽表在窄屏上会横滚，而真正要横向对照的只有
  // 「窗宽」和「水层」两项，把它们放大成条形图比塞进单元格更有用。
  function card(r) {
    const e = effective(r);
    const w = (e.temp_max_c != null && e.temp_min_c != null) ? (Number(e.temp_max_c) - Number(e.temp_min_c)) : null;
    // 窗宽是「温度能不能筛鱼」的关键：热响应容差 = max(2.2, 窗宽/2)，窗宽 20℃ 时曲线极平。
    const wCls = w == null ? 'muted' : w <= 8 ? 'ok-text' : w <= 14 ? 'warn-text' : 'bad-text';
    const wNote = w == null ? '' : w <= 8 ? '窄：温度能有效筛鱼' : w <= 14 ? '中等：有一定区分力' : '宽：温度几乎不参与筛选';
    const locs = r.locs || [];
    return h('section', { class: 'fishcard' },
      h('div', { class: 'fishcard-head' },
        h('span', { class: 'fi' },
          h('b', { class: 'fname', text: r.name_zh }),
          h('span', { class: 'fkey', text: r.fish_key }),
          h('span', { class: 'badge ' + (r.rarity_tier >= 2 ? 'warn' : r.rarity_tier >= 1 ? 'accent' : ''), text: RARITY[r.rarity_tier] || String(r.rarity_tier) })),
        h('span', { class: 'now' },
          h('em', { text: '水温' }),
          h('span', { class: 'mono' }, cellOf(r, 'temp_min_c', 1), ' ~ ', cellOf(r, 'temp_max_c', 1), ' ℃'),
          h('span', { class: 'muted small' }, ' · 窗宽 '),
          h('span', { class: 'mono ' + wCls, text: w == null ? '—' : w.toFixed(1) })),
        h('span', { class: 'grow' }),
        h('span', { class: r.overlaid ? 'pill ok' : 'pill', text: r.overlaid ? (r.origin === 'curated' ? '文献值' : '已覆盖') : '基线' }),
        h('button', { type: 'button', class: 'edit', text: '展开修改', onclick: () => editor(r, load) })),
      h('div', { class: 'fishcard-body eco-grid' },
        h('div', {},
          h('em', { text: '栖息水层' }),
          layerBar(r)),
        h('div', {},
          h('em', { text: '活跃时段' }),
          h('div', { class: 'small' }, '季节 ', h('span', { class: (r.override && r.override.season_mask != null) ? 'overridden' : 'muted', text: maskText(pick(r, 'season_mask'), SEASON_NAMES) || '—' })),
          h('div', { class: 'small' }, '昼夜 ', h('span', { class: (r.override && r.override.diel_mask != null) ? 'overridden' : 'muted', text: maskText(pick(r, 'diel_mask'), DIEL_NAMES) || '—' }))),
        h('div', {},
          h('em', { text: '活跃度 / 攻击性' }),
          h('div', { class: 'small' }, cellOf(r, 'activity', 2), h('span', { class: 'muted' }, ' / '), cellOf(r, 'aggression', 2))),
        h('div', {},
          h('em', { text: '出现的水域' }),
          locs.length
            ? h('div', { class: 'small muted', style: { lineHeight: '1.55' }, text: locs.join(' · ') })
            : h('span', { class: 'muted small', text: '—' }))),
      wNote ? h('div', { class: 'muted small', style: { padding: '0 0 12px' }, text: `窗宽判定：${wNote}（热响应容差 = max(2.2, 窗宽/2) = ${w == null ? '—' : Math.max(2.2, w / 2).toFixed(1)}℃）` }) : null);
  }

  // 取某字段的生效值（覆盖优先，否则基线）。
  function pick(r, k) {
    const ov = r.override ? r.override[k] : null;
    return ov != null ? ov : (r.base ? r.base[k] : null);
  }

  await load();

  // 筛选下拉：core.js 没有 select() 这个封装（只有 segmented），这里直写 <select>。
  // id 供验收测试用 #ecoFilter 直接 selectOption。
  const filterSel = h('select', {
    class: 'select', id: 'ecoFilter', 'aria-label': '筛选鱼种', style: { width: '160px', flex: '0 0 auto' },
    onchange: (e) => { FILTER = e.target.value; SHOWN = PAGE; draw(); },
  }, [['', '全部鱼种'], ['overlaid', '只看已覆盖'], ['cold', '只看到冷水鱼'], ['warm', '只看到暖水鱼']]
    .map(([v, l]) => h('option', { value: v, text: l, selected: v === FILTER })));

  wrap.append(
    banner,
    h('div', { class: 'row', style: { gap: '8px', margin: '0 0 12px', flexWrap: 'nowrap' } },
      h('div', { style: { flex: '1 1 100%', minWidth: '0' } }, searchBox('搜索鱼种 key / 中文名，如 burbot、江鳕', debounce((v) => { Q = v; SHOWN = PAGE; load(); }, 300), Q)),
      filterSel,
      h('span', { class: 'spacer' }),
      status),
    h('p', { class: 'muted small', style: { margin: '0 0 12px', lineHeight: '1.75' } },
      h('b', { class: 'overridden', text: '金色' }), '＝已覆盖，灰色＝资产基线。温度单位 ℃，',
      h('b', { text: '窗宽直接决定热响应的陡缓' }), '——现在多数鱼的窗宽在 20℃ 上下，等于温度几乎不筛鱼。',
      h('br'),
      '季节/昼夜是位掩码（季节 1春 2夏 4秋 8冬，昼夜 1夜 2晨 4昼 8昏）。水层 0=水面 1=水底，浅色带是 ±0.15 容差，钓层落在带外权重×0.01。'),
    listEl);
  return wrap;
}

// ===== 编辑器 =====
// 留空 = 继承基线，下发 null。绝不能把空串转成 0：0 会被当成合法覆盖值写进去，
// 比如水层下限 0 表示水面、季节掩码 0 表示任何季节都不活跃，含义完全不同。
function editor(r, reload) {
  const ov = r.override || {}, base = r.base || {};
  const st = {};
  const fields = ['layer_min', 'layer_pref', 'layer_max', 'temp_min_c', 'temp_pref_c', 'temp_max_c',
    'current_pref', 'activity', 'light_pref', 'clarity_pref', 'aggression'];
  fields.forEach((k) => { st[k] = ov[k] === null || ov[k] === undefined ? '' : String(ov[k]); });
  st.season = { mode: ov.season_mask != null ? '1' : '', mask: ov.season_mask != null ? Number(ov.season_mask) : null };
  st.diel = { mode: ov.diel_mask != null ? '1' : '', mask: ov.diel_mask != null ? Number(ov.diel_mask) : null };
  // 曲线入库是 JSON 字符串（"[1,0.3,0.2,0.5]"）。编辑框里用逗号纯文本，
  // 保存时再拼回 JSON：让管理员手写 JSON 括号是自找麻烦。
  st.curve = ov.diel_curve ? String(ov.diel_curve).replace(/[[\]"\s]/g, '').split(',').map((s) => s.replace(/[^0-9.]/g, '')).join(',') : '';
  st.enabled = ov.enabled === null || ov.enabled === undefined ? '' : String(ov.enabled);
  const initial = JSON.stringify(st);
  let saved = false;

  // 记下打开时的昼夜勾选状态，保存时据此判断「用户有没有动过它」。
  // 只在动过时才校验曲线一致性：库里可能有历史数据本身就曲线/掩码不匹配，
  // 用户若只想改水温，不该被昼夜口径挡回来。
  const dielInit = { mode: st.diel.mode, mask: st.diel.mask };

  const body = h('div');
  // id 供测试定位实时提示（#ecoTempHint / #ecoDielHint）。
  const tempHint = h('div', { class: 'hint', id: 'ecoTempHint', style: { marginTop: '2px' } });
  const dielHint = h('div', { class: 'hint', id: 'ecoDielHint', style: { marginTop: '4px' } });

  const inhDecimals = { temp_min_c: 1, temp_pref_c: 1, temp_max_c: 1, layer_min: 2, layer_pref: 2, layer_max: 2, activity: 2, aggression: 2, current_pref: 2, light_pref: 2, clarity_pref: 2 };
  // 继承提示要写清「继承 18.9」而不是「继承」：否则看不出当前到底用多少。
  // 精度跟着字段走，不能直接 String(b)——基线值是合成小数（18.9184），原样显示太吵。
  function inh(k) {
    const b = base[k];
    if (b === null || b === undefined) return '继承';
    const d = inhDecimals[k];
    return '继承 ' + (d === undefined ? b : Number(b).toFixed(d));
  }

  function numField(key, label, step, hint) {
    const input = h('input', { class: 'input', id: 'eco_s_' + shortId(key), type: 'text', inputmode: 'decimal', value: st[key],
      placeholder: inh(key), oninput: (e) => { st[key] = e.target.value; afterInput(); } });
    return h('div', { class: 'field' }, h('label', { for: 'eco_s_' + shortId(key), text: label }), input,
      hint ? h('div', { class: 'hint', text: hint }) : null);
  }

  // 掩码勾选组：4 个 checkbox，mode='' 表示继承基线。
  // 实时显示「= 9（春/冬）」，只给数字管理员还得自己换算成哪些时段。
  function maskWidget(key, label, names) {
    const s = st[key];
    const curTxt = maskText(s.mode === '' ? base[key + '_mask'] : s.mask, names) || '—';
    const box = h('div', {
      id: 'eco_s_' + key + '_box', style: { marginTop: '8px', padding: '8px 10px', border: '1px solid var(--line)', borderRadius: '9px' },
      hidden: s.mode === '',
    },
    ...names.map((n, i) => h('label', { style: { display: 'inline-flex', alignItems: 'center', gap: '4px', marginRight: '14px', whiteSpace: 'nowrap', fontSize: '12px' } },
      h('input', { type: 'checkbox', id: `eco_s_${key}_b${i}`, checked: s.mask != null && !!(s.mask & (1 << i)),
        onchange: () => { sync(); } }),
      h('span', { text: n }))),
    h('span', { class: 'muted small', id: 'eco_s_' + key + '_txt' }));
    const modeSel = h('select', { class: 'select', id: 'eco_s_' + key + '_mode', style: { width: '100%' },
      onchange: (e) => { s.mode = e.target.value; box.hidden = s.mode === ''; sync(); } },
    h('option', { value: '', text: `继承基线（当前 ${curTxt}）`, selected: s.mode === '' }),
    h('option', { value: '1', text: '自定义', selected: s.mode !== '' }));

    function sync() {
      if (s.mode === '') { const t = document.getElementById('eco_s_' + key + '_txt'); if (t) t.textContent = ''; return; }
      let m = 0;
      for (let i = 0; i < 4; i++) { const b = document.getElementById(`eco_s_${key}_b${i}`); if (b && b.checked) m |= (1 << i); }
      s.mask = m;
      const on = names.filter((_, i) => m & (1 << i));
      const t = document.getElementById('eco_s_' + key + '_txt');
      if (t) t.textContent = '= ' + m + (on.length ? `（${on.join('/')}）` : '（全关）');
      afterInput();
    }
    if (s.mode !== '') setTimeout(sync, 0);   // 已覆盖的要立刻显示「= 9（春/冬）」
    return h('div', { class: 'field' }, h('label', { text: label }), modeSel, box,
      h('div', { class: 'hint', text: `位掩码：${names.map((n, i) => `${1 << i} ${n}`).join(' · ')}；全不勾＝0＝任何时段都不活跃` }));
  }

  function shortId(k) {
    return { temp_min_c: 'tmin', temp_pref_c: 'tpref', temp_max_c: 'tmax', layer_min: 'lmin', layer_pref: 'lpref', layer_max: 'lmax',
      current_pref: 'current', light_pref: 'light', clarity_pref: 'clarity' }[k] || k;
  }

  // 曲线解析：返回 4 个 0~1 的数，或 null（非法/留空）。
  function parseCurve(raw) {
    const s = String(raw || '').trim();
    if (s === '') return null;
    const parts = s.replace(/[[\]]/g, '').split(/[,\s]+/).filter(Boolean).map(Number);
    if (parts.length !== 4 || parts.some((v) => !Number.isFinite(v) || v < 0 || v > 1)) return undefined; // undefined = 非法
    return parts;
  }
  // 生效曲线：输入框优先，留空则取基线（与判鱼侧「曲线非空就用曲线」同口径）。
  function effectiveCurve() {
    const p = parseCurve(st.curve);
    if (p !== null) return p === undefined ? null : p;
    const b = base.diel_curve;
    if (!b) return null;
    try { const c = JSON.parse(b); return Array.isArray(c) && c.length === 4 ? c : null; } catch { return null; }
  }

  // 实时算窗宽与热响应容差，直接对应判鱼侧：
  //   tolerance := max(2.2, (temp_max - temp_min) / 2)
  function updateTemp() {
    const g = (k) => { const raw = String(st[k]).trim(); return raw === '' ? null : Number(raw); };
    const pickV = (k) => { const c = g(k); if (c != null && isFinite(c)) return c; const b = base[k]; return b == null ? null : Number(b); };
    const tmin = pickV('temp_min_c'), tmax = pickV('temp_max_c'), tpref = pickV('temp_pref_c');
    if (tmin == null || tmax == null || tpref == null || !(tmax > tmin)) { tempHint.textContent = '—'; return; }
    const width = tmax - tmin, tol = Math.max(2.2, width / 2);
    const verdict = width <= 8 ? '窄窗口：温度能有效筛鱼' : width <= 14 ? '中等：温度有一定区分力' : '宽窗口：温度几乎不参与筛选（热响应曲线极平）';
    tempHint.replaceChildren('窗口宽 ', h('b', { text: width.toFixed(1) + '℃' }), ' · 热响应容差 ', h('b', { text: tol.toFixed(1) + '℃' }),
      ` · 偏好距两端 ${(tpref - tmin).toFixed(1)} / ${(tmax - tpref).toFixed(1)}℃ → `,
      h('span', { class: width <= 14 ? '' : 'bad-text', text: verdict }));
  }

  // 曲线与掩码是两套参数，必须实时告诉编辑者「现在到底哪几个时段真的开口」，
  // 否则会出现「勾了昏、没改曲线、保存后咬口毫无变化」这种无从排查的情况。
  function updateDiel() {
    const curve = effectiveCurve();
    let mask = null;
    if (st.diel.mode !== '') mask = st.diel.mask;
    else if (base.diel_mask != null) mask = Number(base.diel_mask);
    const maskOn = mask == null ? [] : DIEL_NAMES.filter((_, i) => mask & (1 << i));
    if (!curve) {
      dielHint.replaceChildren('当前没有曲线，判鱼按勾选生效：', h('b', { text: maskOn.length ? maskOn.join('/') : '无' }), '（命中 1.0 / 未命中 0.35）。');
      return;
    }
    const curveOn = DIEL_NAMES.filter((_, i) => curve[i] >= DIEL_ACTIVE_LINE);
    const same = curveOn.join('/') === maskOn.join('/');
    const fmt = curve.map((v, i) => `${DIEL_NAMES[i]} ${v.toFixed(2)}`).join(' · ');
    dielHint.replaceChildren('曲线 ', h('span', { class: 'mono', text: fmt }), ' → 实际开口 ',
      h('b', { text: curveOn.length ? curveOn.join('/') : '无' }),
      same ? h('span', { class: 'ok-text', text: '（与勾选一致）' })
        : h('span', { class: 'bad-text', text: `（与勾选「${maskOn.length ? maskOn.join('/') : '无'}」不一致：判鱼只认曲线，勾选仅展示）` }));
  }
  function afterInput() { updateTemp(); updateDiel(); }

  function paint() {
    let curveInput = null;
    body.replaceChildren(
      // 基线摘要：一行看完「继承来的到底是什么」，不用逐个输入框看 placeholder。
      h('div', { class: 'callout info' }, h('div', { class: 'grow' },
        h('div', {}, '基线档案：水温 ', h('b', { text: `${num(base.temp_min_c, 1)}~${num(base.temp_max_c, 1)}℃` }), `（偏好 ${num(base.temp_pref_c, 1)}）`,
          ` · 水层 ${num(base.layer_min, 2)}~${num(base.layer_max, 2)}（偏好 ${num(base.layer_pref, 2)}）`,
          ` · 活跃 ${num(base.activity, 2)} · 昼夜 ${maskText(base.diel_mask, DIEL_NAMES) || '—'} · 季节 ${maskText(base.season_mask, SEASON_NAMES) || '—'} · 攻击性 ${num(base.aggression, 2)}`),
        h('div', { class: 'muted small', text: '留空的字段＝继承这份基线；覆盖只作用于本物种，对全部水域生效。' }))),

      h('h3', { style: { fontSize: '13px', margin: '16px 0 8px' }, text: '水温（判鱼热响应的自变量）' }),
      h('div', { class: 'form-grid', style: { gridTemplateColumns: 'repeat(3,minmax(0,1fr))' } },
        numField('temp_min_c', '水温下限 ℃', '0.5', '低于此温度几乎不开口'),
        numField('temp_pref_c', '水温偏好 ℃', '0.5', '最活跃的温度（热响应峰值）'),
        numField('temp_max_c', '水温上限 ℃', '0.5', '高于此温度几乎不开口')),
      tempHint,

      h('h3', { style: { fontSize: '13px', margin: '18px 0 8px' }, text: '栖息水层（0=水面 1=水底）' }),
      h('div', { class: 'form-grid', style: { gridTemplateColumns: 'repeat(3,minmax(0,1fr))' } },
        numField('layer_min', '水层下限', '0.01', ''),
        numField('layer_pref', '水层偏好', '0.01', '权重峰值所在深度'),
        numField('layer_max', '水层上限', '0.01', '')),

      h('h3', { style: { fontSize: '13px', margin: '18px 0 8px' }, text: '活跃时段' }),
      h('div', { class: 'form-grid', style: { gridTemplateColumns: 'repeat(2,minmax(0,1fr))' } },
        maskWidget('season', '季节掩码', SEASON_NAMES),
        maskWidget('diel', '昼夜掩码', DIEL_NAMES)),

      h('div', { class: 'field', style: { marginTop: '10px' } },
        h('label', { for: 'eco_s_curve', text: '昼夜曲线 夜/晨/昼/昏' }),
        curveInput = h('input', { class: 'input', id: 'eco_s_curve', type: 'text', value: st.curve,
          placeholder: '继承 ' + (base.diel_curve || '—'),
          oninput: (e) => { st.curve = e.target.value; updateDiel(); } }),
        h('div', { class: 'hint' }, '四个 0~1 的数，如 ', h('b', { text: '1,0.3,0.2,0.5' }),
          '；峰值固定 1、最低 0.08，只压不抬。', h('b', { text: '曲线非空时判鱼只看曲线，上面的昼夜勾选只用于界面展示' }),
          '——想让某个时段真的活跃，必须让它这一段 ≥0.5，光勾勾选框没用。'),
        dielHint),

      h('h3', { style: { fontSize: '13px', margin: '18px 0 8px' }, text: '行为系数' }),
      h('div', { class: 'form-grid', style: { gridTemplateColumns: 'repeat(4,minmax(0,1fr))' } },
        numField('activity', '活跃度', '0.05', '1=常规，>1 更活跃'),
        numField('aggression', '攻击性', '0.05', '1=常规，影响咬口积极性'),
        numField('current_pref', '水流偏好', '0.01', '0~1，越大越爱流水'),
        numField('light_pref', '光照偏好', '0.01', '0~1，越大越爱强光'),
        numField('clarity_pref', '清澈度偏好', '0.01', '0~1，越大越爱清水')),

      h('div', { class: 'hint', style: { marginTop: '12px' } },
        '食性 / 天气 / 拟饵动作 / 底质 / 水色五个偏好矩阵不在此处维护：它们是十几到二十几个键的权重表，手改一个字符就会让整条鱼的判鱼权重出错，且写进覆盖后没有回滚入口。需要调整请改 assets/fish_ecology_defs.json 后重灌基线。'),

      h('div', { class: 'field', style: { marginTop: '10px' } },
        h('label', { for: 'eco_s_enabled', text: '本物种是否参与判鱼' }),
        h('select', { class: 'select', id: 'eco_s_enabled', style: { width: '100%' }, onchange: (e) => { st.enabled = e.target.value; } },
          h('option', { value: '', text: `继承基线（${base.enabled === 0 ? '不出鱼' : '出鱼'}）`, selected: st.enabled === '' }),
          h('option', { value: '1', text: '出鱼', selected: st.enabled === '1' }),
          h('option', { value: '0', text: '不出鱼', selected: st.enabled === '0' })),
        h('div', { class: 'hint', text: '选「不出鱼」= 全服所有水域都不再判到这种鱼' })));

    updateTemp(); updateDiel();
  }
  paint();

  function readMask(key) {
    const s = st[key];
    if (s.mode === '') return null;
    let m = 0;
    for (let i = 0; i < 4; i++) { const b = document.getElementById(`eco_s_${key}_b${i}`); if (b && b.checked) m |= (1 << i); }
    return m;
  }
  const numOf = (k) => { const raw = String(st[k]).trim(); return raw === '' ? null : Number(raw); };

  const save = async () => {
    // 曲线校验先做：非法值不该发出请求。
    const curveArr = parseCurve(st.curve);
    if (curveArr === undefined) return toast('昼夜曲线要四个 0~1 的数，顺序 夜,晨,昼,昏', 'bad');
    const diel_curve = curveArr ? JSON.stringify(curveArr) : null;

    const body2 = {
      fish_key: r.fish_key,
      layer_min: numOf('layer_min'), layer_pref: numOf('layer_pref'), layer_max: numOf('layer_max'),
      temp_min_c: numOf('temp_min_c'), temp_pref_c: numOf('temp_pref_c'), temp_max_c: numOf('temp_max_c'),
      diel_mask: readMask('diel'), diel_curve,
      season_mask: readMask('season'),
      // 五个偏好矩阵**根本不下发**，而不是发 null——发 null 会把已有的矩阵覆盖
      // 清成「继承基线」，等于静默丢数据。弹窗里已经没有它们的输入框了。
      current_pref: numOf('current_pref'), activity: numOf('activity'),
      light_pref: numOf('light_pref'), clarity_pref: numOf('clarity_pref'), aggression: numOf('aggression'),
      enabled: st.enabled === '' ? null : Number(st.enabled),
    };
    const labels = { layer_min: '水层下限', layer_pref: '水层偏好', layer_max: '水层上限', temp_min_c: '水温下限', temp_pref_c: '水温偏好', temp_max_c: '水温上限', current_pref: '水流偏好', activity: '活跃度', light_pref: '光照偏好', clarity_pref: '清澈度偏好', aggression: '攻击性' };
    for (const [k, v] of Object.entries(body2)) {
      if (typeof v === 'number' && Number.isNaN(v)) return toast((labels[k] || k) + ' 不是有效数字', 'bad');
    }

    // 曲线一旦给了（含继承来的），判鱼就只认曲线。此时若用户改了昼夜勾选却与曲线
    // 不一致，界面会写「已覆盖」而实际咬口没变，是最难排查的一类「改了没生效」。
    const touched = dielInit.mode !== st.diel.mode || dielInit.mask !== readMask('diel');
    if (body2.diel_mask != null && touched) {
      const eff = effectiveCurve();
      if (eff) {
        const curveOn = [0, 1, 2, 3].filter((i) => eff[i] >= DIEL_ACTIVE_LINE);
        const maskOn = [0, 1, 2, 3].filter((i) => body2.diel_mask & (1 << i));
        if (curveOn.join() !== maskOn.join()) {
          const n = DIEL_NAMES;
          return toast(`昼夜勾选与曲线不一致：曲线让 ${curveOn.map((i) => n[i]).join('/') || '无'} 开口，勾选是 ${maskOn.map((i) => n[i]).join('/') || '无'}。判鱼只认曲线，请把两者改成一致（或清空曲线用勾选）`, 'bad');
        }
      }
    }

    try {
      await api('/api/fish/ecology-species', { method: 'POST', body: JSON.stringify(body2) });
      toast('已保存：' + r.fish_key + '，最迟 30 秒生效');
      saved = true; d.close(); reload();
    } catch (e) { toast('保存失败：' + errText(e), 'bad'); }
  };

  const d = sidePage({
    title: r.name_zh, sub: r.fish_key, body,
    desc: '留空＝继承基线；这里改的是物种本身，对全服所有水域生效',
    effect: '保存后最迟 30 秒生效，无需重启。',
    foot: [
      r.overlaid ? btn('恢复基线', async () => {
        if (!(await confirmDialog({ title: '恢复基线', desc: `「${r.name_zh}」的鱼种档案覆盖将全部删除，回到资产基线（其他水域不受影响）。`, confirm: '恢复基线', danger: true }))) return;
        try {
          await api('/api/fish/ecology-species/reset', { method: 'POST', body: JSON.stringify({ fish_key: r.fish_key }) });
          toast('已恢复基线：' + r.fish_key); saved = true; d.close(); reload();
        } catch (e) { toast(errText(e), 'bad'); }
      }, 'ghost') : null,
      h('span', { class: 'spacer' }),
      btn('保存', save, 'primary'),
    ],
    beforeClose: () => saved || JSON.stringify(st) === initial || window.confirm('放弃未保存的生态修改？'),
    isDirty: () => !saved && JSON.stringify(st) !== initial,
  });
}

// 数字格式化：空/null/非数一律 —，避免列表里出现 NaN 或 undefined。
function num(v, d = 1) {
  return (v == null || v === '' || !isFinite(Number(v)) ? '—' : Number(v).toFixed(d));
}