// 经济四档页签：按鱼种逐档覆盖 XP/克 与基准水域银/公斤。
//
// 四档阶梯是**游戏内结算基准**（2026-11-23 定稿）：
//   经验 1 : 3.27 : 20.4 : 40.8、鱼价 1 : 1.5 : 3 : 6（都相对未达标）
// 每种鱼只有「未达标基数」是独立数据，另外三档由阶梯推出。改档位溢价
// 必须走提案并同步测试，不要用逐鱼种覆盖绕过——那样会让阶梯名存实亡。
//
// 试算页最容易挨骂的一点：卖价 = 基准价 × 水域倍率 × **当日行情系数** × 克重 × 新鲜度。
// 不把系数和算式摊开，页面上就会出现「1kg 却比每公斤价高」，看着像算错
// （2026-11-23 实际报障）。所以 runPreview 里逐项写出，一样都不能省。
import { h, api, toast, errText, btn, empty, loading, searchBox, debounce, sidePage, confirmDialog, fmt } from '../core.js';

const GRADES = [1, 2, 3, 4];
const GRADE_NAMES = { 1: '未达标', 2: '达标', 3: '上星', 4: '蓝冠' };

// 四档数值保留 4 位：经验是 0.0735 这类小数，截到 2 位就看不出阶梯了。
function econNum(v) {
  if (v == null || v === '') return '—';
  const n = Number(v);
  if (!isFinite(n)) return '—';
  return String(Math.round(n * 10000) / 10000);
}

// 覆盖值金色、内置值灰色。判据是 != null，不能用真值——0 不会出现在这里
// （后端要求覆盖值 > 0），但保持与生态页同一口径，免得以后改规则时两边不一致。
function econCell(ov, base) {
  if (ov != null) return h('b', { class: 'overridden', text: econNum(ov) });
  if (base > 0) return h('span', { class: 'muted', text: econNum(base) });
  return h('span', { class: 'muted', text: '—' });
}

let ROWS = [];
let MAPS = [];
let Q = '';
let ONLY = '';

export function econTab() {
  const wrap = h('div');
  const status = h('span', { class: 'muted small', id: 'econStatus' });
  const listEl = h('div', { class: 'fishlist', id: 'econTable' });

  async function load() {
    listEl.replaceChildren(h('div', { style: { padding: '18px' } }, loading(5)));
    try {
      const d = await api('/api/econ/species' + (Q ? '?q=' + encodeURIComponent(Q) : ''));
      ROWS = d.items || [];
      MAPS = Array.isArray(d.maps) ? d.maps : [];
      status.textContent = `${ROWS.length} 个鱼种 · ${ROWS.filter((r) => r.overridden).length} 个已覆盖`;
      draw();
    } catch (e) { listEl.replaceChildren(empty('读取失败', errText(e))); }
  }

  function draw() {
    const rows = ONLY === 'overridden' ? ROWS.filter((r) => r.overridden) : ROWS;
    if (!rows.length) {
      listEl.replaceChildren(empty(ROWS.length ? '没有已覆盖的鱼种' : '没有符合条件的鱼种', ROWS.length ? '去掉筛选看全部。' : '换个搜索词。'));
      return;
    }
    listEl.replaceChildren(...rows.map(card));
  }

  // 卡片：上行身份 + 四档阶梯一眼看完（两行数值：经验 / 鱼价），下行是阈值与状态。
  // 不用 12 列表格——那在窄屏上要横滚，而真正要横向对照的只有「四档是不是
  // 按阶梯排列」这一点，做成一排四格反而更容易看出来。
  function card(r) {
    const xpCells = GRADES.map((g) => {
      const gr = (r.grades || []).find((x) => x.grade === g) || {};
      return h('span', { class: 'ecograde' },
        h('em', { text: GRADE_NAMES[g] }),
        h('span', { class: 'mono' }, econCell(gr.xp_c, gr.base_xp)));
    });
    const prCells = GRADES.map((g) => {
      const gr = (r.grades || []).find((x) => x.grade === g) || {};
      return h('span', { class: 'ecograde' },
        h('em', { text: GRADE_NAMES[g] }),
        h('span', { class: 'mono' }, econCell(gr.price_per_kg, gr.base_price)));
    });
    return h('section', { class: 'fishcard' },
      h('div', { class: 'fishcard-head' },
        h('span', { class: 'fi' },
          h('b', { class: 'fname', text: r.name_zh || r.fish_key }),
          h('span', { class: 'fkey', text: r.fish_key })),
        h('span', { class: 'now' },
          h('em', { text: '阈值' }),
          h('span', { class: 'mono', text: `${fmt(r.trophy_g)} / ${fmt(r.trophy_line_g)} / ${fmt(r.super_trophy_line_g)}` }),
          h('span', { class: 'muted small', text: ' g（达标 / 上星 / 蓝冠）' })),
        h('span', { class: 'grow' }),
        r.price_missing ? h('span', { class: 'pill warn', text: '价表缺失' }) : null,
        h('span', { class: r.overridden ? 'pill ok' : 'pill', text: r.overridden ? '已覆盖' : '默认' }),
        h('button', { type: 'button', class: 'edit', text: '编辑', onclick: () => editor(r, load) })),
      h('div', { class: 'fishcard-body eco-grid' },
        h('div', {}, h('em', { text: '经验 XP/克' }), h('div', { class: 'ecogradeline' }, ...xpCells)),
        h('div', {}, h('em', { text: '鱼价 银/公斤（基准水域）' }), h('div', { class: 'ecogradeline' }, ...prCells))),
      h('div', { class: 'muted small', style: { padding: '0 0 12px' }, text: '金色＝已覆盖，灰色＝内置默认。四档应大致呈 1 : 3.27 : 20.4 : 40.8（经验）与 1 : 1.5 : 3 : 6（鱼价），偏离说明这条鱼的覆盖值和阶梯不一致。' }));
  }

  // 列表首次加载：econTab 是同步函数（页签切换要立刻拿到节点），
  // 数据靠 load() 自己去拉，拉完只重画列表容器，不重画整个页签。
  load();

  // 只读「已覆盖」的筛选：253 种里真正改过的通常只有几条，默认全量反而更难找。
  const onlySel = h('select', {
    class: 'select', id: 'econFilter', 'aria-label': '筛选鱼种', style: { width: '160px', flex: '0 0 auto' },
    onchange: (e) => { ONLY = e.target.value; draw(); },
  }, [['', '全部鱼种'], ['overridden', '只看已覆盖']]
    .map(([v, l]) => h('option', { value: v, text: l, selected: v === ONLY })));

  wrap.append(
    h('div', { class: 'row', style: { gap: '8px', margin: '0 0 12px', flexWrap: 'nowrap' } },
      h('div', { style: { flex: '1 1 100%', minWidth: '0' } },
        searchBox('搜索鱼种 key / 中文名，如 bream、欧鳊', debounce((v) => { Q = v; load(); }, 300), Q)),
      onlySel,
      h('span', { class: 'spacer' }),
      status),
    h('p', { class: 'muted small', style: { margin: '0 0 12px', lineHeight: '1.75' } },
      '经验按 ', h('b', { text: 'trunc(速率 × 克重)' }), ' 结算；鱼价在基准价之上再叠当日系数（±5%）与新鲜度（24h 内全额）。',
      h('br'),
      h('b', { text: '四档统一阶梯' }), '：经验 1 : 3.27 : 20.4 : 40.8、鱼价 1 : 1.5 : 3 : 6（都相对未达标）。',
      '这是游戏内结算基准，改档位溢价须走提案并同步测试，不要用逐鱼种覆盖绕过。'),
    listEl);
  return wrap;
}

// ===== 编辑器 =====
function editor(r, reload) {
  const body = h('div');
  const previewEl = h('span', { class: 'muted', id: 'ecoPreview', text: '试算中…' });
  const st = {};
  GRADES.forEach((g) => {
    const gr = (r.grades || []).find((x) => x.grade === g) || {};
    st['xp_' + g] = gr.xp_c == null ? '' : String(gr.xp_c);
    st['pr_' + g] = gr.price_per_kg == null ? '' : String(gr.price_per_kg);
  });
  let saved = false;

  // 试算档位默认选「已经填过覆盖值」的那一档，没有就上星（3）——
  // 上星是最常调的一档，默认落在它上面能省一次选择。
  const filled = GRADES.find((g) => st['xp_' + g] !== '' || st['pr_' + g] !== '');
  let grade = Number(filled || (r.grades || []).find((x) => x.xp_c != null || x.price_per_kg != null)?.grade || 3);
  let weightG = 1000;
  let mapKey = (MAPS[0] && MAPS[0].key) || '';

  const rows = GRADES.map((g) => {
    const gr = (r.grades || []).find((x) => x.grade === g) || {};
    return h('tr', {},
      h('td', {}, h('b', { text: String(g) }), ' ', GRADE_NAMES[g]),
      h('td', { class: 'muted mono' }, econNum(gr.base_xp)),
      h('td', {}, h('input', { class: 'input', id: 'eco_xp_' + g, type: 'text', inputmode: 'decimal',
        placeholder: '默认', value: st['xp_' + g], style: { width: '110px' }, oninput: (e) => { st['xp_' + g] = e.target.value; schedule(); } })),
      h('td', { class: 'muted mono' }, econNum(gr.base_price)),
      h('td', {}, h('input', { class: 'input', id: 'eco_pr_' + g, type: 'text', inputmode: 'decimal',
        placeholder: '默认', value: st['pr_' + g], style: { width: '110px' }, oninput: (e) => { st['pr_' + g] = e.target.value; schedule(); } })));
  });

  const gradeSel = h('select', { class: 'select', id: 'ecoGrade', style: { width: '120px' },
    onchange: (e) => { grade = Number(e.target.value); schedule(); } },
  GRADES.map((g) => h('option', { value: String(g), text: `${g} ${GRADE_NAMES[g]}`, selected: g === grade })));

  const weightInput = h('input', { class: 'input', id: 'ecoW', type: 'text', inputmode: 'decimal', value: String(weightG),
    style: { width: '96px' }, oninput: (e) => { weightG = Number(e.target.value) || 0; schedule(); } });

  const mapSel = h('select', { class: 'select', id: 'ecoMap', style: { maxWidth: '260px' },
    onchange: (e) => { mapKey = e.target.value; schedule(); } },
  (MAPS.length ? MAPS : [{ key: '', name: '未取得水域列表', pct: 1 }])
    .map((m) => h('option', { value: m.key, text: `${m.name}（×${m.pct}）`, selected: m.key === mapKey })));

  let timer = null;
  function schedule() { clearTimeout(timer); timer = setTimeout(runPreview, 220); }

  // 试算：把当日行情系数与算式逐项摊开。
  // 「1kg 卖价 ≠ 每公斤价」永远会被当成 bug 报上来，所以系数、倍率、克重、
  // 新鲜度四项都要写出来，让人自己就能对上。
  async function runPreview() {
    if (!previewEl.isConnected) return;
    const qs = new URLSearchParams({ species: r.fish_key, grade: String(grade), weight_g: String(weightG || 1000) });
    if (mapKey) qs.set('map', mapKey);
    if (st['xp_' + grade].trim() !== '') qs.set('xp_c', st['xp_' + grade].trim());
    if (st['pr_' + grade].trim() !== '') qs.set('price_per_kg', st['pr_' + grade].trim());
    try {
      const d = await api('/api/econ/preview?' + qs.toString());
      if (!previewEl.isConnected) return;
      const mf = Number(d.market_factor) || 1;
      // 接口返回的 price_per_kg / base_price 是基准水域价，要再乘水域倍率才是本图每公斤价。
      const basePerKg = Number(d.price_per_kg) > 0 ? Number(d.price_per_kg) : Number(d.base_price);
      const kg = Number(d.weight_g) / 1000;
      const pct = Number(d.map_pct) || 1;
      const mapPerKg = basePerKg * pct;
      const mp = MAPS.find((m) => m.key === d.map);
      const mapName = mp ? mp.name : (d.map || '基准水域');
      previewEl.classList.remove('muted');
      previewEl.replaceChildren(
        '经验 ', h('b', { text: fmt(d.xp) }), ' · 卖价 ', h('b', { text: Number(d.price_silver).toFixed(2) }), ' 银',
        h('div', { class: 'muted small', style: { marginTop: '4px' } },
          `${fmt(d.weight_g)}g · ${d.grade} 档 ${GRADE_NAMES[d.grade] || ''} · ${mapName} ×${pct} · 刚钓获（新鲜度 1.0）`),
        h('div', { class: 'muted small' }, '算式：基准价 ', basePerKg.toFixed(2),
          ' × 水域倍率 ', String(pct), ' = 本图 ', mapPerKg.toFixed(2), ' 银/kg，再 × 当日行情 ',
          h('b', { text: mf.toFixed(4) }), ' × ', kg, 'kg → ', Number(d.price_silver).toFixed(2), ' 银'),
        h('div', { class: 'muted small' },
          '当日行情取 0.95–1.05，按「UTC 日期 + 鱼种」哈希，同一鱼种当天固定、跨天会变；因此 1kg 通常不正好等于每公斤基准价。'));
    } catch (e) {
      if (previewEl.isConnected) previewEl.replaceChildren('试算失败：' + errText(e));
    }
  }

  body.append(
    h('div', { class: 'callout info' }, h('div', { class: 'grow' },
      h('div', {}, '阈值：达标 ', h('b', { text: fmt(r.trophy_g) }), 'g · 上星 ', h('b', { text: fmt(r.trophy_line_g) }),
        'g · 蓝冠 ', h('b', { text: fmt(r.super_trophy_line_g) }), 'g'),
      h('div', { class: 'muted small', text: '留空＝跟随内置表；只填一列也可以，另一列继续用默认。' }))),

    h('div', { class: 'tbl', style: { marginTop: '12px' } }, h('table', {},
      h('thead', {}, h('tr', {},
        h('th', { text: '档位' }), h('th', { text: '默认 XP/克' }), h('th', { text: '覆盖 XP/克' }),
        h('th', { text: '默认 银/公斤' }), h('th', { text: '覆盖 银/公斤' }))),
      h('tbody', {}, ...rows))),

    h('div', { class: 'row', style: { gap: '10px', marginTop: '14px', flexWrap: 'wrap', borderBottom: '0', padding: '0' } },
      h('label', { class: 'muted small', style: { display: 'flex', alignItems: 'center', gap: '6px' } }, '试算档位 ', gradeSel),
      h('label', { class: 'muted small', style: { display: 'flex', alignItems: 'center', gap: '6px' } }, '重量(g) ', weightInput),
      h('label', { class: 'muted small', style: { display: 'flex', alignItems: 'center', gap: '6px' } }, '水域 ', mapSel)),
    h('div', { style: { marginTop: '8px' } }, previewEl));

  const d = sidePage({
    title: r.name_zh || r.fish_key, sub: r.fish_key, body,
    desc: '按鱼种覆盖经验与鱼价（未填的档位继续用内置值）',
    effect: '保存后主服刷新即生效，最迟 30 秒。',
    foot: [
      r.overridden ? btn('全部恢复默认', async () => {
        if (!(await confirmDialog({ title: '恢复默认', desc: `「${r.name_zh || r.fish_key}」四档的经验与鱼价覆盖将全部删除，回到内置表。`, confirm: '恢复默认', danger: true }))) return;
        try {
          await api('/api/econ/species/reset', { method: 'POST', body: JSON.stringify({ fish_key: r.fish_key }) });
          toast('已恢复默认：' + r.fish_key); saved = true; d.close(); reload();
        } catch (e) { toast(errText(e), 'bad'); }
      }, 'ghost') : null,
      h('span', { class: 'spacer' }),
      btn('保存', async () => {
        const grades = GRADES.map((g) => {
          const xp = st['xp_' + g].trim(), pr = st['pr_' + g].trim();
          return {
            grade: g,
            xp_c: xp === '' ? null : Number(xp),
            price_per_kg: pr === '' ? null : Number(pr),
          };
        });
        const bad = grades.find((g) => (g.xp_c != null && !(g.xp_c > 0)) || (g.price_per_kg != null && !(g.price_per_kg > 0)));
        if (bad) return toast(`${bad.grade} 档的覆盖值必须大于 0，或留空表示默认`, 'bad');
        const nan = grades.find((g) => Number.isNaN(g.xp_c) || Number.isNaN(g.price_per_kg));
        if (nan) return toast(`${nan.grade} 档填的不是有效数字`, 'bad');
        try {
          await api('/api/econ/species/save', { method: 'POST', body: JSON.stringify({ fish_key: r.fish_key, grades }) });
          toast('已保存：' + r.fish_key + '，主服刷新后生效');
          saved = true; d.close(); reload();
        } catch (e) { toast('保存失败：' + errText(e), 'bad'); }
      }, 'primary'),
    ],
    isDirty: () => !saved && GRADES.some((g) => {
      const gr = (r.grades || []).find((x) => x.grade === g) || {};
      return st['xp_' + g] !== (gr.xp_c == null ? '' : String(gr.xp_c))
        || st['pr_' + g] !== (gr.price_per_kg == null ? '' : String(gr.price_per_kg));
    }),
  });
  runPreview();
}

// 供其它页签复用：把一条鱼的四档压成一行文字（权重页签的「攻略原值均值」用）。
export const gradeLine = (grades) => (grades || []).map((g) => econNum(g.base_xp)).join(' / ');
export { econNum, GRADES, GRADE_NAMES };
export const econMeta = () => ({ rows: ROWS.length, maps: MAPS.length });