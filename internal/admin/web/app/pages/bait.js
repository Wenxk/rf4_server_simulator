// 鱼饵偏好：每种鱼吃什么、不吃什么，以及自动抽取的本期开口饵。
//
// 从旧后台的 #baitTable 移植。2026-11-24 用户要求删掉「偏好口（按类）」
// 「饵效证据」「吃不吃」三列——前两者只在旧版节奏打分与护鱼网里间接生效、
// 后台无需维护，后者是纯展示结论；水域与水层也搬到生态基线（那条鱼本身的
// 习性，与用什么饵无关）。因此这里只剩判鱼真正用到的四列：
// 鱼种 / 自动抽取 / 本期开口 / 规则。
import { h, api, toast, errText, pageHead, work, ib, hint, bullets, pill, callout, btn, empty, loading, searchBox, debounce, sidePage, confirmDialog, segmented, toggleEl, icon, fmt } from '../core.js';
import { loadCats, openBaitPicker, termChip } from './bait-picker.js';

const WILDCARD = [['各类饵', '所有饵料'], ['拟饵', '所有拟饵'], ['活饵类', '所有活饵'], ['饵鱼类', '所有饵鱼']];
let Q = '', LOC = '', FILTER = 'all';
const PAGE = 30;
let SHOWN = PAGE;

const put = (path, body) => api(path, { method: 'PUT', body: JSON.stringify(body) });

// 前置条件预设。v 必须与 fishing_bait_rules.txt 里的原文逐字一致：
// 服务端 deriveConditionFlags 是按关键词解析的（「海图…号区域」「顺水漂流」
// 「jig阶梯」「饵在水中运动」「已上船」），改措辞=条件直接失效。
// eff 说明服务端到底管不管：只有打「生效」的那几条会真的拦鱼。
const COND_TYPES = [
  { t: 'none', label: '不限制（留空）', v: '', hint: '任何水域、任何钓法都可能出这条鱼。' },
  { t: 'chart', label: '海图 · 指定区号（留空=任意海图区域）', v: '', hint: '生效。只有抛竿落点在这几个海图区域时才判这条鱼；区号与客户端水域编号一致，多个用 / 分隔（如 30/34/41/55）。' },
  { t: 'jig', label: '拟饵 · 须出现「jig 阶梯」', v: '使用拟饵时须出现“jig阶梯”', hint: '生效，但只对拟饵。没上报 jig 阶梯动作时不判这条鱼；动作证据缺失时放行（保持中性）。' },
  { t: 'lure_boat', label: '拟饵 · 路亚 + 有效船票 + 已上船 + 饵在水中运动', v: '使用拟饵时须采用路亚钓法，使用有效船票并已上船，饵在水中运动', hint: '生效，但只对拟饵。要求船票+已在船上，且饵在水中运动。' },
  { t: 'lure_bare', label: '路亚 · 有效船票 + 已上船 + 饵在水中运动', v: '路亚钓法，使用有效船票并已上船，饵在水中运动', hint: '生效。同上但不带「使用拟饵时」前缀，天然饵也按这条判。' },
  { t: 'jig_or_motion', label: '拟饵 · jig 阶梯 或「路亚+上船+运动」二选一', v: '使用拟饵时须出现“jig阶梯”，或采用路亚钓法且使用有效船票并已上船，饵在水中运动', hint: '生效，二选一：出现 jig 阶梯即通过；否则要同时满足路亚、上船、饵在水中运动。' },
  { t: 'float_drift', label: '浮漂 · 立漂 / 顺水漂流（只有「顺水漂流」生效）', v: '浮漂钓法；手竿或博洛尼亚竿须浮漂立起，其他浮漂竿须出现“顺水漂流”', hint: '半生效：服务端只认「顺水漂流」，且只在拟饵时才校验；「浮漂立起」「手竿/博洛尼亚竿」没有对应上报，不参与判定。' },
  { t: 'tele_only', label: '仅手竿 / 博洛尼亚竿 + 立漂（仅备注，不参与判定）', v: '仅手竿或博洛尼亚竿，浮漂立起', hint: '仅备注。服务端拿不到「竿型」「浮漂立起」证据，这条条件不会拦任何鱼，写不写结果一样。' },
  { t: 'custom', label: '自定义…（高级，按关键词解析）', v: '', hint: '按关键词解析：含「海图」+数字=限区号；含「顺水漂流」「jig阶梯」「饵在水中运动」「已上船」各点亮一项。写错关键词等于没写。' },
];

// 把库里存的条件文本反解成「下拉类型 + 区号/自定义原文」。
function condParse(v) {
  const s = String(v || '').trim();
  if (!s || s === '无') return { t: 'none', zones: '', raw: '' };
  const hit = COND_TYPES.find((x) => x.t !== 'none' && x.t !== 'chart' && x.t !== 'custom' && x.v === s);
  if (hit) return { t: hit.t, zones: '', raw: '' };
  if (/^海图[\d或/、,，\s]*号区域$/.test(s)) return { t: 'chart', zones: (s.match(/\d+/g) || []).join('/'), raw: '' };
  return { t: 'custom', zones: '', raw: s };
}
// 反解回服务端认的原句。区号按「海图30或34号区域」的固定措辞拼，
// 中间用「或」而不是斜杠——斜杠那是输入时的分隔习惯，落库用「或」。
function condCompose(type, zones, raw) {
  if (type === 'none') return '';
  if (type === 'custom') return raw.trim();
  if (type === 'chart') {
    const ns = String(zones).split(/[/、,，\s或]+/).map((s) => s.trim()).filter(Boolean);
    return ns.length ? `海图${ns.join('或')}号区域` : '';
  }
  const hit = COND_TYPES.find((x) => x.t === type);
  return hit ? hit.v : '';
}

// 一句话概括这条鱼现在的规则（卡片右上「现在 吃 …」那一行）
function nowLine(r) {
  if (!r.has_rule) return '没有设置，按实际钓获数据判断';
  const bits = [];
  if ((r.allow || []).length) bits.push('吃 ' + r.allow.slice(0, 2).join('、') + (r.allow.length > 2 ? ` 等 ${r.allow.length} 种` : ''));
  if ((r.bottom_only || []).length) bits.push('底钓另吃 ' + r.bottom_only.slice(0, 2).join('、'));
  if ((r.deny || []).length) bits.push('不吃 ' + r.deny.slice(0, 2).join('、'));
  if (r.auto_rotation) bits.push('自动抽取');
  if (r.condition && r.condition !== '无') bits.push(r.condition);
  if (r.source === 'override' && r.enabled === false) bits.push('已停用');
  const line = bits.slice(0, 3).join('；');
  return line ? (line.length > 64 ? line.slice(0, 62) + '…' : line) : '已启用，但没有填具体的饵';
}

// 「本期开口」格：开了自动抽取才有内容。
// 三种状态都要能区分，否则管理员看不出「没抽」和「抽了但没生效」：
//   总开关未开 → 抽签照算但不生效，必须提醒；
//   型号不足配额 → 本期全开，不假装抽过；
//   正常抽中 → 配额/池子 + 具体饵名 + 下次切换时刻。
function autoPickCell(r) {
  if (!r.auto_rotation) return h('span', { class: 'muted', text: '—' });
  const rot = r.rotation || {};
  if (rot.global_off) {
    return h('div', {},
      h('div', { class: 'small', style: { color: 'var(--warn)' }, text: '⚠ 总开关未开，抽签不生效' }),
      h('div', { class: 'muted small', style: { marginTop: '2px' }, text: '到「游戏参数 › 玩法与钓组」打开「自动轮换开口饵」' }));
  }
  if (!rot.enabled) {
    return h('span', { class: 'muted small', text: `型号只有 ${rot.pool_size || 0} 种，不足配额 ${rot.quota || 0}，本期全开` });
  }
  const next = rot.next_switch ? new Date(rot.next_switch * 1000).toLocaleTimeString('zh-CN') : '—';
  // 配额/池子：抽的是「型号 × 形态」组，一组里的所有尺寸/颜色都会放行。
  return h('div', {},
    h('div', {}, pill(`🎲 本期开口 ${rot.quota}/${rot.pool_size}`, 'accent')),
    h('div', { class: 'small', style: { fontWeight: '600', marginTop: '3px', lineHeight: '1.5' }, text: (rot.active_baits || []).join(' / ') || '—' }),
    h('div', { class: 'muted small', text: `下次 ${next} · 同型号各规格通用` }));
}

// 「规则」格：吃 / 不吃 / 底钓只吃 / 前置条件，可折叠。
function ruleCell(r) {
  const bits = [];
  if (r.has_rule) {
    bits.push(h('div', { class: 'small' }, '吃：' + ((r.allow || []).join(' / ') || '—')));
    if ((r.deny || []).length) bits.push(h('div', { class: 'muted small' }, '不吃：' + r.deny.join(' / ')));
    if ((r.bottom_only || []).length) bits.push(h('div', { class: 'muted small' }, '底钓只吃：' + r.bottom_only.join(' / ')));
    if (r.condition && r.condition !== '无') bits.push(h('div', { class: 'muted small' }, '前置：' + r.condition));
  } else {
    bits.push(h('div', { class: 'muted small', text: '无规则条目 · 严格模式下该鱼不开口' }));
  }
  const tag = r.source === 'override'
    ? (r.enabled ? pill('已覆盖', 'ok') : pill('覆盖已停用'))
    : pill('内置');
  bits.push(h('div', { style: { marginTop: '4px' } }, tag));
  return h('div', { class: 'rulecell' }, ...bits);
}

export default async function bait() {
  const areas = await api('/api/fish/areas').catch(() => []);
  loadCats();   // 预取分类树：编辑器里「整类」标签要染色，等打开再拉会闪一下
  // id 沿用旧后台的 #baitTable：验收测试按它定位列表，换名等于把测试改瞎。
  // 形态是卡片而非表格（2026-11-26 起与生态页统一），但锚点保持一致。
  const listWrap = h('div', { class: 'fishlist', id: 'baitTable' });
  const count = h('span', { class: 'hint', style: { margin: '0 0 0 6px' } });
  const banner = h('div');
  let rows = [];
  let rulesOn = true;

  async function load() {
    listWrap.replaceChildren(h('div', { style: { padding: '18px' } }, loading(4)));
    try {
      const d = await api('/api/bait/prefs?limit=2000&q=' + encodeURIComponent(Q) + '&loc=' + encodeURIComponent(LOC));
      rows = d.rows || [];
      rulesOn = d.rules_enabled !== false;
      banner.replaceChildren(rulesOn ? '' : callout('warn',
        h('span', {}, '「鱼会挑饵」开关当前是关闭的：这里保存的规则不影响判鱼。', h('a', { href: '#rules', text: '去打开' }))));
      draw();
    } catch (e) { listWrap.replaceChildren(empty('读取失败', errText(e))); }
  }

  // 逐鱼切换自动抽取。用 fish_key 而不是中文名：开关表以 fish_key 为主键，
  // 不受「泥鳅 / 泥鳅鱼」这类按水域改名以及 BOM 脏字符影响。
  async function toggleAuto(key, on, box) {
    box.disabled = true;
    try {
      await api('/api/bait/auto-rotation', { method: 'POST', body: JSON.stringify({ fish_key: key, enabled: on }) });
      toast(on ? '已开启自动抽取（需总开关也打开）' : '已关闭自动抽取');
      await load();
    } catch (e) {
      box.checked = !on; box.disabled = false;
      toast('切换失败：' + errText(e), 'bad');
    }
  }

  // 立即重抽本期开口：给所有开了「自动抽取」的鱼换一批开口饵。
  // 服务端只是把轮次偏移 +1，周期与下次自然切换时刻都不变。
  async function reroll() {
    if (!(await confirmDialog({
      title: '立即重抽本期开口',
      desc: '会给所有开了「自动抽取」的鱼重新抽一批开口饵，游戏内判鱼同步生效。轮换周期和下次自然切换时刻都不变。',
      confirm: '确认执行',
    }))) return;
    try {
      const d = await api('/api/bait/auto-rotation/reroll', { method: 'POST', body: '{}' });
      await load();
      toast('已重抽，当前是第 ' + d.round + ' 轮');
    } catch (e) { toast('重抽失败：' + errText(e), 'bad'); }
  }

  function draw() {
    const list = rows.filter((r) => FILTER === 'all' || (FILTER === 'tier3' && r.tier3 > 0) || (FILTER === 'rule' && r.has_rule) || (FILTER === 'none' && !r.has_rule) || (FILTER === 'edited' && r.source === 'override'));
    const page = list.slice(0, SHOWN);
    count.textContent = list.length > page.length ? `${list.length} 种鱼 · 先显示 ${page.length}` : `${list.length} 种鱼`;
    const more = list.length > page.length
      ? h('div', { style: { display: 'flex', justifyContent: 'center', padding: '14px 0 4px' } },
        btn(`再显示 ${Math.min(PAGE, list.length - page.length)} 种（还有 ${list.length - page.length} 种）`, () => { SHOWN += PAGE; draw(); }, 'ghost'))
      : null;
    // 注意：replaceChildren 是原生 DOM API，传 null 会渲染出字面量 "null"。
    // 只有 h() 内部才会跳过 null，所以这里必须自己过滤，不能直接把 more 递进去。
    const cards = page.length ? page.map((r) => {
      const locs = (r.locs || []).slice(0, 2).join(' · ');
      // 自动抽取开关：勾选=参与抽签；还要总开关打开才生效，两处是「与」的关系。
      const auto = h('input', { type: 'checkbox', checked: !!r.auto_rotation,
        title: '勾选=参与自动抽取；还需在「游戏参数 › 玩法与钓组」打开「自动轮换开口饵」总开关',
        onchange: (e) => toggleAuto(r.fish_key, e.target.checked, e.target) });
      return h('section', { class: 'fishcard' },
        h('div', { class: 'fishcard-head' },
          h('span', { class: 'fi' }, h('b', { class: 'fname', text: r.name_zh }), h('span', { class: 'fkey', text: r.fish_key }), locs ? h('span', { class: 'fkey', text: locs }) : null),
          h('span', { class: 'now' }, h('em', { text: '现在' }), h('span', { text: nowLine(r) })),
          h('label', { class: 'autobox', title: '自动抽取本期开口饵' }, auto, h('span', { text: '自动抽取' })),
          h('button', { type: 'button', class: 'edit', text: '展开修改', onclick: () => editFish(r, load) })),
        // 「本期开口」与「规则」两格放在卡片下方。旧后台是表格里两列，
        // 这里是卡片式，所以并排成两栏，窄屏自动堆叠。
        h('div', { class: 'fishcard-body' },
          h('div', { class: 'pickcell' }, h('em', { text: '本期开口' }), autoPickCell(r)),
          h('div', { class: 'rulewrap' }, h('em', { text: '规则' }), ruleCell(r))));
    }) : [empty('没有符合条件的鱼', '换个筛选，或者把搜索词清空。')];
    listWrap.replaceChildren(...(more ? [...cards, more] : cards));
  }

  load();
  const locSel = h('select', { class: 'select', style: { width: '112px', flex: '0 0 auto' }, onchange: (e) => { LOC = e.target.value; SHOWN = PAGE; load(); } },
    h('option', { value: '', text: '所有水域' }), (Array.isArray(areas) ? areas : []).map((a) => h('option', { value: a.loc, text: a.loc, selected: a.loc === LOC })));
  const aside = [
    ib('三份清单分别管什么', '比「整条鱼一句话」更细的一层', bullets([
      '爱吃这些：正常咬钩的饵。',
      '只有底钓才吃：浮钓和路亚抛它不咬。',
      '不吃这些：明确排除，其他条件满足也不咬。',
      '自动抽取：按稀有度每期重新抽一批开口饵，没抽中的降权。',
      '前置条件：写一句人能看懂的话，比如「夜间」「水温 12℃ 以上」。',
      '不勾「启用」等于这套规则作废，按默认走。',
    ])),
    ib('自动抽取怎么算', '按型号抽签，不是按单一饵', bullets([
      '抽中「红蚯蚓（10/14/18）· Pop-up」表示这三个尺寸都开口，同型号不同规格在判鱼上等价。',
      '名单里只写了一个尺寸也整组开。',
      '普通 3 款 / 稀有 2 款 / 超稀有 1 款，配额来自鱼的稀有度。',
      '折叠名一律带形态后缀（煮饵/Pop-up/颗粒/玉米/活饵），因为「玉米」光看名字分不出是颗粒还是玉米粒。',
      '「立即重抽本期」只换这一期的饵，周期和下次切换时刻不变。',
    ])),
    ib('改的是哪一层', '不是改鱼本身，是改后台的吃饵规则', bullets([
      '只影响这种鱼咬不咬、咬哪个饵，不改鱼种、权重、出鱼档位。',
      '保存写进本服规则，不改客户端文件，重启也不丢。',
      '「恢复默认」只丢掉这条鱼的覆盖，回到出厂规则。',
    ])),
    ib('生效方式', null, hint('保存后约 30 秒热加载，不用重启服务，也不用玩家重登。')),
  ];
  const filterSel = h('select', { class: 'select', style: { width: '104px', flex: '0 0 auto' }, onchange: (e) => { FILTER = e.target.value; SHOWN = PAGE; draw(); } },
    [['all', '全部鱼种'], ['tier3', '有 Tier 3 饵'], ['rule', '已设置'], ['none', '没设置'], ['edited', '我改过的']].map(([v, l]) => h('option', { value: v, text: l, selected: v === FILTER })));
  const body = [
    banner,
    h('div', { class: 'row baitfilters', style: { gap: '8px', margin: '0 0 14px', flexWrap: 'nowrap' } },
      h('div', { style: { flex: '1 1 100%', minWidth: '0' } }, searchBox('搜索鱼名或鱼种 ID', debounce((v) => { Q = v; SHOWN = PAGE; load(); }, 300), Q)),
      locSel, filterSel,
      btn('↻ 立即重抽本期', reroll, 'ghost'),
      h('span', { class: 'spacer' }), count),
    // 说明放在列表上方而不是侧栏：这几句直接解释「本期开口」那列怎么读，
    // 放在侧栏要点开才看得见，而这一列正是最容易看错的地方。
    h('p', { class: 'muted small', style: { margin: '0 0 12px', lineHeight: '1.7' } },
      '判定只用「规则」：来自编译进服务端的 fishing_bait_rules.txt（严格模式生效），可以逐鱼覆盖成「吃 / 不吃 / 底钓只吃」。',
      h('br'),
      '「自动抽取」按', h('b', { text: '型号' }), '抽签：抽中「红蚯蚓（10/14/18）· Pop-up」表示这三个尺寸都开口，同型号不同规格在判鱼上等价；', h('b', { text: '名单里只写了一个尺寸也整组开' }), '。'),
    listWrap,
  ];
  return [pageHead('鱼饵偏好', '每种鱼爱吃什么、不吃什么。点「展开修改」逐条改，保存后约 30 秒生效。'), work(body, aside)];
}

// 三份清单的标签编辑器。
// 三种视觉必须能分开，否则用户分不清自己加的是哪一个：
//   整类词（`颗粒`）紫色 / 类别限定（`颗粒@鱼子`）拆成两段 / 普通型号名默认色。
// 这一层由 bait-picker 的 termChip 统一提供，避免两处染色逻辑走岔。
function chipList(arr, redraw) {
  if (!arr.length) return h('div', { class: 'chips' }, h('span', { class: 'muted small', text: '（空）' }));
  return h('div', { class: 'chips' }, ...arr.map((v, i) => termChip(v, () => { arr.splice(i, 1); redraw(); })));
}

function editFish(r, reload) {
  // 主键是 fish_key（00083 迁移）。只传中文名会让「同一鱼种多名」
  // （匈红鲤(镜鲤) / 匈红鲤（镜鲤））再次分裂成两行，所以这里必须带上 key。
  const st = {
    allow: [...(r.allow || [])], deny: [...(r.deny || [])], bottom: [...(r.bottom_only || [])],
    enabled: r.source === 'override' ? !!r.enabled : true,
    cond: condParse(r.condition),
  };
  const snapshot = () => JSON.stringify(st);
  const initial = snapshot();
  const body = h('div');

  async function addTo(title, arr) {
    const names = await openBaitPicker({ title: `加入到「${title}」`, used: arr });
    if (!names || !names.length) return;
    let n = 0, dup = 0;
    names.forEach((v) => { if (arr.includes(v)) dup++; else { arr.push(v); n++; } });
    draw();
    if (n && dup) toast(`已加入 ${n} 项，${dup} 项已在列表里`);
    else if (n) toast(`已加入 ${n} 项`);
    else toast('这些都已经在列表里了', 'bad');
  }

  const bucket = (title, help, arr) => h('div', { style: { padding: '12px 0', borderTop: '1px solid var(--line)' } },
    h('div', { class: 'row', style: { justifyContent: 'space-between', marginBottom: '6px' } },
      h('div', {}, h('b', { text: title }), help ? h('div', { class: 'muted small', text: help }) : null),
      btn('添加饵', () => addTo(title, arr), 'ghost sm', { icon: 'plus' })),
    chipList(arr, draw));

  function draw() {
    const condHint = (COND_TYPES.find((x) => x.t === st.cond.t) || {}).hint || '';
    body.replaceChildren(...[
      h('h3', { style: { fontSize: '14px', margin: '0 0 2px' }, text: '吃什么' }),
      bucket('爱吃', '这些饵可以钓到它。', st.allow),
      bucket('不吃', '用这些饵钓不到它。', st.deny),
      bucket('底钓时只吃', '底钓时只认这些饵。留空表示不限制。', st.bottom),
      h('p', { class: 'muted small', style: { margin: '14px 0 0', lineHeight: '1.7' } },
        '本期开口饵由「自动抽取」独立控制，在列表页那一列勾选；吃口资格只认上面的三份清单，不再有手工轮换组。'),
      h('h3', { style: { fontSize: '14px', margin: '22px 0 8px' }, text: '前置条件' }),
      h('div', { class: 'field' },
        h('label', { text: '这条鱼在什么条件下才开口' }),
        h('select', { class: 'select', id: 'brCondType', style: { width: '100%' },
          onchange: (e) => { st.cond.t = e.target.value; draw(); } },
          COND_TYPES.map((x) => h('option', { value: x.t, text: x.label, selected: st.cond.t === x.t }))),
        st.cond.t === 'chart' ? h('div', { style: { marginTop: '6px' } },
          h('input', { class: 'input', id: 'brCondZones', value: st.cond.zones, placeholder: '海图区号，多个用 / 分隔，例如 30/34/41/55；留空=任意海图区域',
            oninput: (e) => { st.cond.zones = e.target.value; } })) : null,
        st.cond.t === 'custom' ? h('div', { style: { marginTop: '6px' } },
          h('input', { class: 'input', id: 'brCondCustom', value: st.cond.raw, placeholder: '自定义条件文本（按关键词解析）',
            oninput: (e) => { st.cond.raw = e.target.value; } })) : null,
        h('div', { class: 'muted small', id: 'brCondHint', style: { marginTop: '6px', lineHeight: '1.7' }, text: condHint })),
      h('div', { style: { marginTop: '12px' } },
        toggleEl({ checked: st.enabled, label: '使用我的设置（关闭则用默认规则）', onChange: (v) => { st.enabled = v; } })),
      habits(r)].filter(Boolean));
  }
  draw();

  const save = async () => {
    try {
      await put('/api/bait/rules', {
        fish_key: r.fish_key, fish_zh: r.name_zh,
        allow: st.allow, deny: st.deny, bottom_only: st.bottom,
        condition: condCompose(st.cond.t, st.cond.zones, st.cond.raw),
        enabled: st.enabled,
      });
      toast('已保存，约 30 秒内生效');
      saved = true; d.close(); reload();
    } catch (e) { toast(errText(e), 'bad'); }
  };
  let saved = false;
  const d = sidePage({
    title: r.name_zh, sub: r.fish_key, body, effect: '保存后最多 30 秒生效，无需重启游戏客户端。',
    foot: [r.source === 'override' ? btn('恢复默认', async () => {
      if (!(await confirmDialog({ title: `把「${r.name_zh}」恢复默认？`, desc: '删除你的设置，回到内置规则。', confirm: '恢复默认' }))) return;
      try {
        await api('/api/bait/rules/reset', { method: 'POST', body: JSON.stringify({ fish_key: r.fish_key, fish_zh: r.name_zh }) });
        toast('已恢复默认，最多 30 秒生效'); saved = true; d.close(); reload();
      } catch (e) { toast(errText(e), 'bad'); }
    }, 'ghost') : null, h('span', { class: 'spacer' }), btn('保存', save, 'primary')],
    beforeClose: () => saved || snapshot() === initial || window.confirm('放弃未保存的鱼饵修改？'),
    isDirty: () => !saved && snapshot() !== initial,
  });
}

// 只读：这条鱼的习性数据，帮助判断该怎么设置
function habits(r) {
  if (!r.layer && !r.official?.length && !r.baits?.length) return null;
  return h('details', { class: 'advanced', style: { marginTop: '22px' } },
    h('summary', {}, '这种鱼的参考数据 ', h('small', { text: '（只读）' })),
    h('div', { class: 'stack', style: { paddingTop: '12px' } },
      r.layer && (r.layer[0] || r.layer[2]) ? h('p', { class: 'small', text: `活动水层：${(r.layer[0] * 100).toFixed(0)}%–${(r.layer[2] * 100).toFixed(0)}% 深度，最爱 ${(r.layer[1] * 100).toFixed(0)}%（0 = 水面，100% = 水底）。` }) : null,
      r.official?.length ? h('p', { class: 'small', text: '官服占比（仅参考）：' + r.official.map((b) => `${b.bait_id} ${b.pct}%`).join('、') }) : null,
      r.qual_g ? h('div', { class: 'small muted', text: `合格档约 ${fmt(r.qual_g)} g，钩入口容量 ${fmt(r.qual_g)}~${fmt(r.qual_g * 4)} g 全兼容${r.trophy_g ? `；奖杯档 ${fmt(r.trophy_g)} g` : ''}。` }) : null));
}