const {test}=require('node:test');
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 「关于」页把服务端随包带的 README 渲染成 HTML。
//
// 迁移时的取舍（批次 6）：
//   旧后台有个 `#aboutReadme` 容器 + `#view-about` 视图，SPA 不再是「一堆预置 div
//   里切换显隐」，而是按路由往 #app 里重建节点，容器没有 id 也没有 view-* 类名。
//   所以断言只能落在「渲染产物长什么样」上——标题、表格、链接改写、代码块，
//   这几条才是真会坏的东西；「必须有个 id 叫 aboutReadme」不是行为。
//
// 链接改写这条尤其要留：markdown.js 会把 README 里的相对路径
// （`docs/deploy.md`）改写成 GitHub blob 绝对地址，否则点进去是 404。
// 这是本项目自己的约定，和用哪套 UI 无关。
const README='# 测试项目\n\n这是 **README** 内容。\n\n| 项目 | 地址 |\n|---|---|\n| 部署 | [文档](docs/deploy.md) |\n\n```powershell\nbin\\launcher.exe\n```';

function serve(readme){
  return http.createServer((req,res)=>{
    const url=new URL(req.url,'http://fixture');
    const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
    const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
    if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
    if(pathname==='/api/about')return json({content:readme});
    if(pathname==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
    if(pathname.startsWith('/api/'))return json({});
    if(sendAsset(res,url.pathname))return;
    res.writeHead(404);res.end('');
  });
}

test('about page renders embedded README markdown',async()=>{
 const server=serve(README);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/about`,{waitUntil:'networkidle'});
  // 渲染产物在 .work .md 里（markdown.js 的出口），入口不再有 #aboutReadme。
  const md=page.locator('.work .md');
  await md.locator('h1').waitFor();
  assert.equal(await md.locator('h1').textContent(),'测试项目');
  assert.equal(await md.locator('table').count(),1,'表格语法应渲染成 <table>');
  assert.equal(await md.locator('strong').first().textContent(),'README');
  // 相对路径必须改写成 GitHub blob 绝对地址，否则链接点进去 404。
  assert.equal(await md.locator('a').getAttribute('href'),
    'https://github.com/OpenFisherCommunity/rf4_server_simulator/blob/main/docs/deploy.md');
  assert.equal(await md.locator('pre code').textContent(),'bin\\launcher.exe');
  // 主操作行里的仓库入口也要在：这是「关于」页唯一的行动点。
  assert.ok(await page.locator('.work a.btn[href*="github.com/OpenFisherCommunity"]').count()>0,'应有跳 GitHub 的按钮');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});

// 读不到文档时的降级：不能是空白页，也不能把「读取失败」之后留成裸 null。
// 从源码直接跑时 /api/about 常返回非 2xx，这是最常见的真实场景。
test('about page explains itself when the document is missing',async()=>{
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
  const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
  if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
  if(pathname==='/api/about'){res.statusCode=404;return json({error:'about.md 不存在'});}
  if(pathname==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
  if(pathname.startsWith('/api/'))return json({});
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/about`,{waitUntil:'networkidle'});
  const md=page.locator('.work .md-wrap');
  await md.waitFor();
  const text=await md.innerText();
  assert.match(text,/读取失败/,'应说明读取失败，而不是留空');
  assert.match(text,/about\.md 不存在/,'应把服务端给的原因带出来，方便判断是缺文件还是权限问题');
  assert.match(text,/源码/,'应说明「自己从源码运行没有这个文件是正常的」，否则用户会以为坏了');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});