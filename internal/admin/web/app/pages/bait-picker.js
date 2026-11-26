// 鱼饵层级选择器：类型 → 二级类型 → 形态 → 品牌 → 型号 → 规格。
//
// 从旧后台 index.html 的 renderBrPicker() 移植（用户 2026-11-25 定的交互）。
// 层级越深越窄，每一层都能单独「勾选」待加入，最后一次写进规则。
//
// 三层数据的来源必须分开说清楚，否则很容易写成「后台能选、判鱼不认」：
//   1) 类型层写的是**整类词**（走 baitTypeTerms，服务端按枚举匹配）——稳。
//   2) 品牌层**不能单独写进规则**：品牌名（zeiman/syberia）不出现在任何饵的
//      商品名里，判鱼是按名称匹配的，写了也匹配不到。所以它只作导航，
//      不提供任何「加入」按钮。测试里专门断言 `allow` 不含品牌名。
//   3) 型号层写的是**去掉规格号的型号名**（`Spiker #2`）。判鱼侧 matchOne 会先比
//      baitBaseName(饵名)==规则词，因此该型号的全部规格一起命中。
//   4) 规格层写的是**单个完整商品名**，只命中那一款。
//
// 跨类别同名是这一版的重点：`鱼子` 同时存在于 自然的 / 香煮成饵 / 颗粒 三处
// （真实库里 25 个型号名横跨 ≥2 个二级类别）。写进规则的词必须带类别限定
// （`颗粒@鱼子`），否则一个 `鱼子` 会把另外几个类别下的同名饵一起放行。
// 这个判断由后端 scope_term 给出，前端不自己猜。
import { h, api, toast, icon, sidePage } from '../core.js';

// 分类树缓存：整个会话只拉一次。它是静态数据（从客户端饵料表编译来的），
// 不会因为改规则而变化，重复拉只会拖慢每次打开选择器。
let CATS = null;
let TERMS = null;
let TERM_MAP = null;

export function loadCats(force = false) {
  if (CATS && !force) return Promise.resolve({ cats: CATS, terms: TERMS });
  return api('/api/bait/categories').then((d) => {
    CATS = d.categories || [];
    TERMS = d.terms || [];
    // term -> label：用来判断某个标签是不是「整类词」，好染成另一种颜色。
    TERM_MAP = Object.fromEntries(TERMS.map((t) => [t.term, t.label]));
    return { cats: CATS, terms: TERMS };
  }).catch(() => {
    CATS = []; TERMS = []; TERM_MAP = {};
    return { cats: CATS, terms: TERMS };
  });
}

export const isCategoryTerm = (term) => !!(TERM_MAP && TERM_MAP[term]);

// 型号 → 写进规则的词。有 scope_term 必须用它：那是后端算好的「本类别内唯一写法」。
const lineToTerm = (ln) => ln.scope_term || ln.term || ln.base || '';

// 把 `颗粒@鱼子` 拆成两段，供标签显示成「颗粒 › 鱼子」。
// 不拆的话两个同名标签看起来一模一样，用户以为自己重复添加了两次。
export function splitScope(term) {
  const s = String(term || ''), at = s.indexOf('@');
  if (at <= 0 || at >= s.length - 1) return null;
  return { scope: s.slice(0, at), name: s.slice(at + 1) };
}

// 已选标签：三种视觉必须能分开，否则用户分不清自己加的是哪一个。
//   1) 整类词（`颗粒`）——紫色，说明「含该类全部饵」；
//   2) 类别限定（`颗粒@鱼子`）——蓝色，拆成「限定词 › 名字」；
//   3) 普通饵名 / 型号名——默认色。
// 最后那个 × 是删除按钮，测试取标签文本时会把它剥掉。
export function termChip(term, onRemove) {
  const scope = splitScope(term);
  const cat = isCategoryTerm(term);
  const title = cat
    ? `整类：${TERM_MAP[term]}（含该类全部饵）`
    : (scope ? `只限「${scope.scope}」这一类里的「${scope.name}」（不影响其它类别下的同名饵）` : '');
  const body = scope
    ? [h('span', { class: 'sc', text: scope.scope }), h('span', { class: 'sep', text: '›' }), scope.name]
    : term;
  return h('span', { class: 'chip' + (cat ? ' wild' : '') + (scope ? ' scope' : ''), title },
    h('span', {}, body),
    h('button', { type: 'button', 'aria-label': '移除 ' + term, onclick: onRemove }, icon('x', 11)));
}

// 打开选择器。used 是目标列表里已有的词（用于显示「已在列表」并禁用重复加入）。
// 返回 Promise<选中的词数组|null>：确认后由调用方一次性写入目标列表，取消返回 null。
//
// 交互与旧后台一致：点圆点是「收窄范围」，点 ＋ 是「勾选待加入」。
// 勾选不关窗，可以跨层累积，最后「一次性加入」——用户 2026-11-25 明确要求，
// 以前每点一项就关窗、加 5 个饵要开关 5 次。
export function openBaitPicker({ title, used = [] }) {
  return new Promise((resolve) => {
    let settled = false;
    const finish = (v) => { if (settled) return; settled = true; resolve(v); panel.close(); };
    const mount = h('div', { class: 'stack' });
    const panel = sidePage({
      title: title || '选择鱼饵',
      sub: '按游戏里的层级往下选：类型 › 品牌 › 型号 › 规格。点圆点是「收窄范围」，点 ＋ 是「勾选待加入」——可以一次勾多个再统一加入。',
      nested: true, body: mount,
    });
    buildPicker({ used, mount, finish });
  });
}

function buildPicker({ used, mount, finish }) {
  const state = { cat: 0, sub: 0, shape: '', kind: '', brand: '', base: '', q: '', picked: [] };
  const inList = (t) => used.includes(t);
  const isPicked = (t) => state.picked.includes(t);

  const node = () => (CATS || []).find((c) => c.key === state.cat) || null;
  const child = () => {
    const n = node();
    return n && state.sub ? ((n.children || []).find((c) => c.sub === state.sub) || null) : null;
  };
  const curLine = () => {
    const ch = child();
    if (!state.base || !ch) return null;
    return (ch.brands || []).flatMap((b) => b.lines || []).find((l) => (l.base || '') === state.base) || null;
  };

  function patch(p) {
    // 任意一层变动都要把更深的层清空，否则会留下矛盾的组合
    // （比如切了类别但 base 还留着上一类的型号名）。
    if ('cat' in p) { state.cat = p.cat; state.sub = 0; state.shape = ''; state.kind = ''; state.brand = ''; state.base = ''; }
    if ('sub' in p) { state.sub = p.sub; state.shape = ''; state.kind = ''; state.brand = ''; state.base = ''; }
    if ('shape' in p) { state.shape = p.shape; state.kind = ''; state.brand = ''; state.base = ''; }
    if ('kind' in p) { state.kind = p.kind; state.brand = ''; state.base = ''; }
    if ('brand' in p) { state.brand = p.brand; state.base = ''; }
    if ('base' in p) { state.base = p.base; }
    draw();
  }

  function toggle(term) {
    if (!term) return;
    const i = state.picked.indexOf(term);
    if (i >= 0) state.picked.splice(i, 1); else state.picked.push(term);
    draw();   // 重绘整屏以更新所有层的勾选态与底部计数
  }

  // 一个层按钮：「点圆点=收窄范围，点 ＋=勾选待加入」。
  // 两者必须是不同的可点区域，否则用户永远不知道该点哪。
  function option({ label, count, on, term, onPick, dim, badge, tip }) {
    const st = inList(term) ? 'in' : (isPicked(term) ? 'sel' : '');
    const mark = st === 'in' ? '已在列表' : (st === 'sel' ? '✓ 已选' : '');
    return h('button', {
      type: 'button', class: `br-opt${on ? ' on' : ''}${st === 'sel' ? ' sel' : ''}${st === 'in' ? ' in' : ''}${dim ? ' dim' : ''}`,
      title: tip || '', onclick: onPick,
    },
    term ? h('span', {
      class: 'tick', title: '勾选待加入：写进规则的是「' + term + '」',
      onclick: (e) => { e.stopPropagation(); toggle(term); },
    }, st === 'in' || st === 'sel' ? '✓' : '＋') : null,
    label,
    typeof count === 'number' ? h('span', { class: 'n', text: String(count) }) : null,
    badge || null,
    mark ? h('span', { class: 'n ' + (st === 'in' ? 'ok' : 'on'), text: mark }) : null);
  }

  function draw() {
    const ch = child(), nd = node(), line = curLine();

    // --- 第 1 层：类型（全部 / 鱼饵 / 拟饵）---
    const catBtns = [{ key: 0, label: '全部' }, { key: 27, label: '鱼饵' }, { key: 17, label: '拟饵' }].map((c) => {
      const n0 = (CATS || []).find((x) => x.key === c.key);
      const n = c.key === 0 ? (CATS || []).reduce((s, x) => s + (x.count || 0), 0) : ((n0 && n0.count) || 0);
      return option({ label: c.label, count: n, on: state.cat === c.key, onPick: () => patch({ cat: c.key }) });
    });

    // --- 第 1.5 层：该大类下的二级类型（游戏菜单里「旋转亮片路亚」这一档）---
    // 整类词也能勾选：勾了以后和型号名一起批量加入，省得来回进出。
    const typeBtns = (state.cat && nd) ? (nd.children || []).filter((c) => c.count > 0).map((c) => option({
      label: c.label, count: c.count, on: state.sub === c.sub && !state.shape, term: c.term || '',
      onPick: () => patch({ sub: c.sub }), tip: '点这里＝把范围收到这一类型；勾选框可把它整类加入规则',
    })) : [];

    // --- 形态层（只有香煮成饵有：下沉 / Pop-up）---
    let shapeLayer = null;
    if (ch && (ch.shapes || []).length) {
      shapeLayer = layer('形态', [
        option({ label: '两种都要', count: ch.count, on: state.shape === '', onPick: () => patch({ shape: '' }) }),
        ...(ch.shapes || []).map((s) => option({
          label: s.label, count: s.count || 0, on: state.shape === s.shape, term: s.term,
          onPick: () => patch({ shape: s.shape }), tip: `点这里＝只看${s.label}；勾选框＝把「${s.term}」整类加入`,
        })),
      ]);
    }

    // --- 第 2 层：品牌（只作导航，不能单独入规则，所以不给 term）---
    // 只有一个品牌时这一层没有信息量，藏掉。
    let brandLayer = null;
    if (ch && (ch.brands || []).length > 1) {
      brandLayer = layer('品牌', [
        option({ label: '全部', count: ch.count, on: state.kind === '', onPick: () => patch({ kind: '' }) }),
        ...(ch.brands || []).map((b) => option({
          label: b.label || b.kind || '无品牌', count: b.count, on: state.kind === b.kind,
          onPick: () => patch({ kind: b.kind }),
        })),
      ]);
    }

    // --- 第 3 层：型号（写型号名，覆盖该型号全部规格）---
    let modelLayer = null;
    const brandList = ch ? (ch.brands || []).filter((b) => !state.kind || b.kind === state.kind) : [];
    const lines = [];
    brandList.forEach((b) => (b.lines || []).forEach((ln) => lines.push({
      base: ln.base, count: ln.count, term: lineToTerm(ln), unused: ln.unused, shared: ln.shared_with || [],
    })));
    // 这一层只在「没有信息量」时才隐藏：只有一个型号且已经选中它。
    // 但跨类别同名的型号例外——那种情况下这一行是唯一能看到「同名×N」与
    // 「限定写法」提示的地方，藏掉就等于什么都没说（`颗粒` 下只有 `鱼子` 一个型号时踩过）。
    const onlyOne = state.base && lines.length === 1 && lines[0].base === state.base;
    const hideModel = onlyOne && !(lines[0].shared && lines[0].shared.length);
    if (lines.length && !hideModel) {
      const cols = lines.slice(0, 120).map((x) => option({
        label: x.base, count: x.count, on: state.base === x.base, term: x.term, dim: x.unused,
        onPick: () => patch({ base: x.base }),
        tip: `写进规则：「${x.term}」——该型号全部 ${x.count} 个规格一起命中`
          + (x.shared.length ? `。同名型号还在：${x.shared.join('、')}——所以写进规则时会带上类别限定` : ''),
        badge: x.shared.length ? h('span', { class: 'badge', title: `同名型号还在：${x.shared.join('、')}`, text: `同名×${1 + x.shared.length}` }) : null,
      }));
      if (lines.length > 120) cols.push(h('span', { class: 'muted small', style: { alignSelf: 'center' }, text: `还有 ${lines.length - 120} 个，请用搜索` }));
      modelLayer = layer('型号', [
        option({ label: '全部', count: lines.reduce((s, x) => s + x.count, 0), on: state.base === '', onPick: () => patch({ base: '' }) }),
        ...cols,
      ]);
    }

    // --- 加入按钮组：明确「这一下会写进去什么词」---
    const addBtns = [];
    const pushAdd = (term, label, extra) => {
      if (!term) return;
      const inIt = inList(term), sel = isPicked(term);
      addBtns.push(h('button', {
        type: 'button', class: 'br-opt add' + (sel ? ' sel' : ''), disabled: inIt,
        title: '写进规则：' + term, onclick: () => toggle(term),
      }, `${inIt ? '✓ 已在列表' : (sel ? '✓ 已选' : '＋')} ${label}${extra ? `（${extra}）` : ''}`));
    };
    if (state.base && line) {
      const term = lineToTerm(line);
      pushAdd(term, `加入型号「${line.base || state.base}」${(line.shared_with || []).length ? '（限本类）' : ''}`, line.count ? line.count + ' 个规格' : '');
    } else if (state.sub) {
      // 未选型号：允许把整个二级类型加进去（写整类词）；有形态时按形态
      const st = state.shape ? (ch.shapes || []).find((s) => s.shape === state.shape) : null;
      if (st) pushAdd(st.term, `加入整类「${st.label}」`, st.count ? st.count + ' 款' : '');
      else if (ch) pushAdd(ch.term, `加入整类「${ch.label}」`, ch.count ? ch.count + ' 款' : '');
    } else if (state.cat && nd) {
      pushAdd(nd.term, `加入整类「${nd.label}」`, nd.count ? nd.count + ' 款' : '');
    }

    // --- 收束路径与「会写进哪个词」的预览 ---
    // 当前收束范围会写进规则的到底是哪个词：型号名 > 形态词 > 整类词（二级）> 整类词（大类）。
    // 单独算出来而不是塞进模板字符串，避免嵌套三元把括号写乱（旧后台在这里出过语法错）。
    const trail = [];
    if (state.cat && nd) trail.push(nd.label);
    if (ch) trail.push(ch.label);
    if (state.shape) trail.push(state.shape === 'popup' ? 'Pop-up' : '下沉');
    if (state.kind) trail.push(state.kind);
    if (state.base) trail.push(state.base);
    const rangeText = trail.filter(Boolean).join(' › ') || '全部饵料 / 拟饵';
    let ruleTerm = '—';
    if (line) ruleTerm = lineToTerm(line);
    else if (state.shape && ch) {
      const st = (ch.shapes || []).find((s) => s.shape === state.shape);
      if (st) ruleTerm = st.term;
    } else if (ch) ruleTerm = ch.term || '—';
    else if (nd) ruleTerm = nd.term || '—';
    const sharedNote = (line && (line.shared_with || []).length)
      ? h('span', { class: 'warn-text' }, `同名型号还出现在：${line.shared_with.join('、')}。所以这里写进规则的是限定写法 `, h('code', { text: ruleTerm }), '，只影响本类别。')
      : null;
    const preview = h('div', { class: 'br-preview' },
      '当前位置：', h('b', { text: rangeText }),
      h('br'), '加入后会写进规则的是：', h('code', { text: ruleTerm }),
      h('br'), h('span', { class: 'muted', text: '层级越深，收束越窄。写「型号名」= 该型号全部规格；写整类词 = 该类型全部饵。' }),
      sharedNote ? h('br') : null, sharedNote);

    // --- 底部批量工具条 ---
    const pickedN = state.picked.length;
    const bar = h('div', { class: 'br-pickbar' },
      h('div', { class: 'info' }, '已勾选 ', h('b', { text: String(pickedN) }), ' 项',
        pickedN ? ['：', h('span', { class: 'chips' }, ...state.picked.map((v) => h('span', { class: 'tag' }, v,
          h('button', { type: 'button', class: 'rm', 'aria-label': '取消勾选 ' + v, onclick: () => toggle(v) }, icon('x', 10)))))] : null),
      h('div', { class: 'acts' },
        h('button', { type: 'button', class: 'btn good', disabled: !pickedN, onclick: commit }, `一次性加入 ${pickedN || ''} 项`),
        h('button', { type: 'button', class: 'btn alt', disabled: !pickedN, onclick: () => { state.picked = []; draw(); } }, '清空勾选'),
        h('button', { type: 'button', class: 'btn alt', onclick: () => finish(null) }, '取消')));

    const specBox = h('div', { id: 'brSpecBox' });
    mount.replaceChildren(
      layer('类型', catBtns),
      typeBtns.length ? layer('', typeBtns) : document.createTextNode(''),
      shapeLayer || document.createTextNode(''),
      brandLayer || document.createTextNode(''),
      modelLayer || document.createTextNode(''),
      layer('加入', addBtns.length ? addBtns : [h('span', { class: 'muted small', style: { alignSelf: 'center' }, text: '先选类型，再逐级收束' })]),
      h('div', { class: 'field', style: { marginTop: '10px' } },
        h('input', { id: 'brPickQ', placeholder: '搜索名称 / ID（会在当前收束范围内过滤）', value: state.q,
          oninput: (e) => { state.q = e.target.value; scheduleSearch(); } })),
      preview, specBox, bar);
    reloadSpecs();
  }

  // 规格列表：只在选了型号时出现，列出该型号下每个具体商品。
  // 型号名写进规则就覆盖全部规格，所以这里是「想只开某几个颜色」时才用。
  let seq = 0;
  function scheduleSearch() {
    const my = ++seq;
    setTimeout(() => { if (my === seq) reloadSpecs(); }, 250);
  }
  function reloadSpecs() {
    const box = document.getElementById('brSpecBox');
    if (!box) return;
    if (!state.base) { box.replaceChildren(); return; }
    const qs = '?cat=' + (state.cat || 0) + '&sub=' + (state.sub || 0) + '&base=' + encodeURIComponent(state.base)
      + (state.kind ? '&kind=' + encodeURIComponent(state.kind) : '')
      + (state.q ? '&q=' + encodeURIComponent(state.q) : '') + '&limit=200';
    const my = ++seq;
    box.replaceChildren(h('div', { class: 'muted small', style: { marginTop: '8px' }, text: '读取规格…' }));
    api('/api/bait/items' + qs).then((d) => {
      if (my !== seq) return;   // 期间换了型号/改了搜索词，丢弃这次结果
      const rows = (d.rows || []).filter((x) => x.base === state.base);
      if (!rows.length) { box.replaceChildren(h('div', { class: 'muted small', style: { marginTop: '8px' }, text: '该型号下没有匹配的规格' })); return; }
      // 用 matched（过滤后全量命中）而不是 rows.length：rows 会被 limit 截断，
      // 用 rows.length 会在规格超过上限时把「共 260 个」写成「共 200 个」。
      const cnt = d.matched || rows.length;
      const names = rows.map((x) => x.name_zh || x.item_id);
      const addable = names.filter((n) => !inList(n)).length;
      box.replaceChildren(
        h('div', { class: 'br-spec-head' },
          h('span', {}, '该型号共 ', h('b', { text: String(cnt) }), ' 个规格 —— 只加入型号名就已全部覆盖；下面用于只挑其中几个。',
            addable ? [' ', h('a', { href: 'javascript:void(0)', class: 'lnk', onclick: () => toggleMany(names) }, `全选本页 ${names.length} 个`)] : null)),
        h('div', { class: 'br-spec-list' }, ...rows.map((x) => {
          const nm = x.name_zh || x.item_id;
          const st = inList(nm) ? 'picked' : (isPicked(nm) ? 'sel' : '');
          return h('div', { class: 'br-spec ' + st, onclick: () => toggle(nm) },
            h('div', {}, h('b', { text: nm }), h('div', { class: 'id', text: x.item_id })),
            h('span', { class: 'st', text: inList(nm) ? '✓ 已在列表' : (isPicked(nm) ? '✓ 已选' : '＋ 勾选') }));
        })));
    }).catch((e) => {
      if (my === seq) box.replaceChildren(h('div', { class: 'muted small', style: { marginTop: '8px' }, text: '读取失败：' + (e.message || e) }));
    });
  }

  function toggleMany(names) {
    let added = 0;
    names.forEach((n) => {
      if (!n || inList(n)) return;
      if (!state.picked.includes(n)) { state.picked.push(n); added++; }
    });
    draw();
    toast(added ? `已勾选 ${added} 个规格` : '这些都已经勾选或已在列表里', added ? '' : 'bad');
  }

  function commit() {
    const names = state.picked.filter(Boolean);
    if (!names.length) { toast('还没有勾选任何项', 'bad'); return; }
    finish(names);
  }

  draw();   // 首次渲染：不调这一下就只有一个空面板（踩过）
  // 分类树是页面打开时预取的，但用户可能点得太快。这时先画空壳，
  // 等树到位再重画一遍——不这样会留下一个「只有类型层、点了没反应」的面板。
  if (!CATS) loadCats().then(() => { if (mount.isConnected) draw(); });
  return { draw, commit, state };
}

// 一层：左边窄标签（类型/形态/品牌…），右边是按钮流。
// 二级类型那一层的标签是空串，但 DOM 结构必须保持一致，测试按 `.k` 取层名。
function layer(k, children) {
  return h('div', { class: 'br-layer' }, h('div', { class: 'k', text: k }), h('div', { class: 'v' }, ...children));
}