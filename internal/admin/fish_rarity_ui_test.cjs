const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 稀有度改为鱼种固有属性后，鱼口页要：
//   1. 说明条只讲「固有属性」，不再出现任何按渔获量切的阈值；
//   2. 每个鱼种标固有档位（普通/稀有/超稀有）与该档预设权重，不再展示本图/全服渔获；
//   3. 「按稀有度重设权重」只发三档（tier0~2），没有第四档。
test('fish rarity bar shows intrinsic tiers and preset posts three tiers',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
 let posted=null;
 const tiers=[
  {tier:0,label:'普通',weight:100,species:169,rows:200,avg_base:5.2},
  {tier:1,label:'稀有',weight:30,species:45,rows:60,avg_base:2.1},
  {tier:2,label:'超稀有',weight:5,species:39,rows:50,avg_base:1.1},
 ];
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin'});
  if(key==='/api/tunables')return json({});
  if(key==='/api/fish/bite_mode')return json({mode:'legacy'});
  if(key==='/api/fish/zones')return json({zones:[]});
  if(key==='/api/bait/prefs')return json({rows:[]});
  if(key==='/api/fish/areas')return json([{loc:'琥珀湖',fish_count:2,level:'level_014_carp_lake',map_kind:'none',texture:'',calibration:null}]);
  if(key==='/api/fish/config')return json({
   area:'琥珀湖',
   setting:{bite_probability:1,bite_delay_fixed:0,bite_delay_scale:1,bite_delay_max:20,bite_delay_lure_max:6},
   game_day:3,game_hour:15,diel_now_segment:2,diel_now_name:'暮',diel_weight_enabled:true,diel_peak_boost:2,diel_penalty:0.3,
   fish:[
    {fish_key:'carp',name_zh:'普通鲤鱼',base_weight:100,weight:100,enabled:true,rarity_tier:0,rarity_label:'普通',
     diel_mask:15,diel_labels:['晨','昼','暮','夜'],diel_active:true,diel_factor:1,effective_weight:100,tier_weight:100},
    {fish_key:'beluga',name_zh:'欧洲鳇',base_weight:5,weight:5,enabled:true,rarity_tier:2,rarity_label:'超稀有',
     diel_mask:15,diel_labels:['晨','昼','暮','夜'],diel_active:true,diel_factor:1,effective_weight:5,tier_weight:5},
   ]
  });
  if(key==='/api/fish/weights/preset'){
   if(req.method==='POST'){
    let body='';req.on('data',c=>body+=c);req.on('end',()=>{
     posted=JSON.parse(body);json({ok:true,weights:[posted.tier0,posted.tier1,posted.tier2],updated:3,tiers});
    });return;
   }
   return json({weights:[100,30,5],tiers,rows:310,active:true});
  }
  if(key.startsWith('/api/'))return json({});
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1440,height:960}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_local_admin_token','fixture');localStorage.setItem('rf4_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#fish`,{waitUntil:'networkidle'});

  // 说明条：只讲固有属性，不能出现按渔获量切出来的阈值或「重算稀有度」入口。
  const bar=page.locator('.params-bar', {hasText:'稀有度'});
  await bar.waitFor();
  const barText=await bar.innerText();
  assert.match(barText,/鱼种固有属性/);
  assert.match(barText,/普通 169/);
  assert.match(barText,/稀有 45/);
  assert.match(barText,/超稀有 39/);
  assert.doesNotMatch(barText,/≥30|14~29|7~13|全服渔获数/);
  assert.equal(await bar.locator('button:has-text("重算稀有度")').count(),0);
  // 钓法倾向层已整层移除：说明条上不该再有入口，页面里也不该残留弹窗文案。
  assert.equal(await bar.locator('button:has-text("钓法倾向")').count(),0);
  const pageSrc=await page.evaluate(()=>document.documentElement.outerHTML);
  assert.doesNotMatch(pageSrc,/钓法倾向|rig_affinity/);

  // 鱼种行：标固有档位 + 档位权重，不出现渔获名次。
  const rowText=await page.locator('#fishRows').innerText();
  assert.match(rowText,/超稀有/);
  assert.match(rowText,/档位权重 5/);
  assert.doesNotMatch(rowText,/本图 \d+ 条|全服 \d+|图内常见|本图少见/);

  // 重设权重弹窗：三档，没有第四档输入框。
  await page.locator('button:has-text("按稀有度重设权重")').click();
  await page.locator('#tp_0').waitFor();
  assert.equal(await page.locator('#tp_0').inputValue(),'100');
  assert.equal(await page.locator('#tp_1').inputValue(),'30');
  assert.equal(await page.locator('#tp_2').inputValue(),'5');
  assert.equal(await page.locator('#tp_3').count(),0);

  // 应用：POST body 只有三档。
  await page.locator('button:has-text("确认重设")').click();
  await page.waitForFunction(()=>true);
  await new Promise(r=>setTimeout(r,400));
  assert.ok(posted,`没有发出 POST`);
  assert.deepEqual(posted,{tier0:100,tier1:30,tier2:5});

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
