const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),os=require('node:os');
const {chromium}=require('playwright');

// 「参数同步」页：导出 + 预演 → 导入。
// 四条必须守住的：
//   1. 导出必须真发请求并落成文件（文件名取 Content-Disposition，不能靠猜）；
//   2. 预演必须打 dry_run=1，且不能发真导请求；
//   3. 确认框取消后不能写库；
//   4. 确认后要提示缓存已重载（否则用户以为生效了，实际还在用旧值）。
test('playstyle sync page previews then imports with confirmation',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
 const previewCalls=[],importCalls=[],exportCalls=[];
 const pkg={format:'rf4-playstyle',format_version:1,digest:'abc123',
  tables:{tunables:[{key:'bite_probability',value:'0.5'}]}};
 const preview={ok:true,dry_run:true,digest:'abc123',
  notes:['fish_bait_override：目标库多出列 [x]，保持现值不覆盖'],
  warnings:['目标库迁移版本 84 高于包内记录的 83'],
  tables:[{table:'tunables',rows:1,updated:1,inserted:0},
          {table:'fish_bait_override',rows:1,updated:0,inserted:1}]};
 const done={ok:true,dry_run:false,digest:'abc123',reloaded:true,notes:[],warnings:[],
  tables:[{table:'tunables',rows:1,updated:1,inserted:0},
          {table:'fish_bait_override',rows:1,updated:0,inserted:1}]};

 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(v,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(v));};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  // 导出端点：必须带 Content-Disposition，前端靠它取文件名。
  if(key==='/api/playstyle/export'){
   exportCalls.push(url.pathname);
   res.writeHead(200,{
    'Content-Type':'application/json; charset=utf-8',
    'Content-Disposition':'attachment; filename="playstyle-20261124-1935.json"',
   });
   return res.end(JSON.stringify(pkg));
  }
  if(key==='/api/playstyle/import'&&req.method==='POST'){
   let body='';req.on('data',c=>body+=c);
   req.on('end',()=>{
    if(url.searchParams.get('dry_run')==='1'){previewCalls.push(body);return json(preview);}
    importCalls.push(body);json(done);
   });
   return;
  }
  if(key==='/api/audit')return json({items:[]});
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/,''));
  const file2=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file2) && fs.statSync(file2).isFile()){
   const ext=path.extname(file2).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file2));return;
  }
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
 });

 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1600,height:1000}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture')});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#/sync`,{waitUntil:'networkidle'});

  // 导航里有入口，页面标题对得上。
  // SPA 的左栏是 <a href="#sync">，旧后台是 <button data-view>，锚点要跟着换。
  assert.ok(await page.locator('#rail a[data-route="sync"]').count()>0,'导航缺少参数同步入口');
  await page.locator('#syncExportBtn').waitFor({state:'attached',timeout:8000});
  assert.match(await page.locator('#title').innerText(),/参数同步/);

  // 导出：真发请求 + 文件名取自 Content-Disposition + 内容就是包本体。
  // 这条以前完全没测——导出按钮是新增 UI，坏了也不会有人发现。
  const dl=page.waitForEvent('download',{timeout:8000});
  await page.locator('#syncExportBtn').click();
  const download=await dl;
  assert.equal(exportCalls.length,1,'导出未请求 /api/playstyle/export');
  assert.equal(download.suggestedFilename(),'playstyle-20261124-1935.json',
   '导出文件名没取 Content-Disposition');
  const saved=path.join(os.tmpdir(),`ps_dl_${process.pid}.json`);
  await download.saveAs(saved);
  const roundtrip=JSON.parse(fs.readFileSync(saved,'utf8'));
  assert.equal(roundtrip.format,'rf4-playstyle','导出的不是本工具包');
  fs.unlinkSync(saved);
  assert.match(await page.locator('#syncExportStatus').innerText(),/已导出/,'导出后缺少完成提示');

  // 选一份包
  const file=path.join(os.tmpdir(),`ps_ui_${process.pid}.json`);
  fs.writeFileSync(file,JSON.stringify(pkg));
  await page.locator('#syncFile').setInputFiles(file);

  // 预演：打 dry_run=1，渲染表格，不发真导
  await page.locator("#syncPreviewBtn").click();
  await page.waitForTimeout(600);
  assert.equal(previewCalls.length,1,'预演未请求 dry_run=1');
  assert.equal(importCalls.length,0,'预演阶段不应发真导请求');
  const tableText=await page.locator('#syncPreview').innerText();
  assert.match(tableText,/tunables/,'预演表格缺少表名');
  assert.match(tableText,/fish_bait_override/,'预演表格缺少第二张表');
  assert.match(tableText,/将更新/,'预演表格缺少「将更新」列');
  assert.match(tableText,/目标库迁移版本 84/,'预演未显示版本警告');
  assert.match(tableText,/多出列/,'预演未显示列差异备注');

  // 取消确认框：不能写库
  page.once('dialog',d=>d.dismiss());
  await page.locator("#syncImportBtn").click();
  await page.waitForTimeout(500);
  assert.equal(importCalls.length,0,'取消确认框后不应发真导请求');
  assert.match(await page.locator('#syncImportStatus').innerText(),/已取消/);

  // 确认：真导 + 提示已重载
  page.once('dialog',d=>d.accept());
  await page.locator("#syncImportBtn").click();
  await page.waitForTimeout(800);
  assert.equal(importCalls.length,1,'确认后应发真导请求');
  const status=await page.locator('#syncImportStatus').innerText();
  assert.match(status,/导入完成/,'导入后缺少完成提示');
  assert.match(status,/重建配置缓存|1 秒后生效/,'导入后未提示缓存已重载');

  fs.unlinkSync(file);
  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});