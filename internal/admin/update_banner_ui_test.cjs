const {test}=require('node:test');
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 版本更新横幅：/api/me（或 /api/dashboard）带 update 时出现，不带时消失。
//
// 迁移时的取舍（批次 6）：
//   旧后台靠固定 id：`#updateBanner` / `#updateText` / `#updateMirror` /
//   `#updateDownload` / `#updatePreview` / `#updateDismissBtn`。
//   SPA 的横幅只挂在 `#updateSlot` 里，内部全是 `.upd` 里的 a.pill 和 button，
//   一个 id 都没有。硬找 id 只会把「旧布局」钉死在新实现上，所以断言改成
//   「按文案找链接，再验它的 href」——这正是用户点下去会发生的事。
//
// 加速下载这条是重点：国内环境下 GitHub 直链基本下不动，横幅真正有用的是
// gh-proxy 镜像地址。镜像字段拿不到时不能退化成「把 GitHub 直链冒充成加速」。
const MIRROR='https://gh-proxy.com/https://github.com/OpenFisherCommunity/rf4_server_simulator/releases/download/v1.2.0/rf4_server_simulator-v1.2.0-windows-amd64.zip';
const PREVIEW_MIRROR='https://gh-proxy.com/https://github.com/OpenFisherCommunity/rf4_server_simulator/releases/download/preview-20261107T110000Z-abc1234/b.zip';
const UPDATE={available:true,channel:'stable',current_version:'v1.1.0',latest_version:'v1.2.0',
  notes_url:'https://github.com/OpenFisherCommunity/rf4_server_simulator/releases/tag/v1.2.0',
  download_url:'https://github.com/OpenFisherCommunity/rf4_server_simulator/releases/latest',
  download_mirror_url:MIRROR,preview_version:'preview-20261107T110000Z-abc1234',
  preview_download_mirror:PREVIEW_MIRROR};

function serve(update, seen){
  return http.createServer((req,res)=>{
    const url=new URL(req.url,'http://fixture');
    const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
    const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
    if(seen&&req.method==='POST'){let body='';req.on('data',c=>body+=c);req.on('end',()=>{seen.push({path:pathname,body});res.writeHead(204);res.end();});return;}
    if(pathname==='/api/me')return json(update?{username:'admin',item_categories:{},update}:{username:'admin',item_categories:{}});
    if(pathname==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
    if(pathname.startsWith('/api/'))return json({});
    if(sendAsset(res,url.pathname))return;
    res.writeHead(404);res.end('');
  });
}

async function open(update, seen){
  const server=serve(update, seen);
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  const base=`http://127.0.0.1:${server.address().port}/admin`;
  return {server, base};
}

test('update banner renders from /api/me payload and hides without update',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  for(const withUpdate of [true,false]){
   const {server,base}=await open(withUpdate?UPDATE:null);
   try{
    const context=await browser.newContext({viewport:{width:1280,height:900}});
    await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
    const page=await context.newPage(),errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto(`${base}/#/dashboard`,{waitUntil:'networkidle'});
    const slot=page.locator('#updateSlot');
    // 只能等 attached：无更新时 #updateSlot 是个空 div、尺寸为 0，
    // 等 visible 会一直等到超时（这里踩过一次）。
    await slot.waitFor({state:'attached'});
    if(withUpdate){
     await slot.locator('.upd').waitFor();
     const text=await slot.innerText();
     assert.match(text,/有新版本/);
     assert.match(text,/v1\.2\.0/,'应写出新版本号');
     assert.match(text,/当前 v1\.1\.0/,'应同时写出当前版本，否则用户不知道自己从哪升到哪');
     // 加速下载：镜像直链原样落地，不能被换成 GitHub 直链。
     assert.equal(await slot.locator('a', {hasText:'国内加速'}).getAttribute('href'),MIRROR,
       '加速下载应指向 gh-proxy 附件直链');
     assert.equal(await slot.locator('a', {hasText:'GitHub 下载'}).getAttribute('href'),
       'https://github.com/OpenFisherCommunity/rf4_server_simulator/releases/latest');
     // 预览版：地址里必须有 preview 那个构建号，否则等于把人导到 stable 页。
     const pv=slot.locator('a', {hasText:'预览版'});
     assert.equal(await pv.getAttribute('href'),PREVIEW_MIRROR,'预览版应指向 preview 加速直链');
     assert.match(await pv.innerText(),/preview-20261107T110000Z-abc1234/,'按钮上要带预览版构建号');
     assert.equal(await slot.locator('button', {hasText:'跳过这个版本'}).count(),1,'非强制更新应有跳过按钮');
    }else{
     // 等主界面渲染完再看横幅，避免「还没渲染」被误判成「隐藏了」。
     await page.locator('#view').waitFor();
     await page.waitForTimeout(300);
     assert.equal(await slot.locator('.upd').count(),0,'无更新时横幅应隐藏');
    }
    assert.deepEqual(errors,[],withUpdate?'update 渲染不应有页面错误':'无 update 渲染不应有页面错误');
    await context.close();
   }finally{await new Promise(r=>server.close(r));}
  }
 }finally{await browser.close();}
});

test('dismiss posts the version and clears the banner',async()=>{
 const seen=[];
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER});
 const {server,base}=await open(UPDATE,seen);
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/dashboard`,{waitUntil:'networkidle'});
  const slot=page.locator('#updateSlot');
  await slot.locator('.upd').waitFor();
  await slot.locator('button', {hasText:'跳过这个版本'}).click();
  await page.waitForFunction(()=>!document.querySelector('#updateSlot .upd'),null,{timeout:5000});
  const post=seen.find(x=>x.path==='/api/update/dismiss');
  assert.ok(post,'应 POST /api/update/dismiss');
  // 版本要带上：服务端按版本记「跳过了哪个」，不带版本会把所有版本一起隐藏掉。
  assert.equal(JSON.parse(post.body).version,'v1.2.0','跳过时要把具体版本号带给服务端');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});

// 强制更新（forced）不给跳过按钮：能跳过的「强制」等于没有。
// 另外顺手守 safeURL：非 GitHub / gh-proxy 的地址一律不渲染成链接，
// 否则上游把 download_url 换成一个 javascript: 或第三方域名，横幅就成了注入点。
test('forced update hides dismiss and rejects non-github links',async()=>{
 const forced={...UPDATE,forced:true,download_url:'javascript:alert(1)',
   notes_url:'https://example.com/evil',download_mirror_url:MIRROR};
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER});
 const {server,base}=await open(forced);
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/dashboard`,{waitUntil:'networkidle'});
  const slot=page.locator('#updateSlot');
  await slot.locator('.upd').waitFor();
  assert.match(await slot.innerText(),/需要升级/,'forced 时标题应写「需要升级」');
  assert.equal(await slot.locator('button', {hasText:'跳过'}).count(),0,'强制更新不该给跳过按钮');
  const hrefs=await slot.locator('a').evaluateAll(as=>as.map(a=>a.getAttribute('href')));
  assert.ok(hrefs.length>=1,'至少镜像下载链接要在');
  for(const href of hrefs){
   assert.ok(/^https:\/\/(github\.com|gh-proxy\.com)\//.test(href),`横幅里出现了不该渲染的链接：${href}`);
  }
  assert.ok(!hrefs.some(h=>h.includes('javascript')),'javascript: 伪协议不能被渲染成链接');
  assert.ok(!hrefs.some(h=>h.includes('example.com')),'非白名单域名不能被渲染成链接');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});