#!/usr/bin/env node
// 后台入口结构自检。
//
// 历史：2026-11-26 批次 0 起，SPA 与旧后台曾并行于同一目录（spa.html + index.html）。
// 2026-11-27 批次 6 完成切换：**index.html 现在是 SPA 入口**，旧后台原样留存为
// legacy.html（410 KB 单文件，仅作对照与回退，不再演进、不再被任何测试读取）。
//
// 本测试因此从「两套并存」改成「入口正确 + 旧后台冻结 + 模块完整」三件事：
//   - 入口必须是 SPA 空壳，不能又变回那个 410 KB 的单文件；
//   - legacy.html 必须还在（回退要用），且必须与 SPA 无耦合；
//   - app/ 下的模块与资源必须齐全，且入口引用的每个文件都真的存在。
//
// 这是「骨架未破」的守门测试，不涉及任何业务功能，跑得很快。
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const WEB = path.join(__dirname, 'web');
const readText = (rel) => fs.readFileSync(path.join(WEB, rel), 'utf8');

// 与 web/app/core.js:81 完全一致的推导，用来校验入口的 API 前缀。
function baseOf(pathname) {
  return pathname.replace(/\/[^/]*$/, '').replace(/\/+$/, '').endsWith('/admin') ? '/admin' : '';
}

test('入口是 SPA 空壳，旧后台已改名 legacy.html 留存', () => {
  assert.ok(fs.existsSync(path.join(WEB, 'index.html')), '入口 index.html 必须存在');
  assert.ok(fs.existsSync(path.join(WEB, 'legacy.html')), '旧后台必须以 legacy.html 留存（回退要用）');
  // 切换后不该再有第二个 SPA 入口：两个空壳会让人不知道该改哪个。
  assert.ok(!fs.existsSync(path.join(WEB, 'spa.html')), 'spa.html 应已在切换时并回 index.html');
});

test('index.html 是极简空壳，只挂 app/ 模块', () => {
  const html = readText('index.html');
  assert.ok(html.includes('<div id="app">'), 'SPA 需要 #app 挂载点');
  assert.ok(html.includes('app/main.js'), 'SPA 需要加载 app/main.js');
  assert.ok(html.length < 2000, `SPA 入口应是空壳，实得 ${html.length} 字符`);
  assert.ok(!html.includes('view-ecology'), 'SPA 空壳不该含业务视图');
});

test('legacy.html 仍是完整单文件，且不与 SPA 抢入口', () => {
  const html = readText('legacy.html');
  const lines = html.split('\n').length;
  assert.ok(lines > 3000, `旧后台应有数千行，实得 ${lines} 行`);
  assert.ok(html.includes('view-ecology'), '旧后台应含生态视图');
  assert.ok(html.includes('view-weather'), '旧后台应含天气视图');
  assert.ok(html.includes('diel'), '旧后台应含昼夜逻辑');
  // 旧后台是自包含的：不引用 app/ 下的模块，否则它就不能在 SPA 出问题时单独回退。
  assert.ok(!/["']app\/[a-z-]+\.js/.test(html), 'legacy.html 不该引用 SPA 模块');
});

test('index.html 引用的资源全部存在', () => {
  const html = readText('index.html');
  const refs = [...html.matchAll(/(?:href|src)="(app\/[^"]+)"/g)].map((m) => m[1]);
  assert.ok(refs.length >= 5, `应至少引用 5 个 app/ 资源，实得 ${refs.length}`);
  for (const r of refs) {
    assert.ok(fs.existsSync(path.join(WEB, r)), `index.html 引用的 ${r} 不存在`);
  }
});

test('SPA 全部页面模块存在', () => {
  const pages = ['dashboard', 'players', 'shop', 'rules', 'waters', 'hooks', 'bait', 'mail', 'tuning',
    'docs', 'audit', 'zones', 'event-gifts', 'sync', 'weather',
    'ecology', 'ecology-eco', 'ecology-econ', 'ecology-weight', 'bait-picker'];
  for (const p of pages) {
    const f = path.join(WEB, 'app', 'pages', `${p}.js`);
    assert.ok(fs.existsSync(f), `缺少 SPA 页面模块 pages/${p}.js`);
  }
});

test('SPA 公共模块存在', () => {
  for (const m of ['main.js', 'core.js', 'labels.js', 'markdown.js', 'widgets.js', 'app.css', 'theme-aerial.css']) {
    assert.ok(fs.existsSync(path.join(WEB, 'app', m)), `缺少 SPA 公共模块 app/${m}`);
  }
});

test('入口的 API 前缀推导正确', () => {
  const cases = [
    ['/admin/', '/admin', 'launcher 统一入口（目录）'],
    ['/admin/index.html', '/admin', 'launcher 统一入口（显式文件）'],
    ['/', '', '独立端口 → 根'],
    ['/index.html', '', '独立端口 → 显式文件'],
  ];
  for (const [pathname, want, note] of cases) {
    assert.equal(baseOf(pathname), want, `${pathname} 应推导出 ${JSON.stringify(want)}（${note}）`);
  }
});

test('core.js 的 BASE 推导与测试里的复现一致', () => {
  const core = readText('app/core.js');
  const m = core.match(/export const BASE\s*=\s*(.+?);/);
  assert.ok(m, 'core.js 应导出 BASE');
  // 上游若改了推导方式，这里会失败，提醒同步本测试的 baseOf()。
  assert.ok(/replace\(\/\\\/\[\^\/\]\*\$\/, ''\)/.test(m[1]), 'core.js 的 BASE 推导方式已变，需同步更新本测试');
  assert.ok(m[1].includes("endsWith('/admin')"), "core.js 的 BASE 仍依赖 '/admin' 判断");
});

test('SPA 页面按 hash 路由加载，不依赖服务端重写', () => {
  const main = readText('app/main.js');
  assert.ok(/location\.hash/.test(main) || /parseHash/.test(main), 'SPA 应使用 hash 路由');
  const core = readText('app/core.js');
  assert.ok(core.includes('function parseHash') || core.includes('export function parseHash'), 'core.js 应提供 parseHash');
});

test('.cjs 测试不再读 legacy.html，也不再引用已删除的 spa.html', () => {
  const files = fs.readdirSync(__dirname).filter((f) => f.endsWith('.cjs'));
  assert.ok(files.length >= 20, `应至少有 20 个 cjs 测试，实得 ${files.length}`);
  const legacyReaders = files.filter((f) => {
    if (f === path.basename(__filename)) return false; // 本测试的文案里含该字面量
    const src = fs.readFileSync(path.join(__dirname, f), 'utf8');
    return src.includes('web/legacy.html') || src.includes("'legacy.html'");
  });
  assert.deepEqual(legacyReaders, [], `不该有测试读 legacy.html（旧后台已冻结）：${legacyReaders.join(', ')}`);
  const spaReaders = files.filter((f) => {
    if (f === path.basename(__filename)) return false;
    const src = fs.readFileSync(path.join(__dirname, f), 'utf8');
    return src.includes('spa.html');
  });
  assert.deepEqual(spaReaders, [], `spa.html 已并回 index.html，不该再被引用：${spaReaders.join(', ')}`);
});