// 玩家：顶上选人，下面按二级页签看和改这个人的一切。
import {
  h, api, apiList, post, icon, toast, errText, pageHead, pageTabs, work, ib, h2s, h3s, hint, note, kv, bullets, pill, callout, badge,
  empty, btn, loading, Form, savebar,
  searchBox, debounce, confirmDialog, sidePage, fmt, timeText, relTime, navigate, segmented, hideSavebar, replaceHash, render, rerender, setCrumbTail,
  getToken, loginDialog,
} from '../core.js';
import { itemIcon, pickItem } from '../widgets.js';

const TABS = [['profile', '档案'], ['items', '背包与装备'], ['keepnet', '鱼护'], ['records', '鱼种记录'], ['skills', '技能'], ['mail', '邮箱']];
const GRADE = ['未钓到', '钓到过', '达标', '星级', '蓝冠'];
const cents = (v) => (Number(v || 0) / 100).toFixed(2).replace(/\.00$/, '');
let QUERY = '';

export default async function players(params, scope) {
  const [uidParam, tabParam] = params;
  const routeHash = location.hash;
  const isCurrent = () => scope.isCurrent() && location.hash === routeHash;
  if (!getToken()) {
    // 玩家数据不对未登录访客下发；其余页面都能直接看。
    return [
      pageHead('玩家管理', '查看和修改角色的等级、货币、背包、鱼护、图鉴和技能。'),
      work([
        h2s('这一页需要登录'),
        hint('玩家档案涉及账号数据，未登录时后台不下发。登录后本页会自动加载，其它页面不登录也能看。'),
        h('div', { style: { marginTop: '14px' } }, btn('登录管理后台', () => loginDialog(), 'primary')),
        h('p', { class: 'hint', style: { marginTop: '12px' }, html: '也可以在控制中心首页登录：登录状态在两个页面之间是共用的。' }),
      ], ib('为什么单独拦', null, hint('改玩家数据要留审计、写角色库，所以只把这类接口放在登录后面。'))),
    ];
  }
  const tab = TABS.some(([k]) => k === tabParam) ? tabParam : 'profile';
  const tabLabel = (TABS.find(([k]) => k === tab) || [])[1];

  let list = [], listError = '', request = 0;
  let expired = false;
  const loadList = async (background = false) => {
    const id = ++request;
    try {
      const rows = await apiList('/api/players?q=' + encodeURIComponent(QUERY), { preserveSession: background });
      if (id === request) { list = rows; listError = ''; }
    } catch (e) { if (id === request) { listError = errText(e); if (background && e.status === 401) expired = true; } }
  };
  await loadList();
  if (!isCurrent()) return [];
  const uid = uidParam || (list[0] ? String(list[0].uid) : '');
  if (!uidParam && uid) { navigate('players', uid, tab); return []; }

  const whoRow = h('div', { class: 'whorow', role: 'group', 'aria-label': '玩家名单' });
  const presence = row => {
    const state = listError || row?.online_known === false || typeof row?.online !== 'boolean' ? 'unknown' : row.online ? 'online' : 'offline';
    return h('span', { class: 'player-presence', dataset: { state }, text: {online:'在线', offline:'离线', unknown:'状态未上报'}[state] });
  };
  const drawWho = () => {
    const focused = whoRow.contains(document.activeElement) ? document.activeElement.dataset.uid : null;
    const shown = list.slice(0, 16);
    whoRow.replaceChildren(...[
      shown.length ? h('div', { class: 'seg player-roster' }, ...shown.map((p2) => h('button', {
        type: 'button', dataset: {uid: p2.uid}, 'aria-pressed': String(String(p2.uid) === String(uid)),
        class: String(p2.uid) === String(uid) ? 'roster-option on' : 'roster-option',
        onclick: () => { if (String(p2.uid) !== String(uid)) navigate('players', p2.uid, tab); },
      }, h('span', { class:'player-identity' }, h('strong', {text:p2.name || `UID ${p2.uid ?? '?'}`, title:p2.name || ''}),
        h('small', { text:'UID ' + (p2.uid ?? '?') })),
      // 字段缺失时不能把 undefined 直接拼进字符串：名单里出现「Lv.undefined」
      // 会让人以为玩家等级坏了，实际只是这条记录没同步全。
      h('span', {class:'roster-level', text: p2.level == null ? '等级未上报' : `Lv.${p2.level}` }), h('span', {class:'roster-state'}, presence(p2), p2.banned ? h('small',{text:'已封禁'}) : null),
      h('span', {class:'roster-mark', 'aria-hidden':'true'}, String(p2.uid) === String(uid) ? icon('check',16) : null))))
        : h('span', { class: 'hint', text: listError ? '玩家名单读取失败：' + listError : QUERY ? '没有找到这个玩家' : '还没有玩家登录过' }),
      list.length > shown.length ? h('span', { class: 'hint', text: '共 ' + list.length + ' 人，用上面的搜索找' }) : null,
    ].filter(Boolean));
    if (focused) [...whoRow.querySelectorAll('button')].find(b => b.dataset.uid === focused)?.focus({preventScroll:true});
  };
  drawWho();

  let refreshPresence = () => {};
  const search = searchBox('搜索昵称或 UID', debounce((v) => { QUERY = v; loadList().then(() => { drawWho(); refreshPresence(); }); }, 260), QUERY);
  search.querySelector('input').setAttribute('aria-label','搜索昵称或 UID');
  const searchRow = h('div', { class: 'roster-toolbar' }, h('h3', {text:'切换玩家'}), search,
    btn('新建玩家', () => createPlayer(), 'good sm'));

  const tabsEl = pageTabs(TABS, tab, (k) => navigate('players', uid, k));
  setCrumbTail(tabLabel);

  let p = null;
  if (uid) {
    try { p = await api('/api/players/detail?uid=' + encodeURIComponent(uid)); } catch (e) { p = null; }
    // 只接受对象。空数组是 truthy，漏过去会让整个详情页渲染成一片 undefined。
    if (!p || typeof p !== 'object' || Array.isArray(p) || p.uid === undefined) p = null;
  }
  if (!isCurrent()) return [];

  if (!p) {
    return [
      pageHead('玩家管理', '查看和修改角色的等级、货币、背包、鱼护、图鉴和技能。'),
      tabsEl,
      work([searchRow, whoRow, h('div', { style: { marginTop: '18px' } }, callout(uid ? 'bad' : 'info', uid ? '读取这个玩家失败，可能已经不存在。' : '先在上面选一个玩家。'))], [
        ib('说明', null, bullets(['只能改已经在游戏里登录过的角色。', '改完玩家在线时会立即看到变化，不用重登。'])),
      ]),
    ];
  }

  const reload = () => { if (isCurrent() && whoRow.isConnected) render(); };
  const status = h('span');
  const headerStatus = h('span');
  refreshPresence = syncStatus;
  syncStatus();
  function syncStatus() {
    const row = list.find(row => String(row.uid) === String(uid));
    headerStatus.replaceChildren(presence(row));
    status.replaceChildren(presence(row), p.banned ? ' 已封禁' : '');
  }
  let polling = false;
  const poll = setInterval(async () => {
    if (polling || expired || !isCurrent() || !whoRow.isConnected || document.hidden) return;
    polling = true;
    try {
      await loadList(true);
      if (isCurrent() && whoRow.isConnected) {
        drawWho(); syncStatus();
        if (expired) status.replaceChildren('会话已过期，草稿未丢失。', btn('重新登录', () => loginDialog(), 'ghost'));
      }
    }
    finally { polling = false; }
  }, 8000);
  const resume = () => { expired = false; syncStatus(); };
  document.addEventListener('rf4:login', resume);
  scope.setPageCleanup(() => { clearInterval(poll); document.removeEventListener('rf4:login', resume); });
  // 数值缺失一律走 fmt（回「—」），不要直接拼字符串 —— 否则页面上会出现
// 「Lv.undefined · 经验 undefined」这种看起来像数据坏了的字样。
  const meta = `UID ${p.uid ?? '—'} · ${p.level == null ? '等级未上报' : 'Lv.' + p.level} · 经验 ${fmt(p.xp)} · 物品 ${fmt(p.item_count || 0)} · 鱼护 ${fmt(p.keepnet_count || 0)} · 邮箱 ${fmt(p.mail_count || 0)}`;
  const actRow = h('div', { class: 'row', style: { gap: '8px', margin: '0 0 14px', padding: '0', borderBottom: '0', flexWrap: 'nowrap', justifyContent: 'flex-start' } },
    btn('发邮件', () => navigate('mail', p.uid), 'sm', { icon: 'mail' }),
    btn('踢下线', async () => {
      if (!(await confirmDialog({ title: `把「${p.name}」踢下线？`, desc: '玩家会被断开连接，可以马上重新登录。', confirm: '踢下线' }))) return;
      try { await post('/api/players/kick', { uid: p.uid }); toast('已踢下线'); } catch (e) { toast(errText(e), 'bad'); }
    }, 'ghost sm'),
    btn(p.banned ? '解除封禁' : '封禁', async () => {
      const ban = !p.banned;
      if (!(await confirmDialog({ title: ban ? `封禁「${p.name}」？` : `解除对「${p.name}」的封禁？`, desc: ban ? '封禁后这个玩家无法登录游戏。' : '解除后玩家可以正常登录。', confirm: ban ? '封禁' : '解除封禁', danger: ban }))) return;
      try { await post('/api/players/ban', { uid: p.uid, banned: ban }); toast(ban ? '已封禁' : '已解除封禁'); render(); } catch (e) { toast(errText(e), 'bad'); }
    }, p.banned ? 'ghost sm' : 'danger sm'));

  const builders = { profile, items, keepnet, records, skills, mail };
  const pane = builders[tab](p, reload, scope.setLeaveGuard);

  const hh = p.happy_hour || {};
  const aside = [
    ib('这个账号', null, status, kv([
      ['UID', p.uid == null ? '—' : String(p.uid)],
      ['昵称', p.name || '—'],
      ['等级', p.level == null ? '未上报' : 'Lv.' + p.level],
      ['银币', cents(p.silver) + ' 银'],
      ['金币', cents(p.gold) + ' 金'],
      ['会员', p.membership === 1 ? (p.membership_active === false ? '限时会员已到期' : '限时会员') : p.membership === 2 ? '永久会员' : '普通'],
      // 「最近登录」已去掉：profile 表没有这个字段，detail 也不返回，
      // 留着只会永远显示「—」，看起来像坏了。
    ])),
    ib('背包与鱼护', null, kv([
      ['物品', String(p.item_count || 0) + ' 件'],
      ['鱼护', String(p.keepnet_count || 0) + ' 条'],
      ['邮箱', String(p.mail_count || 0) + ' 封'],
    ]), hint(hh.enabled === false ? '欢乐时光已在「钓场与鱼口」里关闭。' : !hh.qualified ? '开通会员后可以享受欢乐时光加成。' : hh.unlimited ? '永久会员，欢乐时光一直有效。' : '欢乐时光有效，剩余约 ' + Math.ceil((hh.remaining_seconds || 0) / 60) + ' 分钟。')),
    ib('相关操作', null, h('div', { class: 'pills' },
      h('a', { class: 'pill', href: '#mail/' + p.uid, text: '给这个人发邮件' }),
      h('a', { class: 'pill', href: '#audit', text: '看他改过什么' })),
      hint('封禁、踢下线会写进审计日志。')),
  ];
  return [
    pageHead('玩家管理', '查看和修改角色的等级、货币、背包、鱼护、图鉴和技能。'),
    tabsEl,
    work([searchRow, whoRow, h('div', {class:'player-heading'}, h2s(`${p.name || '未命名角色'}（UID ${p.uid ?? '—'}）`), headerStatus), hint(meta), actRow, pane], aside),
  ];
}

// ---------- 新建玩家 ----------
// 档案没有密码：客户端用同名登录就会绑定到这份档案（大小写不敏感）。
// 没建过的名字仍落到本机固定号，不会自动建档；新手装备由主服首登按清单发放。
function createPlayer() {
  const form = new Form();
  const body = h('div', { class: 'stack' },
    // uid 初值必须是数字 0：kind:'int' 的空串过不了校验，而「留空自动分配」的
    // 语义由 0 表达（后端也是 uid<=0 才取最大号+1）。
    form.row({ key: 'uid', label: 'UID', kind: 'int', min: 0, def: '0', help: '填 0 = 现有最大 UID + 1，自动分配' }, 0),
    form.row({ key: 'name', label: '昵称', kind: 'string', help: '玩家用这个名字登录就会绑到这份档案。留空按 player 建档。' }, ''),
    form.row({ key: 'level', label: '等级', kind: 'int', min: 1, max: 58, def: '1', unit: '级' }, 1),
    form.row({ key: 'silver', label: '银币（分）', kind: 'int', min: 0, def: '5000' }, 5000),
    form.row({ key: 'gold', label: '金币（分）', kind: 'int', min: 0, def: '0' }, 0),
    hint('新手装备由主服首次登录时按清单发放，这里只管档案本身。'));
  const error = h('p', { role: 'alert', hidden: true });
  body.append(error);
  sidePage({
    title: '新建玩家',
    desc: '没有密码：客户端用同名登录即进入这个档案，等于登记账号。',
    body,
    actions: [
      { label: '取消', kind: 'ghost' },
      { label: '创建玩家', kind: 'primary', onClick: async () => {
        error.hidden = true;
        const keys = ['uid', 'name', 'level', 'silver', 'gold'];
        if (!form.validate(keys)) { body.querySelector('[aria-invalid="true"]')?.focus(); return true; }
        const v = form.values(keys);
        const payload = {
          uid: Math.max(0, Math.floor(+v.uid || 0)),
          name: String(v.name || '').trim(),
          level: Math.max(1, Math.floor(+v.level || 1)),
          silver: Math.max(0, Math.floor(+v.silver || 0)),
          gold: Math.max(0, Math.floor(+v.gold || 0)),
        };
        try {
          const r = await post('/api/players/create', payload);
          toast(`已创建 ${r.name} #${r.uid}`);
          // 建完直接跳到这个人，省一次搜索；整页重渲染会带上新名单。
          //
          // 一定要带 `#`：replaceHash 直接交给 history.replaceState，漏了 `#`
          // 会把 "players/1000002" 当成相对路径写进 URL path——浏览器随即按新
          // 路径重新请求文档，页面整页重载、草稿全丢，而且 URL 长得像 SPA 路由
          // 一样，排查时很容易以为是路由问题。其余五个调用点都是带 `#` 的。
          //
          // 一定要用 rerender 而不是 render：这里是侧页动作内部，pending 还是 true，
          // render() 会被 leaveSidePages 拦掉、静默不重绘。
          replaceHash(`#players/${r.uid}`);
          rerender();
        } catch (e) { error.textContent = errText(e); error.hidden = false; return true; }
      } },
    ],
  });
}

// ---------- 发放物品 ----------
// 走 /api/players/grant：服务端按 item_id 造实例塞进背包，玩家在线时约 2 秒收到刷新。
function grantItem(p, reload) {
  let picked = null;
  const pickedBox = h('div', { class: 'muted small', text: '还没有选物品' });
  const qty = h('input', { class: 'input num', type: 'number', min: 1, step: 1, value: '1', style: { width: '110px' } });
  const error = h('p', { role: 'alert', hidden: true });
  const choose = () => pickItem({
    title: '选择要发放的物品',
    onPick: (x) => {
      picked = x;
      pickedBox.replaceChildren(h('div', { class: 'row' }, itemIcon(x.item_id, 28), h('div', {}, h('b', { text: x.name }), h('div', { class: 'sub mono', text: x.item_id }))));
      pickedBox.classList.remove('muted');
    },
  });
  const body = h('div', { class: 'stack' },
    hint(`发给 ${p.name}（UID ${p.uid}）。`),
    h('div', {}, h('div', { class: 'row', style: { gap: '8px', alignItems: 'center', marginBottom: '8px' } }, btn('选择物品', choose, 'sm'), pickedBox)),
    h('label', { class: 'row', style: { gap: '8px', alignItems: 'center' } }, h('span', { class: 'muted small', text: '数量' }), qty),
    error);
  sidePage({
    title: '发放物品',
    desc: '下发后约 2 秒到达，在线玩家会立即看到。',
    body,
    actions: [
      { label: '取消', kind: 'ghost' },
      { label: '发放', kind: 'primary', onClick: async () => {
        error.hidden = true;
        if (!picked) { error.textContent = '请先选择物品'; error.hidden = false; return true; }
        const n = Math.max(1, Math.floor(+qty.value || 1));
        try { await post('/api/players/grant', { uid: p.uid, item_id: picked.item_id, qty: n }); }
        catch (e) { error.textContent = errText(e); error.hidden = false; return true; }
        toast('已下发，约 2 秒内到达');
        setTimeout(reload, 2300);
      } },
    ],
  });
}

// ---------- 概况 ----------
function profile(p, reload, setDirty) {
  const form = new Form();
  // 保存条内联在表单底部（演示页就是 section 底部的「放弃修改 / 核对并保存」）。
  const actions = h('div', { class: 'savebar-inline' });
  savebar(form, {
    inline: actions,
    effect:'在线玩家立即收到刷新。',
    onSave: async (v) => {
      const body = { uid: p.uid };
      if ('name' in v) body.name = v.name;
      for (const key of ['level', 'xp']) if (key in v) body[key] = Number(v[key]);
      for (const key of ['silver', 'gold', 'bank']) if (key in v) body[key] = Math.round(Number(v[key]) * 100);
      const m = +memberField.input.value;
      const exp = m === 1 && expInput.value ? Math.floor(new Date(expInput.value).getTime() / 1000) : 0;
      if ('membership' in v || 'membership_exp' in v) {
        if (m === 1 && (!exp || exp <= Date.now() / 1000)) throw new Error('限时会员需要选一个未来的到期时间');
        body.membership = m; body.membership_exp = exp;
      }
      await post('/api/players/update', body);
      setTimeout(() => { if (!form.isDirty()) reload(); }, 0);
    },
  });
  setDirty(() => form.isDirty());
  // 会员：分段选择 + 到期时间
  const memberField = form.field({ key: 'membership', label: '会员', kind: 'string' }, String(p.membership || 0));
  memberField.input = h('input', { type: 'hidden', value: memberField.original });
  const toLocal = (ts) => { if (!ts) return ''; const d = new Date(ts * 1000); d.setMinutes(d.getMinutes() - d.getTimezoneOffset()); return d.toISOString().slice(0, 16); };
  const expField = form.field({ key: 'membership_exp', label: '到期', kind: 'string' }, toLocal(p.membership_exp));
  const expInput = h('input', { type: 'datetime-local', class: 'input', value: expField.original, style: { width: '240px' }, oninput: () => form.refresh() });
  expField.input = expInput;
  const expWrap = h('div', { class: 'row', hidden: String(p.membership) !== '1' }, h('span', { class: 'muted small', text: '到期时间' }), expInput);
  const seg = segmented([['0', '普通'], ['1', '限时会员'], ['2', '永久会员']], String(p.membership || 0), (v) => { memberField.input.value = v; expWrap.hidden = v !== '1'; form.refresh(); });
  seg.classList.add('membership-options');
  const hh = p.happy_hour || {};
  const hhText = hh.enabled === false ? '欢乐时光已在「玩法规则」里关闭。' : !hh.qualified ? '开通会员后可以享受欢乐时光加成。'
    : hh.unlimited ? '永久会员，欢乐时光一直有效。' : `欢乐时光有效，剩余约 ${Math.ceil((hh.remaining_seconds || 0) / 60)} 分钟。`;
  const memberRow = h('div', { class: 'row' }, h('span', { class: 'lab' },
    h('b', { text: '会员' }), h('small', { text: '限时需填到期' })),
    h('span', { class: 'ctrl' }, seg));
  const expRow = h('div', { class: 'row', hidden: String(p.membership) !== '1' }, h('span', { class: 'lab' }, h('b', { text: '会员到期' })),
    h('span', { class: 'ctrl' }, expInput));
  const hhNote = h('p', { class: 'hint', style: { margin: '10px 0 0' }, text: hhText });
  const resetExp = () => { expRow.hidden = memberField.input.value !== '1'; };
  seg.addEventListener('click', () => setTimeout(resetExp, 0), true);
  resetExp();
  return h('div', {},
    h('div', { class: 'rows' },
      form.row({ key: 'name', label: '昵称', kind: 'string', required: true }, p.name),
      form.row({ key: 'level', label: '等级', kind: 'int', min: 1, max: 58, unit: '级' }, p.level),
      form.row({ key: 'xp', label: '累计经验', kind: 'int', min: 0, help: '等级和经验要对得上，否则经验条会显示异常。' }, p.xp),
      form.row({ key: 'silver', label: '银币（分）', kind: 'float', min: 0, help: '按游戏里显示的数字填，可以带两位小数。' }, cents(p.silver)),
      form.row({ key: 'gold', label: '金币（分）', kind: 'float', min: 0 }, cents(p.gold)),
      form.row({ key: 'bank', label: '银行（分）', kind: 'float', min: 0 }, cents(p.bank)),
      memberRow, expRow),
    hhNote,
    actions);
}

// ---------- 背包 ----------
function items(p, reload) {
  let q = '';
  const body = h('tbody');
  const draw = () => {
    const rows = (p.items || []).filter((x) => !q || `${x.item_id} ${x.name} ${x.guid}`.toLowerCase().includes(q));
    body.replaceChildren(...(rows.length ? rows.map((x) => h('tr', {},
      h('td', {}, h('div', { class: 'row', style: { flexWrap: 'nowrap' } }, itemIcon(x.item_id), h('div', {}, h('b', { text: x.name || x.item_id }), h('span', { class: 'sub mono', title: x.guid, text: x.item_id + ' · ' + String(x.guid).slice(-8) })))),
      h('td', { text: x.container === 'equip' ? '装备中' : x.container === 'bag' ? '背包' : (x.container || '') }),
      h('td', { class: 'num', text: fmt(x.qty) }),
      h('td', { class: 'num', text: x.durability === undefined || x.durability === null ? '—' : Number(x.durability) <= 1 ? (Number(x.durability) * 100).toFixed(1) + '%' : String(x.durability) }),
      h('td', { class: 'num' }, btn('删除', async () => {
        if (!(await confirmDialog({ title: `删除「${x.name || x.item_id}」？`, desc: `${p.name}（UID ${p.uid}） · 实例 ${x.guid} · 数量 ${x.qty}，不能恢复。`, confirm: '删除', danger: true }))) return;
        try { await post('/api/players/items/delete', { uid: p.uid, guids: [x.guid] }); toast('已删除，约 2 秒后生效'); setTimeout(reload, 2300); } catch (e) { toast(errText(e), 'bad'); }
      }, 'ghost sm')))) : [h('tr', {}, h('td', { colspan: 5 }, empty(
        q ? '没有匹配的物品' : '背包和装备栏都是空的',
        // 「搜不到」和「真没有」是两件事：后者是玩家里没东西，不是筛选问题。
        q ? '这个玩家的物品里没有符合当前关键词的。清空搜索框看全部。'
          : '这个玩家没有装备在身上的东西，背包里也是空的。可以用上面的「发放物品」给他补。')))]));
  };
  draw();
  return h('div', { class: 'stack' },
    h('div', { class: 'row', style: { gap: '8px', flexWrap: 'wrap', alignItems: 'center' } },
      btn('发放物品', () => grantItem(p, reload), 'good sm'),
      h('span', { class: 'muted small', text: '下发后约 2 秒到达，在线玩家会立即看到。' })),
    searchBox('筛选物品名称或 ID', (v) => { q = v.toLowerCase(); draw(); }),
    h('div', { class: 'tbl', style: { maxHeight: '62vh', overflow: 'auto' } }, h('table', {},
      h('thead', {}, h('tr', {}, h('th', { text: '物品' }), h('th', { text: '位置' }), h('th', { class: 'num', text: '数量' }), h('th', { class: 'num', text: '耐久' }), h('th', {}))), body)));
}

// ---------- 护鱼网 ----------
function keepnet(p, reload) {
  const rows = p.keepnet || [];
  return h('div', { class: 'stack' },
    h('div', { class: 'row' }, h('span', { class: 'grow muted small', text: `共 ${rows.length} 条鱼。删除后统计和鱼市会同步更新。` }), btn('放入一条鱼', () => addFish(p, reload), 'primary sm', { icon: 'plus' })),
    rows.length ? h('div', { class: 'tbl', style: { maxHeight: '62vh', overflow: 'auto' } }, h('table', {},
      h('thead', {}, h('tr', {}, ['鱼', '重量', '档位', '地点', '饵', '时间', ''].map((t, i) => h('th', { class: i === 1 ? 'num' : '', text: t })))),
      h('tbody', {}, rows.map((x) => h('tr', {},
        h('td', {}, h('b', { text: x.name_zh || x.species }), h('span', { class: 'sub mono', text: x.species })),
        h('td', { class: 'num', text: fmt(x.weight_g) + ' g' }),
        h('td', {}, badge(GRADE[x.cat] || ('档位 ' + x.cat), x.cat >= 4 ? 'accent' : x.cat >= 3 ? 'warn' : '')),
        h('td', { text: x.keep_level || x.keep_loc || '—' }),
        h('td', { class: 'mono small', text: x.keep_bait || '—' }),
        h('td', { class: 'small muted', text: timeText(x.ts) }),
        h('td', {}, btn('删除', async () => {
          if (!(await confirmDialog({ title: '从护鱼网删除这条鱼？', desc: '不能恢复。', confirm: '删除', danger: true }))) return;
          try { await post('/api/players/keepnet/delete', { uid: p.uid, guids: [x.echo_guid] }); toast('已删除，约 2 秒后生效'); setTimeout(reload, 2300); } catch (e) { toast(errText(e), 'bad'); }
        }, 'ghost sm'))))))) : h('div', { class: 'card' }, empty('护鱼网是空的',
      '这个玩家的鱼护里一条鱼都没有 —— 他没钓到，或者刚被清空过。点上面的「放入一条鱼」可以手工补一条。')));
}
function addFish(p, reload) {
  const form = new Form();
  const results = h('div', { class: 'card', style: { maxHeight: '200px', overflow: 'auto' } });
  const species = form.stacked({ key: 'species', label: '鱼种', kind: 'string', required: true, placeholder: '搜索鱼名，如 拟鲤', span: true }, '');
  const speciesInput = form.fields.get('species').input;
  let selected = '';
  speciesInput.addEventListener('input', () => { selected = ''; hint.textContent = ''; results.replaceChildren(); });
  const hint = h('div', { class: 'help' });
  speciesInput.addEventListener('input', debounce(async () => {
    const q = speciesInput.value.trim();
    if (!q) { results.replaceChildren(); return; }
    // 不要吞成空数组：否则「接口挂了」和「确实没这个鱼名」在界面上完全一样，
    // 用户会一直换关键词试。失败就照实说。
    let list, err = null;
    try { list = await apiList('/api/fish/pool?limit=30&q=' + encodeURIComponent(q)); }
    catch (e) { list = []; err = e; }
    if (speciesInput.value.trim() !== q) return;
    if (err) { results.replaceChildren(empty('鱼种库读取失败', errText(err) + '。这是接口出错，不是没有这个鱼名。')); return; }
    results.replaceChildren(list.length ? h('div', { class: 'list' }, list.map((f) => h('button', { type: 'button', class: 'list-item clickable', onclick: () => { selected = speciesInput.value = f.fish_key; hint.textContent = `${f.name_zh}，常见 ${fmt(f.min_g)}–${fmt(f.max_g)} g`; results.replaceChildren(); } },
      h('div', { class: 'grow' }, h('div', { class: 'title', text: f.name_zh }), h('div', { class: 'sub mono', text: f.fish_key })), h('span', { class: 'muted small', text: `${fmt(f.min_g)}–${fmt(f.max_g)} g` }))))
      : empty('没有这个鱼种', `鱼种库里没有匹配「${q}」的鱼。中文名、拉丁名或 fish_key 都认，换个写法再试。`));
  }, 250));
  sidePage({
    title: '放入护鱼网', beforeClose: () => !form.isDirty() || confirm('放弃未保存的鱼护记录？'), isDirty: () => form.isDirty(),
    body: h('div', { class: 'stack' }, species, results, hint, h('div', { class: 'form-grid' },
      form.stacked({ key: 'weight_g', label: '重量（克）', kind: 'int', min: 1 }, '500'),
      form.stacked({ key: 'cat', label: '档位', kind: 'select', options: [['1', '普通'], ['2', '达标'], ['3', '奖杯'], ['4', '蓝冠']] }, '1'),
      form.stacked({ key: 'keep_level', label: '地点（可选）', kind: 'string', placeholder: '例如 level_001_lake' }, ''),
      form.stacked({ key: 'keep_loc', label: '场景（可选）', kind: 'string' }, ''),
      form.stacked({ key: 'keep_spot', label: '钓点编号', kind: 'int', min: 0 }, '0'),
      form.stacked({ key: 'keep_bait', label: '用的饵（可选）', kind: 'string', placeholder: '例如 worm_01' }, ''))),
    actions: [{ label: '取消', kind: 'ghost' }, {
      label: '放入', kind: 'primary', onClick: async () => {
        if (!form.validate(['species', 'weight_g', 'keep_spot'])) return true;
        if (!selected || selected !== speciesInput.value) { hint.textContent = '请从搜索结果选择鱼种。'; speciesInput.focus(); return true; }
        await post('/api/players/keepnet/add', { uid: p.uid, species: selected, weight_g: +form.value('weight_g'), cat: +form.value('cat'), keep_level: form.value('keep_level'), keep_loc: form.value('keep_loc'), keep_spot: +form.value('keep_spot'), keep_bait: form.value('keep_bait') });
        toast('已放入，约 2 秒后生效'); setTimeout(reload, 2300);
      },
    }],
  });
}

// ---------- 图鉴补录 ----------
function records(p, reload, setDirty) {
  const box = h('div', {}, loading(5));
  let data = null, level = '', q = '';
  const changes = new Map();
  setDirty(() => changes.size > 0);
  const saveBtn = btn('保存补录', async () => {
    const invalid = [...changes.values()].find(c => c.raw && (!/^[1-9]\d*$/.test(c.raw) || !Number.isSafeInteger(Number(c.raw))));
    if (invalid) { toast(invalid.fish_key + ' 的补录重量应为正整数克，留空表示自动。', 'bad'); return; }
    saveBtn.disabled = true;
    try {
      const r = await post('/api/players/records', { uid: p.uid, level, changes: [...changes.values()].map(({fish_key, grade, raw}) => ({fish_key, grade, weight_g: Number(raw)})) });
      toast('已保存，并自动备份了存档（' + r.backup + '）');
      changes.clear(); await load(level);
    } catch (e) { toast(errText(e), 'bad'); saveBtn.disabled = false; }
  }, 'primary', { disabled: true });
  const sync = () => { saveBtn.disabled = !changes.size; saveBtn.textContent = changes.size ? `保存补录（${changes.size}）` : '保存补录'; };
  async function load(lv) {
    box.replaceChildren(loading(5));
    try { data = await api('/api/players/records?uid=' + p.uid + '&level=' + encodeURIComponent(lv || '')); level = data.level; draw(); }
    catch (e) { box.replaceChildren(empty('补录记录读取失败', errText(e) + '。这张地图的历史记录取不到，补录不可用；重试一次，仍失败就看服务端日志。')); }
  }
  function draw() {
    const mapSel = h('select', { class: 'select', style: { width: '220px' }, onchange: async (e) => {
      if (changes.size && !(await confirmDialog({ title: '这张地图的修改还没保存', desc: '切换地图会放弃这些修改。', confirm: '放弃并切换' }))) { e.target.value = level; return; }
      changes.clear(); sync(); load(e.target.value);
    } }, data.maps.map((m) => h('option', { value: m.level, text: m.name, selected: m.level === data.level })));
    const tbody = h('tbody');
    const rows = () => tbody.replaceChildren(...data.fish.filter((f) => !q || `${f.name} ${f.fish_key}`.toLowerCase().includes(q)).map((f) => {
      const c = changes.get(f.fish_key) || { fish_key: f.fish_key, grade: f.restored_grade || 0, weight_g: f.restored_weight_g || 0 };
      const sel = h('select', { class: 'select', style: { height: '30px' } }, [0, 1, 2, 3, 4].map((g) => h('option', { value: g, text: g === 0 ? '不补录' : GRADE[g], selected: +c.grade === g })));
      const w = h('input', { class: 'input num', style: { height: '30px', width: '130px' }, placeholder: '自动', value: c.raw ?? (c.weight_g || '') });
      const edit = () => { changes.set(f.fish_key, { fish_key: f.fish_key, grade: +sel.value, raw: w.value.trim() }); sync(); };
      sel.addEventListener('change', () => { w.value = ''; edit(); });
      w.addEventListener('input', edit);
      return h('tr', {}, h('td', {}, h('b', { text: f.name }), h('span', { class: 'sub mono', text: f.fish_key })),
        h('td', {}, badge(GRADE[f.grade] || '未钓到', f.grade >= 4 ? 'accent' : f.grade >= 3 ? 'warn' : f.grade > 0 ? 'ok' : '')),
        h('td', { class: 'num', text: f.weight_g ? fmt(f.weight_g) + ' g' : '—' }), h('td', {}, sel), h('td', {}, w));
    }));
    rows();
    box.replaceChildren(h('div', { class: 'stack' },
      callout('info', '用于补齐图鉴。补录算一次钓获，但不给经验、货币，也不放进护鱼网。真实钓到的记录不会被覆盖。保存前会自动备份存档。建议在玩家下线时补录。'),
      h('div', { class: 'row records-toolbar' }, mapSel, h('div', { class: 'grow' }, searchBox('筛选鱼名', (v) => { q = v.toLowerCase(); rows(); })), saveBtn),
      h('div', { class: 'tbl', style: { maxHeight: '58vh', overflow: 'auto' } }, h('table', {},
        h('thead', {}, h('tr', {}, h('th', { text: '鱼' }), h('th', { text: '现在' }), h('th', { class: 'num', text: '最重' }), h('th', { text: '补录为' }), h('th', { text: '补录重量（克）' }))), tbody))));
  }
  load('');
  return box;
}

// ---------- 技能 ----------
function skills(p, reload, setDirty) {
  const list = p.skills || [];
  const avail = Number(p.available_points ?? 0);
  const unsaved = new Set();
  setDirty(() => unsaved.size > 0);
  const points = h('input', { class: 'input num', style: { width: '100px' }, value: avail, oninput: () => { unsaved.add('points'); } });
  return h('div', { class: 'stack' },
    h('div', { class: 'card', style: { padding: '14px 16px', background: 'var(--surface-2)' } }, h('div', { class: 'row' },
      h('div', { class: 'grow' }, h('b', { text: '未分配的技能点' }), h('div', { class: 'muted small', text: `等级累计获得 ${p.skill_total ?? 0} 点，已经用掉 ${p.skill_spent ?? 0} 点。` })),
      points, btn('保存', async () => {
        const raw = points.value.trim(), value = Number(raw);
        if (!/^\d+$/.test(raw) || value > 1000000) { toast('技能点须为 0 至 1000000 的整数。', 'bad'); points.focus(); return; }
        try { const result = await post('/api/players/skills/points', { uid: p.uid, points: value }); if (points.value.trim() === raw) { points.value = String(result.available_points); unsaved.delete('points'); } toast('已保存，在线玩家立即收到刷新'); } catch (e) { toast(errText(e), 'bad'); }
      }, 'primary sm'))),
    h('p', { class: 'muted small', text: '下面是每种钓法的熟练度（0–100%）。' }),
    h('div', { class: 'grid-2' }, list.map((s) => {
      const v = Number(s.points) || 0;
      const out = h('output', { text: v.toFixed(1) + '%' });
      const range = h('input', { type: 'range', min: 0, max: 100, step: 0.1, value: v, oninput: (e) => { out.textContent = Number(e.target.value).toFixed(1) + '%'; save.hidden = false; unsaved.add(s.skill_id); } });
      const save = btn('保存', async () => {
        try { await post('/api/players/skills/update', { uid: p.uid, skill_id: s.skill_id, points: +range.value }); unsaved.delete(s.skill_id); toast(`已保存「${s.name}」，在线玩家立即收到刷新`); save.hidden = true; } catch (e) { toast(errText(e), 'bad'); }
      }, 'primary sm', { hidden: true });
      return h('div', { class: 'card skill-card', style: { padding: '12px 14px', background: 'var(--surface-2)' } },
        h('div', { class: 'row', style: { justifyContent: 'space-between' } }, h('b', { class: 'skill-name', text: s.name }), save),
        h('div', { class: 'range-row', style: { marginTop: '6px' } }, range, out));
    })),
    h('div', {}, btn('全部清零', async () => {
      if (!(await confirmDialog({ title: '把所有熟练度清零？', desc: '所有钓法的熟练度都会变成 0%，不能恢复。', confirm: '清零', danger: true }))) return;
      try { for (const s of list) await post('/api/players/skills/update', { uid: p.uid, skill_id: s.skill_id, points: 0 }); unsaved.clear(); toast('已清零'); reload(); } catch (e) { toast(errText(e), 'bad'); }
    }, 'danger sm')));
}

// ---------- 邮箱 ----------
function mail(p, reload) {
  const rows = p.mails || [];
  return h('div', { class: 'stack' },
    h('div', { class: 'row' }, h('span', { class: 'grow muted small', text: '玩家自己删不掉领不出附件的坏邮件，可以在这里删掉后重新发。' }), btn('给他发邮件', () => navigate('mail', p.uid), 'primary sm', { icon: 'mail' })),
    rows.length ? h('div', { class: 'tbl', style: { maxHeight: '62vh', overflow: 'auto' } }, h('table', {},
      h('thead', {}, h('tr', {}, h('th', { text: '内容' }), h('th', { text: '类型' }), h('th', { text: '状态' }), h('th', { text: '时间' }), h('th', {}))),
      h('tbody', {}, rows.map((m) => h('tr', {},
        h('td', { style: { whiteSpace: 'normal', maxWidth: '420px' } }, h('b', { text: m.text || '（只有附件）' }), h('span', { class: 'sub mono', text: '邮件 #' + m.id })),
        h('td', { text: m.kind ? '带附件' : '文字' }),
        h('td', {}, m.read ? badge('已读') : badge('未读', 'accent')),
        h('td', { class: 'small muted', title: timeText(m.created_ts), text: relTime(m.created_ts) }),
        h('td', {}, btn('删除', async () => {
          if (!(await confirmDialog({ title: `删除邮件 #${m.id}？`, desc: `${p.name}（UID ${p.uid}） · ${m.text || '只有附件'}。不能恢复。`, confirm: '删除', danger: true }))) return;
          try { await post('/api/players/mail/delete', { uid: p.uid, ids: [m.id] }); toast('已删除'); setTimeout(reload, 300); } catch (e) { toast(errText(e), 'bad'); }
        }, 'ghost sm'))))))) : h('div', { class: 'card' }, empty('邮箱是空的',
      '这个玩家没有收到过任何邮件，自己也没发过。用上面的表单可以直接给他发一封。')));
}
