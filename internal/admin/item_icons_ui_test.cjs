const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

const PNG=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=','base64');

// 图标：物品与鱼种的图标都走运行时加载（服务端从 ref/ 读，不内嵌），
// 所以前端只负责拼 URL 与「加载失败就藏起来」。
//
// SPA 的图标是 <img> 但**没有类名**（旧后台用 .item-icon / .fish-icon），
// 只能按 src 定位。这不是退化：类名是样式钩子，SPA 的图标样式写在 inline
// style 上，加类名只会多一个没人用的选择器。断言改为验「URL 拼对了」
// 与「404 时 display:none」，这两条才是真正会坏的地方。
test('item and fish rows load encoded icons and hide missing images',async()=>{
 const iconRequests=[];
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin'});
  if(key==='/api/players')return json([{uid:1000001,name:'Tester',level:5,online:false,banned:false}]);
  if(key==='/api/players/detail')return json({
   uid:1000001,name:'Tester',level:5,xp:10,silver:20,gold:30,bank:40,membership:0,membership_exp:0,
   item_count:1,keepnet_count:0,mail_count:0,banned:false,happy_hour:null,
   items:[{item_id:'Bait A',name:'Bait A',category:27,container:'backpack',qty:2,durability:1,guid:'0123456789abcdef'}],
   keepnet:[],mails:[],skills:[],skill_total:0,skill_spent:0,skill_bonus:0,available_points:0
  });
  if(key==='/api/tunables')return json({});
  if(key==='/api/fish/bite_mode')return json({mode:'legacy'});
  if(key==='/api/fish/zones')return json({zones:[]});
  if(key==='/api/fish/areas')return json([{loc:'Test Lake',fish_count:2,level:'level_test',map_kind:'none',texture:'',calibration:null}]);
  if(key==='/api/bait/prefs')return json({rows:[]});
  if(key==='/api/fish/config')return json({
   area:'Test Lake',
   setting:{bite_probability:1,bite_delay_fixed:0,bite_delay_scale:1,bite_delay_max:20,bite_delay_lure_max:6},
   fish:[
    {fish_key:'Test.Fish',name_zh:'Test Fish',base_weight:1,weight:1,enabled:true},
    {fish_key:'Missing.Fish',name_zh:'Missing Fish',base_weight:1,weight:1,enabled:true}
   ]
  });
  if(key.startsWith('/icons/')){
   iconRequests.push(key);
   if(key==='/icons/items/bait%20a.webp'||key==='/icons/fish/test_fish.png'){
    res.writeHead(200,{'Content-Type':'image/png'});return res.end(PNG);
   }
   res.writeHead(404,{'Content-Type':'application/json'});return res.end('{"error":"missing"}');
  }
  if(key.startsWith('/api/'))return json({});
  // 静态资源走真实文件（入口已是 SPA 空壳，模块要逐个读盘）。
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_local_admin_token','fixture');localStorage.setItem('rf4_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  const base=`http://127.0.0.1:${server.address().port}`;

  // 背包：item_id 里有空格，URL 必须整段 encodeURIComponent（Bait A → bait%20a.webp）。
  // 注意 src 前缀：SPA 的 webURL 会拼上 BASE（/admin/ 下即 /admin/icons/...），
  // 旧后台用的是裸相对路径。选择器因此按后缀匹配，不写死开头。
  await page.goto(base+'/admin/#/players',{waitUntil:'networkidle'});
  await page.locator('.roster-option').first().waitFor();
  await page.locator('#tabs a').filter({hasText:'背包'}).first().click();
  const item=page.locator('img[src$="/icons/items/bait%20a.webp"]');
  await item.waitFor();
  await page.waitForFunction(()=>{const i=document.querySelector('img[src$="/icons/items/bait%20a.webp"]');return i&&i.complete&&i.naturalWidth>0});

  // 鱼种表：fish_key 的点号换成下划线，小写。
  await page.goto(base+'/admin/#/waters',{waitUntil:'networkidle'});
  const fish=page.locator('img[src$="/icons/fish/test_fish.png"]').first();
  await fish.waitFor();
  await page.waitForFunction(()=>{const i=document.querySelector('img[src$="/icons/fish/test_fish.png"]');return i&&i.complete&&i.naturalWidth>0});
  // 404 的那张必须自己被藏起来，不能在表里留一个碎图。
  const missing=page.locator('img[src$="/icons/fish/missing_fish.png"]').first();
  await missing.waitFor({state:'attached'});
  await page.waitForFunction(()=>{
    const i=document.querySelector('img[src$="/icons/fish/missing_fish.png"]');
    return i&&i.complete&&i.style.display==='none';
  });
  assert.equal(await missing.evaluate(el=>el.style.display),'none','加载失败的图标应隐藏');

  assert.ok(iconRequests.includes('/icons/items/bait%20a.webp'),JSON.stringify(iconRequests));
  assert.ok(iconRequests.includes('/icons/fish/test_fish.png'),JSON.stringify(iconRequests));
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{
  if(browser)await browser.close();
  await new Promise(r=>server.close(r));
 }
});