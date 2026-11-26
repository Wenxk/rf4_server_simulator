const {test}=require('node:test');
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 「更新日志」页渲染发布包里的 发布说明.md。
//
// 迁移时的取舍（批次 6）：旧后台是 `#releaseBody` + `#view-release`；
// SPA 同样是 docPage() 产出的 .work .md，没有单独 id。断言落在渲染产物上。
// 另加一条旧测试没有的：文件名要显示出来——服务端同时支持 发布说明.md /
// RELEASE_NOTES.md 等多个名字，不显示文件名的话，用户看到内容对不上时分不清
// 是读了哪个文件。
const NOTE='# 发布说明\n\n- 修复 **登录**\n- 来自发布包内的 发布说明.md';

test('release notes page renders local package markdown',async()=>{
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
  const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
  if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
  if(pathname==='/api/release-notes')return json({filename:'发布说明.md',content:NOTE});
  if(pathname==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
  if(pathname.startsWith('/api/'))return json({});
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/release`,{waitUntil:'networkidle'});
  const md=page.locator('.work .md');
  await md.locator('h1').waitFor();
  assert.equal(await md.locator('h1').textContent(),'发布说明');
  assert.equal(await md.locator('strong').first().textContent(),'登录');
  assert.equal(await md.locator('li').count(),2);
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});

// 没有发布包文件时（源码直跑）要给出可读的空态，而不是「没有更新日志」四个字。
test('release notes page falls back to a readable empty state',async()=>{
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
  const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
  if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
  if(pathname==='/api/release-notes')return json({content:''});
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
  await page.goto(`${base}/#/release`,{waitUntil:'networkidle'});
  const box=page.locator('.work .empty');
  await box.waitFor();
  const text=await box.innerText();
  assert.match(text,/没有更新日志/);
  // 空态不能只丢一句「没有」，得说清「为什么没有、去哪看」。
  assert.ok(text.length>8,`空态文案过短，用户判断不了状况：${JSON.stringify(text)}`);
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});