const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');

// 玩家管理页的「+ 新建玩家」入口：只提交档案字段（uid 可留空交给后台取号），
// 创建成功后回到该玩家详情，不携带任何「绑定 IP / 设备」参数。
test('player create entry posts profile fields and opens the new player',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
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
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1440,height:960}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#players`,{waitUntil:'networkidle'});
  await page.locator('.player-row').first().waitFor();

  // 入口与弹窗字段：UID/昵称/等级/银币/金币，且明说不绑定设备。
  const entry=page.locator('button:has-text("+ 新建玩家")');
  await entry.waitFor();
  await entry.click();
  await page.locator('#np_uid').waitFor();
  assert.equal(await page.locator('#np_uid').inputValue(),'');
  assert.equal(await page.locator('#np_silver').inputValue(),'5000');
  assert.match(await page.locator('#modal').innerText(),/同名登录/);

  await page.locator('#np_name').fill('甲板工');
  await page.locator('#np_level').fill('10');
  await page.locator('#np_silver').fill('8000');
  await page.locator('button:has-text("创建玩家")').click();
  await page.waitForFunction(()=>document.querySelector('#modalBg')?.classList.contains('on')===false);

  assert.ok(posted,`没有发出 POST /api/players/create`);
  assert.deepEqual(posted,{uid:0,name:'甲板工',level:10,silver:8000,gold:0});
  // 成功后左上列表切到新号，详情页显示新昵称。
  assert.equal(await page.locator('#playerSearch').inputValue(),'1000002');
  assert.match(await page.locator('#playerList').innerText(),/甲板工/);
  await page.locator('#playerDetail:has-text("甲板工")').waitFor();
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{
  if(browser)await browser.close();
  server.close();
 }
});
