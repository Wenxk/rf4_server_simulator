// .cjs 测试共用的「前端从哪里来」加载器。
//
// 背景：2026-11-27 批次 6 切换后，index.html 是 ~516 字节的 SPA 空壳，
// 真正的界面在 web/app/ 下的模块里（main.js + pages/*.js + 两份 CSS + 字体/照片）。
// 于是每个测试都要做两件以前不需要的事：
//
//   1. **静态资源按真实文件返回**。以前 index.html 是单文件，`page.route('**/*')`
//      把非 API 请求一律回成 `{}` 也无所谓；现在模块加载拿到的若是 JSON，
//      `import` 直接失败、整页白屏，测试会以「等不到 #aboutReadme」告终。
//   2. **MIME 要给对**。尤其 .js 必须是 JS MIME，否则浏览器拒绝执行模块；
//      .html 缺 MIME 会触发「Download is starting」——这两个都实际踩过。
//
// 这里只做「读盘 + 猜 MIME + 处理 /admin 前缀」，不含任何业务 fixture：
// 各测试的接口替身差异很大，塞进来只会变成第二个需要维护的假后端。
const fs = require('node:fs');
const path = require('node:path');

const WEB = path.join(__dirname, 'web');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
  '.ttf': 'font/ttf',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.png': 'image/png',
  '.webp': 'image/webp',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.md': 'text/markdown; charset=utf-8',
  '.txt': 'text/plain; charset=utf-8',
};

// 前端入口 HTML（切换后就在 index.html）。
function entryHTML() {
  return fs.readFileSync(path.join(WEB, 'index.html'));
}

// 把请求路径还原成 web/ 下的相对路径。
//   /admin/app/main.js → app/main.js
//   /app/main.js       → app/main.js
//   /admin/            → index.html
//   /                  → index.html
// icons/ 与 maps/ 是运行时从 ref/ 取的真实素材，测试里不存在，
// 交给调用方按原文返回占位（返回 null）。
function toRel(pathname) {
  let p = pathname.replace(/^\/admin(?=\/|$)/, '');
  if (p === '' || p === '/') return 'index.html';
  p = p.replace(/^\/+/, '');
  if (p === '') return 'index.html';
  // 目录请求给 index.html
  if (p.endsWith('/')) return p + 'index.html';
  return p;
}

// 读一个资源；读不到或路径越界返回 null。
// 越界检查不能省：`..%2f` 之类的路径会把 fs 带到 web/ 外面去。
function readAsset(rel) {
  if (!rel) return null;
  const full = path.resolve(WEB, rel);
  const root = path.resolve(WEB);
  if (full !== root && !full.startsWith(root + path.sep)) return null;
  try {
    if (!fs.statSync(full).isFile()) return null;
    return fs.readFileSync(full);
  } catch (e) {
    return null;
  }
}

// ---- 形态一：page.route('**/*') 里用 ----
//
// 命中静态资源就自己 fulfil 并返回 true；不命中返回 false，调用方继续走业务逻辑。
// 注意 route.fulfill 是异步的，这里不必 await——Playwright 保证按调用顺序生效，
// 但调用方必须**同步** return，否则会把同一次请求再 fulfil 一次（后者报错）。
function serveAsset(route, url, opts = {}) {
  const prefix = opts.prefix || '';
  let pathname = url.pathname;
  if (prefix) {
    if (pathname !== prefix && !pathname.startsWith(prefix + '/')) return false;
    pathname = pathname.slice(prefix.length) || '/';
  }
  // 运行时素材（贴图、图标）不在仓库里，测试中一律给空图，别让它变成模块加载失败。
  if (/^\/(icons|maps)\//.test(pathname)) {
    route.fulfill({ status: 200, contentType: 'image/png', body: Buffer.alloc(0) });
    return true;
  }
  const rel = toRel(pathname);
  const body = readAsset(rel);
  if (!body) return false;
  route.fulfill({
    status: 200,
    contentType: MIME[path.extname(rel).toLowerCase()] || 'application/octet-stream',
    body,
  });
  return true;
}

// ---- 形态二：node:http server 里用 ----
//
// 同样是「命中就写完并返回 true」。这类测试起的是真 server，
// 以前非 API 请求一律回 index.html，现在必须换成本函数，否则模块路径全被
// 回成 HTML、整页白屏。
function sendAsset(res, pathname) {
  if (/^\/(icons|maps)\//.test(pathname)) {
    res.writeHead(200, { 'Content-Type': 'image/png' });
    res.end(Buffer.alloc(0));
    return true;
  }
  const rel = toRel(pathname);
  const body = readAsset(rel);
  if (!body) return false;
  res.writeHead(200, { 'Content-Type': MIME[path.extname(rel).toLowerCase()] || 'application/octet-stream' });
  res.end(body);
  return true;
}

module.exports = { WEB, MIME, entryHTML, serveAsset, sendAsset, readAsset, toRel };