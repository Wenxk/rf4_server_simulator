// 鱼种档案：四个页签，一页维护
//   生态基线 / 饵料规则 / 经济四档 / 权重档位
//
// 四个页签原来分属三个独立页面，2026-11-25 合成一页：
// 它们描述的都是「鱼种本身」，横向对照才能看出哪条鱼的数据不对劲。
// 懒加载：切到哪个页签才拉哪个页签的数据，避免一进页面并发四个重查询。
//
// 三个页签的实现分别在 ecology-eco.js / ecology-econ.js / ecology-weight.js，
// 本文件只管页签切换与共享常量，否则单文件会胀到 900 行以上。
import { h, pageHead, work, setCrumbTail, callout, empty } from '../core.js';
import { ecoTab } from './ecology-eco.js';
import { econTab } from './ecology-econ.js';
import { weightTab } from './ecology-weight.js';

// 稀有度三档。数值与后端 fish_ecology.rarity_tier 一一对应（0/1/2），
// 第四档不存在——上游有 tier3，本服只有三档，别照抄。
export const TIER_NAMES = ['普通', '稀有', '超稀有'];
export const TIER_HINTS = ['多数水域的主力鱼', '鲟类、深海与极地鱼、蟹类等', '欧洲鳇、剑鱼、各类白化与锦鲤变种'];

// 页签标题：面包屑末级与页签按钮共用这一份，改文案只改这里。
export const ECON_TAB_META = {
  eco: '生态基线',
  bait: '饵料规则',
  econ: '经济四档',
  weight: '权重档位',
};
const TAB_ORDER = ['eco', 'bait', 'econ', 'weight'];

export default async function ecology(params, scope) {
  let tab = (params && params[0]) || (params && params.tab) || 'eco';
  if (!TAB_ORDER.includes(tab)) tab = 'eco';
  const body = h('div');

  // 页签自己做按钮组，不用 core.pageTabs：
  //   1) 四个页签共用同一条 hash（#ecology），不该产生 #ecology/eco 这种子路由，
  //      否则刷新会落在子路由上、面包屑末级和左栏高亮都对不上；
  //   2) 验收测试按 #ecoTabs button[data-etab="weight"] 定位并读 .on 判断当前页签。
  const tabsEl = h('div', { class: 'tabs', id: 'ecoTabs', role: 'tablist', 'aria-label': '鱼种档案分类' },
    ...TAB_ORDER.map((k) => h('button', {
      type: 'button', role: 'tab', dataset: { etab: k },
      class: k === tab ? 'on' : '',
      'aria-selected': String(k === tab),
      onclick: () => {
        if (tab === k) return;
        tab = k;
        for (const b of tabsEl.children) {
          const on = b.dataset.etab === k;
          b.classList.toggle('on', on);
          b.setAttribute('aria-selected', String(on));
        }
        paint();
      },
    }, ECON_TAB_META[k])));

  function paint() {
    setCrumbTail(ECON_TAB_META[tab] || '');
    // 页签内容整体重画：每个页签自己的 loader 负责拉数据，
    // 这里只保证「切过去就有东西」，不去预取别的页签。
    body.replaceChildren(h('div', { style: { padding: '20px' } }, empty('正在读取…')));
    const p = tab === 'bait' ? import('./bait.js').then((m) => m.default([], scope))
      : tab === 'econ' ? econTab()
      : tab === 'weight' ? weightTab()
      : ecoTab();
    Promise.resolve(p).then((nodes) => {
      // 页签切得快时，旧请求回来会盖掉新页签，这里按当前 tab 再确认一次。
      if (tabNow() === tab) body.replaceChildren(...(Array.isArray(nodes) ? nodes.filter(Boolean) : [nodes]));
    }).catch((e) => {
      if (tabNow() === tab) body.replaceChildren(callout('bad', '读取失败：' + (e.message || e)));
    });
  }
  // tab 变量会被 onclick 改动，闭包里读它就是当前值；保留这个函数是为了
  // 让「旧请求回来」那处判断读起来明确是在比对当前页签。
  const tabNow = () => tab;

  const aside = [
    h('div', { class: 'ib' }, h('b', { text: '四个页签分别管什么' }),
      h('ul', { class: 'bullets' },
        h('li', { text: '生态基线：水温、季节、昼夜、水层、活跃度——决定这条鱼什么时候、在哪一层出现。' }),
        h('li', { text: '饵料规则：吃什么、不吃什么、本节期哪几款开口。' }),
        h('li', { text: '经济四档：每档的经验与鱼价。' }),
        h('li', { text: '权重档位：按稀有度设基础权重，作用于所有水域。' }))),
    h('div', { class: 'ib' }, h('b', { text: '三层优先级' }),
      h('p', { class: 'muted small', text: '资产基线 → 本页的鱼种覆盖 → 水域覆盖（在钓场页改）。越靠后越优先。本页改的是「物种本身」，对全服所有水域生效。' })),
    h('div', { class: 'ib' }, h('b', { text: '生效方式' }),
      h('p', { class: 'muted small', text: '保存后约 30 秒热加载，不用重启服务，也不用玩家重登。' })),
  ];

  paint();
  return [pageHead('鱼种档案', '生态基线 · 饵料规则 · 经济四档 · 权重档位，一页维护。'), tabsEl, work(body, aside)];
}

// 数值格式化：空/null/非数一律 —，避免列表里出现 NaN 或 undefined。
export const num = (v, d = 1) => (v == null || v === '' || !isFinite(Number(v)) ? '—' : Number(v).toFixed(d));