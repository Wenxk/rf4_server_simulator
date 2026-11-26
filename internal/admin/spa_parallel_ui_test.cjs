#!/usr/bin/env node
// 后台双平台骨架自检：确认旧后台(web/index.html)与 SPA(web/spa.html + web/app/)
// 都在、SPA 的模块依赖完整、两套的 API 前缀推导各自正确。
//
// 这是「骨架未破」的守门测试，不涉及任何业务功能，跑得很快。
// 用法：node internal/admin/spa_parallel_ui_test.cjs
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const WEB = path.join(__dirname, 'web');
const readText = (rel) => fs.readFileSync(path.join(WEB, rel), 'utf8');

// 与 web/app/core.js:81 完全一致的推导，用来校验两套入口各自的 API 前缀。
function baseOf(pathname) {
  return pathname.replace(/\/[^/]*$/, '').replace(/\/+$/, '').endsWith('/admin') ? '/admin' : '';
}

test('两套后台入口并存，且旧后台未改名为 legacy', () => {
  assert.ok(fs.existsSync(path.join(WEB, 'index.html')), '旧后台 index.html 必须存在');
  assert.ok(fs.existsSync(path.join(WEB, 'spa.html')), '新 SPA spa.html 必须存在');
  assert.ok(!fs.existsSync(path.join(WEB, 'legacy.html')), '不该出现 legacy.html（本方案不改名）');
});

test('旧后台仍是完整单文件，不是被 SPA 空壳替换', () => {
  const html = readText('index.html');
  const lines = html.split('\n').length;
  assert.ok(lines > 3000, `旧后台应有数千行，实得 ${lines} 行`);
  assert.ok(html.includes('view-ecology'), '旧后台应含生态视图');
  assert.ok(html.includes('view-weather'), '旧后台应含天气视图');
  assert.ok(html.includes('diel'), '旧后台应含昼夜逻辑');
});

test('SPA 入口是极简空壳，只挂 app/ 模块', () => {
  const html = readText('spa.html');
  assert.ok(html.includes('<div id="app">'), 'SPA 需要 #app 挂载点');
  assert.ok(html.includes('app/main.js'), 'SPA 需要加载 app/main.js');
  assert.ok(html.length < 2000, `SPA 入口应是空壳，实得 ${html.length} 字符`);
  assert.ok(!html.includes('view-ecology'), 'SPA 空壳不该含业务视图');
});

test('spa.html 引用的资源全部存在', () => {
  const html = readText('spa.html');
  const refs = [...html.matchAll(/(?:href|src)="(app\/[^"]+)"/g)].map((m) => m[1]);
  assert.ok(refs.length >= 5, `应至少引用 5 个 app/ 资源，实得 ${refs.length}`);
  for (const r of refs) {
    assert.ok(fs.existsSync(path.join(WEB, r)), `spa.html 引用的 ${r} 不存在`);
  }
});

test('SPA 全部页面模块存在', () => {
  const pages = ['dashboard', 'players', 'shop', 'rules', 'waters', 'hooks', 'bait', 'mail', 'tuning', 'docs', 'audit', 'zones', 'event-gifts'];
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

test('两套入口的 API 前缀推导各自正确', () => {
  const cases = [
    ['/admin/', '/admin', 'launcher 统一入口 → 旧后台'],
    ['/admin/index.html', '/admin', 'launcher 统一入口 → 旧后台显式文件'],
    ['/admin/spa.html', '/admin', 'launcher 统一入口 → 新 SPA'],
    ['/spa.html', '', '独立端口 → 新 SPA'],
    ['/', '', '独立端口 → 根'],
  ];
  for (const [pathname, want, note] of cases) {
    assert.equal(baseOf(pathname), want, `${pathname} 应推导出 ${JSON.stringify(want)}（${note}）`);
  }
  // 同一目录下两个入口必须得到相同前缀，否则旧后台或新 SPA 会打错端口。
  assert.equal(baseOf('/admin/spa.html'), baseOf('/admin/'), '同目录两入口的 API 前缀必须一致');
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

test('旧后台的 .cjs 测试仍读 index.html（本方案不改测试路径）', () => {
  const files = fs.readdirSync(__dirname).filter((f) => f.endsWith('.cjs'));
  assert.ok(files.length >= 20, `应至少有 20 个 cjs 测试，实得 ${files.length}`);
  const readers = files.filter((f) => {
    const src = fs.readFileSync(path.join(__dirname, f), 'utf8');
    return src.includes("web/index.html") || src.includes("web', 'index.html");
  });
  assert.ok(readers.length >= 10, `应有一批测试读 index.html，实得 ${readers.length}`);
  // 排除自身：本测试的断言文案里出现了 legacy.html 字面量。
  const legacyReaders = files.filter((f) => {
    if (f === path.basename(__filename)) return false;
    const src = fs.readFileSync(path.join(__dirname, f), 'utf8');
    return src.includes('legacy.html');
  });
  assert.equal(legacyReaders.length, 0, `不该有测试读 legacy.html，实得: ${legacyReaders.join(', ')}`);
});