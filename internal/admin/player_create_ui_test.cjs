const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');

// 玩家管理页的「+ 新建玩家」入口：只提交档案字段（uid 可留空交给后台取号），
// 创建成功后回到该玩家详情，不携带任何「绑定 IP / 设备」参数。
test('player create entry posts profile fields and opens the new player',async()=>{
 const {sendAsset}=require('./web_fixture.cjs');
 let posted=null,created=false;
 const existing={uid:1000001,name:'Tester',level:10,xp:0,silver:0,gold:0,bank:0,banned:false,online:false};
 const server=require('node:http').createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin'});
  if(key==='/api/tunables')return json({});
  if(key==='/api/players')return json(created?[{...existing,uid:1000002,name:'甲板工',level:10,silver:8000}]:[existing]);
  if(key==='/api/players/detail')return json({...existing,uid:+url.searchParams.get('uid'),name:+url.searchParams.get('uid')===1000002?'甲板工':'Tester',items:[],keepnet:[],mails:[],item_count:0,keepnet_count:0,mail_count:0});
  if(key==='/api/players/create'){
   let body='';req.on('data',c=>body+=c);req.on('end',()=>{
    posted=JSON.parse(body);created=true;
    json({ok:true,uid:1000002,name:posted.name,level:posted.level,xp:0,silver:posted.silver,gold:posted.gold});
   });return;
  }
  if(key.startsWith('/api/'))return json({});
  // 静态资源走真实文件：入口已是 SPA 空壳，模块要逐个读盘，回 HTML 会白屏。
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1440,height:960}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#/players`,{waitUntil:'networkidle'});
  // 名单是 .roster-option（按钮），不是旧后台的 .player-row 表格行。
  await page.locator('.roster-option').first().waitFor();

  // 入口与表单字段：UID / 昵称 / 等级 / 银币 / 金币。
  // SPA 用右侧侧页（.side-page）而不是旧后台的 #modal，字段 id 由 Form 生成
  // （f_<key>_<n>），所以按前缀定位，不能照抄 #np_uid。
  const entry=page.locator('button:has-text("新建玩家")').first();
  await entry.waitFor();
  await entry.click();
  const side=page.locator('.side-page');
  await side.waitFor();
  // 必须把定位范围收在侧页内：档案编辑表单（同一页右侧）也有「银币」字段，
  // 按 id 前缀全页找会命中两个（Form 生成的 id 带自增后缀，不唯一）。
  const field=key=>side.locator(`input[id^="f_${key}_"]`);
  // 「留空自动分配」的语义由 0 表达（后端 uid<=0 才取最大号+1），所以初值是 0 不是空串。
  assert.equal(await field('uid').inputValue(),'0','UID 初值应为 0（自动分配）');
  assert.equal(await field('silver').inputValue(),'5000','银币初值应为 5000');
  // 这段说明不能丢：档案没有密码，玩家用同名登录就会绑进来，等于登记账号。
  assert.match(await side.innerText(),/没有密码/,'侧页应说明档案没有密码');

  await field('name').fill('甲板工');
  await field('level').fill('10');
  await field('silver').fill('8000');
  await side.getByRole('button',{name:'创建玩家',exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('.side-page'));

  assert.ok(posted,`没有发出 POST /api/players/create`);
  assert.deepEqual(posted,{uid:0,name:'甲板工',level:10,silver:8000,gold:0});
  // 创建成功后直接跳到新号（replaceHash players/<uid>），省一次搜索。
  // 这比旧后台「把 uid 填进搜索框」更直接，断言也跟着改成看 hash 与详情标题。
  await page.waitForFunction(()=>location.hash.includes('1000002'),null,{timeout:5000});
  assert.match(await page.locator('.whorow').innerText(),/甲板工/,'名单应出现新玩家');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{
  if(browser)await browser.close();
  server.close();
 }
});
