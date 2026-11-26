// 权重档位页签：按鱼种固有稀有度统一设定基础权重。
//
// 影响面是**全部水域**的「水域 × 鱼种」行（loc_fish.catches），
// 不属于某张图，所以入口从钓场页搬到了鱼种档案里。
//
// 三档，没有第四档：上游有 tier3，本服只有普通/稀有/超稀有。
// POST 体严格是 {tier0,tier1,tier2}——验收测试对它做 deepEqual，
// 多带一个键就会挂。所以这里手写对象，不要 spread 整个 form 值。
//
// 只有相对比例有意义：100 : 30 : 5 意味着超稀有被抽中的概率是普通档的 1/20。
import { h, api, toast, errText, btn, empty, loading, confirmDialog, fmt } from '../core.js';

// 三档名称与说明。数值必须与后端 fish_ecology.rarity_tier 对齐（0/1/2）。
const TIER_NAMES = ['普通', '稀有', '超稀有'];
const TIER_HINTS = [
  '多数水域的主力鱼',
  '鲟类、深海与极地鱼、蟹类等',
  '欧洲鳇、剑鱼、各类白化与锦鲤变种',
];

export function weightTab() {
  const box = h('div', { id: 'ecoWeightBox' });
  const state = h('div', { class: 'muted small', style: { margin: '0 0 10px' } });
  let WEIGHTS = [100, 30, 5];
  let TIERS = [];

  async function load() {
    box.replaceChildren(h('div', { style: { padding: '18px' } }, loading(4)));
    try {
      const d = await api('/api/fish/weights/preset');
      const w = Array.isArray(d.weights) ? d.weights : [100, 30, 5];
      WEIGHTS = [Number(w[0]) || 0, Number(w[1]) || 0, Number(w[2]) || 0];
      TIERS = Array.isArray(d.tiers) ? d.tiers : [];
      draw(d);
    } catch (e) {
      box.replaceChildren(empty('权重档位读取失败', errText(e) + '。这是接口出错，没有任何可编辑的档位；重试一次，仍失败就看服务端日志。'));
    }
  }

  function draw(d) {
    const rows = TIERS.length
      ? TIERS.map((t) => h('tr', {},
        h('td', {},
          h('b', { text: TIER_NAMES[t.tier] || t.label || (Number.isInteger(t.tier) ? `档位 ${t.tier}` : '未命名档位') }),
          h('div', { class: 'muted', style: { fontSize: '11.5px' }, text: TIER_HINTS[t.tier] || '' })),
        h('td', { class: 'mono' }, fmt(t.species)),
        h('td', { class: 'mono' }, fmt(t.rows)),
        h('td', { class: 'mono' }, Number(t.avg_base || 0).toFixed(1)),
        h('td', {}, h('input', {
          class: 'input', id: 'tp_' + t.tier, type: 'text', inputmode: 'decimal',
          style: { width: '100px' }, value: String(WEIGHTS[t.tier] ?? ''),
        }))))
      : [h('tr', {}, h('td', { colspan: 5 }, empty('没有读到稀有度档案',
        '接口没有返回 tiers，所以这里列不出各档各有多少鱼种，也就不能按档位重设权重。'
        + '多半是鱼种数据还没导入 —— 先去「参数同步」导一次，再回来看。')))];

    // 状态说明必须写清「现在生效的是哪一套」：档位预设与攻略原值差十倍以上，
    // 不说明白，管理员会以为页面上的数字没生效。
    const activeText = d && d.active
      ? `当前生效的是档位预设（共 ${fmt(d.rows || 0)} 条「水域 × 鱼种」）。`
      : '当前还没启用档位预设，走的是攻略站原值——这就是各图权重差十倍的原因。';
    state.replaceChildren(h('div', {}, '只有相对比例有意义：普通 100 与超稀有 5，意味着后者被抽中的概率是前者的 1/20。', activeText));

    box.replaceChildren(
      state,
      h('div', { class: 'tbl' }, h('table', {},
        h('thead', {}, h('tr', {},
          h('th', { text: '档位' }), h('th', { text: '鱼种数' }), h('th', { text: '涉及行' }),
          h('th', { text: '攻略原值均值' }), h('th', { text: '预设权重' }))),
        h('tbody', {}, ...rows))),
      h('p', { class: 'muted small', style: { marginTop: '10px' } },
        '保存后立即生效，不用重启。已手工设过覆盖权重的鱼不受影响（覆盖优先）；',
        '攻略原值一直保留在静态库里，随时可以回滚。'),
      h('div', { class: 'row', style: { gap: '10px', marginTop: '12px', borderBottom: '0', padding: '0' } },
        btn('确认重设', apply, 'primary'),
        btn('回滚到攻略原值', restore, 'ghost')));
  }

  async function apply() {
    // 三档都要填不小于 0.1 的数。0 或负数会让该档鱼完全不出，
    // 这几乎不会是本意，所以在提交前拦住。
    const w = [0, 1, 2].map((i) => {
      const el = document.getElementById('tp_' + i);
      if (!el) return null;
      const raw = String(el.value).trim();
      return raw === '' ? null : Number(raw);
    });
    if (w.some((v) => v === null || !Number.isFinite(v) || v < 0.1)) {
      return toast('三档都要填不小于 0.1 的数', 'bad');
    }
    try {
      // 严格三个键：验收测试对 POST body 做 deepEqual({tier0,tier1,tier2})。
      const r = await api('/api/fish/weights/preset', {
        method: 'POST',
        body: JSON.stringify({ tier0: w[0], tier1: w[1], tier2: w[2] }),
      });
      toast(`已生效：${r.updated || 0} 档权重立即作用于所有水域`);
      await load();
    } catch (e) { toast('保存失败：' + errText(e), 'bad'); }
  }

  async function restore() {
    if (!(await confirmDialog({
      title: '回滚到攻略原值',
      desc: '会清空档位预设，所有水域的基础权重回到攻略站渔获数原值（各图尺度差十倍以上）。',
      confirm: '回滚', danger: true,
    }))) return;
    try {
      await api('/api/fish/weights/restore', { method: 'POST', body: JSON.stringify({}) });
      toast('已回滚到攻略原值');
      await load();
    } catch (e) { toast('回滚失败：' + errText(e), 'bad'); }
  }

  load();
  return box;
}