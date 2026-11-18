const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 稀有度重算入口：说明条按库里阈值动态渲染、弹窗预览、保存时带上三个下限与重设权重开关。
test('fish rarity bar renders live cuts and recalculation posts them',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
 const previewQueries=[];
 let posted=null;
 const tiers=[
  {tier:0,label:'常规',min_catches:30,species:110,before:62,min_obs:30,max_obs:284},
  {tier:1,label:'少见',min_catches:14,species:86,before:63,min_obs:14,max_obs:29},
  {tier:2,label:'罕见',min_catches:7,species:30,before:62,min_obs:7,max_obs:13},
  {tier:3,label:'极罕见',min_catches:0,species:22,before:66,min_obs:1,max_obs:6},
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
   rarity_cuts:[30,14,7],rarity_active:true,
   fish:[
    {fish_key:'carp',name_zh:'普通鲤鱼',base_weight:100,weight:100,enabled:true,rarity_tier:0,rarity_label:'常规',
     diel_mask:15,diel_labels:['晨','昼','暮','夜'],diel_active:true,diel_factor:1,effective_weight:100,
     loc_catches:11,loc_rank:2,loc_total:136,loc_species:36,global_catches:58,tier_weight:100},
    {fish_key:'eel',name_zh:'鳗鱼',base_weight:15,weight:15,enabled:true,rarity_tier:2,rarity_label:'罕见',
     diel_mask:8,diel_labels:['夜'],diel_active:true,diel_factor:2,effective_weight:30,
     loc_catches:1,loc_rank:22,loc_total:136,loc_species:36,global_catches:8,tier_weight:15},
   ]
  });
  if(key==='/api/fish/rarity'){
   if(req.method==='POST'){
    let body='';req.on('data',c=>body+=c);req.on('end',()=>{
     posted=JSON.parse(body);json({ok:true,cuts:[posted.cut0,posted.cut1,posted.cut2],changed:150,total:248,skipped:5,weights:[100,40,15,5],updated:4,tiers});
    });return;
   }
   previewQueries.push(url.search);
   return json({cuts:[30,14,7],preview_cuts:[30,14,7],default_cuts:[30,14,7],active:true,tiers,weights:[100,40,15,5],weights_active:true});
  }
  if(key==='/api/fish/rarity/restore')return json({ok:true,cuts:[30,14,7],changed:3,tiers});
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

  // 说明条必须按库里阈值动态写，不能是写死的旧数字。
  const bar=page.locator('.params-bar', {hasText:'稀有度'});
  await bar.waitFor();
  const barText=await bar.innerText();
  assert.match(barText,/≥30 常规/);
  assert.match(barText,/14~29 少见/);
  assert.match(barText,/7~13 罕见/);
  assert.match(barText,/≤6 极罕见/);
  assert.match(barText,/不受本服测试进度影响/);

  // 打开弹窗，三个阈值默认值与后端一致。
  await bar.locator('button:has-text("重算稀有度")').click();
  await page.locator('#rc0').waitFor();
  assert.equal(await page.locator('#rc0').inputValue(),'30');
  assert.equal(await page.locator('#rc1').inputValue(),'14');
  assert.equal(await page.locator('#rc2').inputValue(),'7');

  // 预览表四档齐全，且带“重算后 / 相对现在 / 实际区间”。
  await page.waitForFunction(()=>document.querySelectorAll('#rarityPreview tbody tr').length===4);
  const previewText=await page.locator('#rarityPreview').innerText();
  assert.match(previewText,/常规/);
  assert.match(previewText,/110/);
  assert.match(previewText,/30~284 条/);

  // 改罕见下限会触发带参数的预览请求（防抖 220ms）。
  const before=previewQueries.length;
  await page.locator('#rc2').fill('9');
  await page.waitForFunction(n=>window.__q!==n,{},before).catch(()=>{});
  await page.waitForTimeout(500);
  assert.ok(previewQueries.length>before,`预览请求未发出：${JSON.stringify(previewQueries)}`);
  assert.match(previewQueries[previewQueries.length-1],/cut0=30/);
  assert.match(previewQueries[previewQueries.length-1],/cut2=9/);

  // 应用：POST body 必须带三个下限 + 重设权重开关（默认勾选）。
  await page.locator('button:has-text("应用并重算")').click();
  await page.waitForFunction(()=>true);
  await new Promise(r=>setTimeout(r,400));
  assert.ok(posted,`没有发出 POST：${JSON.stringify(previewQueries)}`);
  assert.deepEqual(posted,{cut0:30,cut1:14,cut2:9,reset_weights:true});

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
