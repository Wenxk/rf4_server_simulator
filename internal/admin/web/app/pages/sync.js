// 参数同步：把后台所有可调项打成一个 JSON 包，拿到另一台机器导入即可复现同一套手感。
//
// 从旧后台 #view-sync 移植。这是 SPA 相对旧后台**唯一缺的一页**（其余 12 个视图
// 都已迁完），所以在批次 6 切换前必须补上，否则切换那一刻运营就丢了搬参数的能力。
//
// 导入分两步：先 dry_run=1 预演拿到「将更新/将新增」，用户确认后再真导。
// 后端按业务主键 UPSERT、单事务提交，失败整体回滚（见 internal/admin/playstyle.go）。
import { h, api, pageHead, work, ib, bullets, hint, btn, empty, toast, errText, fmt, getToken } from '../core.js';

const FMT = 'rf4-playstyle';

// 包里到底有什么、没有什么，必须写在页面上。
// 「不含玩家存档」这条尤其重要：有人会以为这是全量备份，导到另一台机器后发现玩家没了。
function exportNote() {
  return h('div', { class: 'ps-note' },
    h('p', {},
      '包里含 ', h('b', { text: '13 张表' }),
      '：游戏参数（89 条开关与数值）、鱼种生态覆盖、饵料规则、自动抽取开关、权重档位、',
      '经济四档、咬口缩放、区域名册、货架覆盖。'),
    h('p', {},
      h('b', { text: '不含' }), '玩家存档、后台账号密码、网关登录令牌，也不含',
      h('b', { text: '派生表' }),
      '（fish_ecology 会按资产重灌、hook_bite_profile 从静态库派生）——',
      '那几张导过去会被服务重新覆盖，反而造成半应用。'));
}

// 预演/导入结果表：包内 / 将更新 / 将新增三列。
// 旧后台把「将更新」「将新增」涂成黄绿两色，这里保留那个语义但走主题变量。
function resultTable(data) {
  const rows = Array.isArray(data.tables) ? data.tables : [];
  if (!rows.length) return empty('包里没有可导入的表',
    '这个文件里一张认识的表都没有 —— 可能选错了文件（比如导出的是一份存档备份而不是参数包），也可能文件被别的程序改过。重新导出一份再试。');
  return h('div', { class: 'tbl' }, h('table', { class: 'ps-table' },
    h('thead', {}, h('tr', {},
      h('th', { text: '表' }), h('th', { class: 'num', text: '包内' }),
      h('th', { class: 'num', text: '将更新' }), h('th', { class: 'num', text: '将新增' }))),
    h('tbody', {}, ...rows.map((t) => h('tr', {},
      h('td', { class: 'mono', text: t.table }),
      h('td', { class: 'num mono', text: fmt(t.rows || 0) }),
      h('td', { class: 'num mono' }, t.updated ? h('b', { class: 'ps-upd', text: fmt(t.updated) }) : '0'),
      h('td', { class: 'num mono' }, t.inserted ? h('b', { class: 'ps-ins', text: fmt(t.inserted) }) : '0'))))));
}

function noteBlock(data) {
  const notes = Array.isArray(data.notes) ? data.notes : [];
  const warns = Array.isArray(data.warnings) ? data.warnings : [];
  if (!notes.length && !warns.length) return null;
  return h('div', { class: 'ps-msgs' },
    ...warns.map((w) => h('div', { class: 'ps-warn' }, h('b', { text: '⚠ ' }), w)),
    ...notes.map((n) => h('div', { class: 'ps-info' }, h('b', { text: '· ' }), n)));
}

export default async function syncPage(params, scope) {
  const head = pageHead('参数同步', '导出当前手感，在另一台机器导入即可复现同一套玩法参数；先预演再导入。',
    [['13', '张参数表'], ['1 秒', '导入后生效'], ['不含', '玩家存档']]);

  const exportStatus = h('div', { class: 'muted small', id: 'syncExportStatus' });
  const importStatus = h('div', { id: 'syncImportStatus', class: 'muted small' });
  const previewBox = h('div', { class: 'table-wrap', id: 'syncPreview' });
  const fileInput = h('input', { type: 'file', id: 'syncFile', accept: '.json,application/json' });
  const previewBtn = btn('预演', () => doPreview(), 'alt', { id: 'syncPreviewBtn' });
  const importBtn = btn('导入', () => doImport(), 'good', { id: 'syncImportBtn' });

  // 读文件 + 校验 format。两个按钮共用，避免预演走一套解析、导入走另一套。
  async function readPkg() {
    const f = fileInput.files && fileInput.files[0];
    if (!f) throw new Error('先选择一个 .json 参数包');
    const text = await f.text();
    let pkg;
    try { pkg = JSON.parse(text); } catch (e) { throw new Error('文件不是合法 JSON'); }
    if (!pkg || pkg.format !== FMT) throw new Error('不是参数包（format=' + ((pkg && pkg.format) || '空') + '）');
    return { pkg, name: f.name };
  }

  async function doExport() {
    exportStatus.textContent = '正在打包…';
    try {
      // 导出要拿 Content-Disposition 里的文件名，不能自己拼：后端带的是
      // playstyle-YYYYMMDD-HHMM.json，自己拼日期会在跨时区时和服务器不一致。
      // 这里走原生 fetch 而不是 api()，原因是 api() 会把响应当 JSON 解析，
      // 而导出是附件下载，需要 blob 与响应头。
      const r = await fetch(location.pathname.replace(/\/[^/]*$/, '') + '/api/playstyle/export', {
        headers: { Authorization: 'Bearer ' + getToken() },
      });
      if (!r.ok) {
        let msg = 'HTTP ' + r.status;
        try { const j = await r.json(); if (j.error) msg = j.error; } catch (e) {}
        throw new Error(msg);
      }
      const blob = await r.blob();
      const cd = r.headers.get('Content-Disposition') || '';
      const m = /filename="([^"]+)"/.exec(cd);
      const name = m ? m[1] : ('playstyle-' + new Date().toISOString().slice(0, 10) + '.json');
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 4000);
      exportStatus.replaceChildren('已导出 ', h('b', { text: name }), `（${(blob.size / 1024).toFixed(0)} KB）。拿到目标机器后进本页「导入参数包」。`);
      toast('参数包已下载');
    } catch (e) {
      exportStatus.textContent = '导出失败：' + errText(e);
      toast('导出失败：' + errText(e), 'bad');
    }
  }

  async function doPreview() {
    importStatus.textContent = '正在预演…';
    previewBox.replaceChildren();
    try {
      const { pkg, name } = await readPkg();
      const data = await api('/api/playstyle/import?dry_run=1', { method: 'POST', body: JSON.stringify(pkg) });
      if (!scope.isCurrent()) return;
      previewBox.replaceChildren(...[resultTable(data), noteBlock(data)].filter(Boolean));
      importStatus.replaceChildren(h('b', { text: name }), ` 预演完成（digest ${data.digest || ''}）。确认无误再点「导入」。`,
        h('div', { class: 'muted small', text: '预演不会写入任何数据。' }));
      toast('预演完成，未写入');
    } catch (e) {
      importStatus.textContent = '预演失败：' + errText(e);
      toast('预演失败：' + errText(e), 'bad');
    }
  }

  async function doImport() {
    try {
      const { pkg, name } = await readPkg();
      // 先预演一次，把影响面摆给用户看；真导还要再点一次确认。
      // 只拿 importBtn 的点击当确认是不够的——必须让用户看到「将更新 N 行」再决定。
      importStatus.textContent = '正在预演…';
      const pre = await api('/api/playstyle/import?dry_run=1', { method: 'POST', body: JSON.stringify(pkg) });
      if (!scope.isCurrent()) return;
      previewBox.replaceChildren(...[resultTable(pre), noteBlock(pre)].filter(Boolean));
      const upd = (pre.tables || []).reduce((a, t) => a + (t.updated || 0), 0);
      const ins = (pre.tables || []).reduce((a, t) => a + (t.inserted || 0), 0);
      const ok = window.confirm(
        `即将导入 ${name}\n\n将更新 ${upd} 行、新增 ${ins} 行，覆盖当前参数配置。\n` +
        '（玩家存档、后台账号、登录令牌不受影响）\n\n确定继续？');
      if (!ok) { importStatus.textContent = '已取消，未写入。'; return; }
      importStatus.textContent = '正在导入…';
      const data = await api('/api/playstyle/import', { method: 'POST', body: JSON.stringify(pkg) });
      if (!scope.isCurrent()) return;
      previewBox.replaceChildren(...[resultTable(data), noteBlock(data)].filter(Boolean));
      importStatus.replaceChildren(
        h('b', { class: 'ps-done', text: '导入完成' }),
        `（digest ${data.digest || ''}）。`,
        // 这一句不能省：不推 reload_tunables，后台显示已导入、实际判鱼还在用旧值。
        data.reloaded ? '已通知主服重建配置缓存，约 1 秒后生效。' : '未推送重载指令，请重启主服或等缓存过期。');
      toast('导入完成');
    } catch (e) {
      importStatus.textContent = '导入失败：' + errText(e);
      toast('导入失败：' + errText(e), 'bad');
    }
  }

  const exportCard = h('div', { class: 'card' },
    h('div', { class: 'section-head' },
      h('div', {}, h('h2', { text: '导出当前玩法参数' }),
        h('p', { text: '把后台所有可调项打成一个 JSON 包，拿到另一台机器导入即可复现同一套手感' })),
      btn('下载参数包', doExport, 'good', { id: 'syncExportBtn' })),
    h('div', { class: 'card-pad' }, exportNote(), exportStatus));

  const importCard = h('div', { class: 'card', style: { marginTop: '16px' } },
    h('div', { class: 'section-head' },
      h('div', {}, h('h2', { text: '导入参数包' }),
        h('p', { text: '按业务主键覆盖，单事务提交；先预演再导入' }))),
    h('div', { class: 'card-pad' },
      h('div', { class: 'ps-toolbar' }, fileInput, previewBtn, importBtn),
      importStatus,
      h('div', { style: { marginTop: '12px' } }, previewBox)));

  const aside = [
    ib('这一页管什么', null, bullets([
      '换机器、换测试环境时，把一套手感原样搬过去。',
      '导出只是一次读操作；导入会真的写库，所以强制先预演。',
      '派生表与玩家数据不在包里，导过去也不会被误覆盖。',
    ])),
    ib('导入后为什么还要等', null,
      hint('生态覆盖与权重档位有缓存，只有主服收到 reload_tunables 才重建。正常情况 1 秒内生效；若提示「未推送重载指令」，说明主服没在跑，需要重启。')),
  ];

  return [head, work([exportCard, importCard], aside)];
}