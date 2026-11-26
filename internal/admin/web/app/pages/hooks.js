// 鱼钩：按型号微调咬口难易。真实物理数据只读。
import { h, api, post, toast, errText, pageHead, work, ib, h2s, hint, bullets, badge, btn, empty, loading, searchBox, debounce, sidePage, Form, fmt, replaceHash, render } from '../core.js';
import { itemIcon } from '../widgets.js';

const FAMILY = [['', '所有钩型'], ['classic', '普通钩'], ['carp', '鲤鱼钩'], ['treble', '三本钩'], ['jighead', '铅头钩'], ['offset', '曲柄钩'], ['marine', '海钓钩'], ['other', '其他']];
const familyName = (f) => (FAMILY.find(([k]) => k === f) || [f, f])[1];
// 数值字段可能缺失（静态库派生不全会漏字段）。直接 Number(undefined).toFixed()
// 会渲染成「NaN kg」—— 那是「算不出来」，不是 0，必须显式回「—」。
const numText = (v, d, unit) => (Number.isFinite(Number(v)) ? Number(v).toFixed(d) + unit : '—');

const isDefault = (p) => Number(p.solid_scale) === 1 && Number(p.hold_scale) === 1 && !Number(p.small_fish_floor) && !Number(p.over_capacity_hold_floor);
// 钩号一律按游戏显示值（S24 / S20 / S1/0 / S14/0），内部刻度只在旁边作对照。
// 直接显示内部刻度会让人对不上客户端和攻略——那两处用的永远是 display_size。
const sizeText = (p) => String(p.display_size || p.size || '');
let Q = '', FAM = '', SIZE = '';

// 兼容旧的搏鱼书签；等路由替换视图后定位到真实的鱼参数分组。
function openFight() {
  const target = '#tuning/gameplay';
  const observer = new MutationObserver(() => {
    observer.disconnect();
    if (location.hash === target) document.getElementById('pg-fish')?.scrollIntoView({ block: 'start' });
  });
  observer.observe(document.getElementById('view'), { childList: true });
  replaceHash(target);
  render();
}

export default async function hooks(params = []) {
  if (params[0] === 'fight') { openFight(); return []; }
  const tbody = h('tbody');
  const count = h('span', { class: 'muted small' });
  // 钩号选项来自接口的 sizes（附各号数量），不是硬编码 0-33。首次加载成功后填充，
  // 之后每次刷新只更新数量，保留当前选中项。
  const sizeSel = h('select', { class: 'select', 'aria-label': '钩号', style: { width: '150px' }, onchange: (e) => { SIZE = e.target.value; load(); } },
    h('option', { value: '', text: '所有钩号' }));
  function fillSizes(sizes) {
    if (!Array.isArray(sizes) || !sizes.length) return;
    const keep = SIZE;
    sizeSel.replaceChildren(
      h('option', { value: '', text: '所有钩号' }),
      // 钩号项缺字段时不能拼出「undefined（—）」：拿不到钩号就跳过这一项，
      // 一个没有钩号的筛选项对用户没有任何意义，还会让人以为数据坏了。
      ...sizes.filter((s) => s && s.size != null).map((s) => h('option', { value: String(s.size), text: `${s.display_size || s.size}（${fmt(s.count)}）`, selected: String(s.size) === keep })));
    if (keep) sizeSel.value = keep;
  }
  const famSel = h('select', { class: 'select', style: { width: '140px' }, onchange: (e) => { FAM = e.target.value; load(); } }, FAMILY.map(([k, l]) => h('option', { value: k, text: l, selected: k === FAM })));
  let sequence = 0;
  const statDl = h('dl');
  async function load() {
    const request = ++sequence;
    count.textContent = '读取中'; statDl.replaceChildren();
    tbody.replaceChildren(h('tr', {}, h('td', { colspan: 7 }, loading(4))));
    try {
      const d = await api(`/api/hooks/profiles?limit=500&q=${encodeURIComponent(Q)}&size=${encodeURIComponent(SIZE)}&family=${encodeURIComponent(FAM)}`);
      if (request !== sequence) return;
      if (!Array.isArray(d.profiles) || !Number.isInteger(d.total)) throw new Error('型号列表不完整');
      fillSizes(d.sizes);
      const list = d.profiles;
      count.textContent = list.length < (d.total || 0) ? `找到 ${fmt(d.total)} 个，显示前 ${list.length} 个` : `${fmt(list.length)} 个型号`;
      statDl.replaceChildren(
        h('div', {}, h('dt', { text: '符合条件的' }), h('dd', { text: fmt(d.total || list.length) })),
        h('div', {}, h('dt', { text: '这页显示' }), h('dd', { text: fmt(list.length) })),
        h('div', {}, h('dt', { text: '本页非默认' }), h('dd', { text: fmt(list.filter((p) => !isDefault(p)).length) })));
      // 空表有两种来源，处置完全不同：一种是筛太窄，一种是库里真没有。
      // 只写「没有符合条件的鱼钩」时用户分不清，会反复改关键词却没结果。
      const emptyRow = h('tr', {}, h('td', { colspan: 7 }, empty('没有符合条件的鱼钩',
        Q || SIZE || FAM
          ? '按当前关键词 / 钩号 / 钩型没筛出来。放宽一项，或者点「所有钩号」看全部。'
          : '钩子库里一个型号都没有。通常是静态库没派生出来 —— 看服务端启动日志里 hook_bite_profile 的结果。')));
      tbody.replaceChildren(...(list.length ? list.map((p) => h('tr', {},
        h('td', {}, h('div', { class: 'row', style: { flexWrap: 'nowrap' } }, itemIcon(p.item_id, 32), h('div', {}, h('b', { text: p.name || p.brand || p.item_id }), h('span', { class: 'sub mono', text: p.item_id })))),
        h('td', { text: familyName(p.family) }),
        h('td', { class: 'num' }, h('b', { text: sizeText(p) }), h('div', { class: 'sub mono', text: '刻度 ' + (p.size ?? '—') })),
        h('td', { class: 'num', text: numText(p.strength_kg, 1, ' kg') }),
        h('td', { class: 'num', text: Number.isFinite(Number(p.entry_capacity_g)) ? fmt(Math.round(p.entry_capacity_g)) + ' g' : '—' }),
        h('td', {}, isDefault(p) ? badge('默认') : null, h('span', { class: 'sub', text: `咬口 ×${p.solid_scale ?? '—'} · 保持 ×${p.hold_scale ?? '—'} · 小鱼下限 ${p.small_fish_floor ?? '—'} · 大鱼下限 ${p.over_capacity_hold_floor ?? '—'}` })),
        h('td', { class: 'num' }, btn('调整', () => edit(p, load), 'sm')))) : [emptyRow]));
    } catch (e) {
      if (request !== sequence) return;
      count.textContent = '未取得';
      tbody.replaceChildren(h('tr', {}, h('td', { colspan: 7 }, empty('读取失败', errText(e) + '。这是接口出错，不是筛选问题；重试一次，仍失败就看服务端日志。'), btn('重试', load))));
    }
  }
  const scheduleLoad = debounce(() => { if (tbody.isConnected) load(); }, 300);
  load();
  const aside = [
    ib('这一页管什么', '不同型号的鱼钩，咬口和中钩后的稳不稳可以单独调整', bullets([
      '不改鱼钩本身的强度、长度和适合鱼重。',
      '咬口难易 ×1 是默认；大于 1 更容易中鱼。',
      '中钩稳定 ×1 是默认；大于 1 搏鱼时更不容易脱钩。',
      '只影响这个型号，同钩型的其他钩号各管各的。',
    ])),
    ib('搏鱼参数', null, h('a', { class: 'pill', href: '#hooks/fight', text: '游戏参数 → 玩法与钓组 → 鱼' })),
    ib('改过多少', null, statDl, hint('改完约 30 秒热加载，不用重启服务。')),
  ];
  return [
    pageHead('钩子手感', '不同型号的鱼钩，咬口和中钩后稳不稳可以单独调。不改变鱼钩本身的强度。'),
    work([
      h2s('鱼钩型号'),
      hint('按钩型、钩号筛选；钩号用游戏里显示的那种（S24 / S20 / S1/0），点「调整」可以单独改一个型号。'),
      h('div', { class: 'hooks-toolbar', style: { display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap', margin: '0 0 12px' } },
        h('div', { style: { flex: '1 1 200px', minWidth: '160px', maxWidth: '320px' } }, searchBox('搜索型号或品牌，如 simmons', v => { Q = v; scheduleLoad(); }, Q)),
        famSel, sizeSel, h('span', { style: { flex: '1 1 auto' } }), count),
      h('div', { class: 'tbl', style: { maxHeight: 'calc(100vh - 330px)', overflow: 'auto' } }, h('table', {},
        h('thead', {}, h('tr', {}, h('th', { text: '鱼钩' }), h('th', { text: '钩型' }), h('th', { class: 'num', text: '钩号' }), h('th', { class: 'num', text: '强度' }),
          h('th', { class: 'num', title: '钩子大小能装下多大的鱼嘴', text: '适合鱼重' }), h('th', {}), h('th', { class: 'num', text: '调整' }))), tbody)),
    ], aside),
  ];

}

function edit(p, reload) {
  const form = new Form();
  const body = h('div', { class: 'stack' },
    h('dl', { class: 'kv small' },
      h('dt', { text: '钩型 / 钩号' }), h('dd', { text: `${familyName(p.family)} · ${sizeText(p)}` }),
      h('dt', { text: '长度' }), h('dd', { text: numText(p.length_mm, 2, ' mm') }),
      h('dt', { text: '强度' }), h('dd', { text: numText(p.strength_kg, 2, ' kg') }),
      h('dt', { text: '适合鱼重' }), h('dd', { text: Number.isFinite(Number(p.entry_capacity_g)) ? '约 ' + fmt(Math.round(p.entry_capacity_g)) + ' g 以下' : '—' })),
    h('div', {},
      form.row({ key: 'solid_scale', label: '咬口难易', help: '1 是默认。大于 1 更容易中鱼，小于 1 更难。', kind: 'float', min: 0, max: 3, def: '1', unit: '倍' }, p.solid_scale),
      form.row({ key: 'hold_scale', label: '中钩后稳不稳', help: '1 是默认。大于 1 搏鱼时不容易脱钩。', kind: 'float', min: 0, max: 3, def: '1', unit: '倍' }, p.hold_scale),
      h('details', { class: 'advanced' }, h('summary', {}, '进阶'), h('div', {},
        form.row({ key: 'small_fish_floor', label: '小鱼最低咬口', help: '鱼远小于钩子时，咬口至少保留这个比例。0–1。', kind: 'float', min: 0, max: 1, def: '0' }, p.small_fish_floor),
        form.row({ key: 'over_capacity_hold_floor', label: '大鱼最低稳定度', help: '鱼超过钩子适合的重量时，稳定度至少保留这个比例。0–1。', kind: 'float', min: 0, max: 1, def: '0' }, p.over_capacity_hold_floor)))));
  const advanced = body.querySelector('details');
  const error = h('p', { role: 'alert', hidden: true });
  body.append(error);
  sidePage({
    effect:'保存后主服热加载，约 30 秒内生效。',
    title: p.name || p.item_id, beforeClose: () => !form.isDirty() || confirm('放弃未保存的调整？'), isDirty: () => form.isDirty(), desc: p.item_id, body,
    actions: [
      { label: '恢复默认', kind: 'ghost', onClick: () => {
        for (const f of form.fields.values()) f.input.value = f.spec.def;
        advanced.open = true; error.hidden = true; form.validate([...form.fields.keys()]); form.refresh(); return true;
      } },
      { label: '取消', kind: 'ghost' },
      { label: '保存', kind: 'primary', onClick: async () => {
        const keys = ['solid_scale', 'hold_scale', 'small_fish_floor', 'over_capacity_hold_floor'];
        error.hidden = true;
        if (!form.validate(keys)) { advanced.open = true; body.querySelector('[aria-invalid="true"]')?.focus(); return true; }
        const v = form.values(keys);
        try {
          await post('/api/hooks/profiles', { item_id: p.item_id, solid_scale: +v.solid_scale, hold_scale: +v.hold_scale, small_fish_floor: +v.small_fish_floor, over_capacity_hold_floor: +v.over_capacity_hold_floor });
        } catch (e) { error.textContent = errText(e); error.hidden = false; return true; }
        toast('已保存，约 30 秒内生效'); reload();
      } },
    ],
  });
}
