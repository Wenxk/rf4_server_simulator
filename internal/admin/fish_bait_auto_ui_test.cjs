const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 钓场与鱼口页的「爱吃的饵」列：开了自动抽取的鱼种必须直接显示本期抽中的饵，
// 而不是只写一句「按稀有度每期重抽」让人去鱼饵偏好页对名单。
// 三种状态都要画得出来：正常抽中 / 型号不足全开 / 总开关未开。
test('fish page shows the auto-rotated bait pick in the bait column',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
 const pick=(baits,extra={})=>Object.assign({enabled:true,quota:3,pool_size:47,active_baits:baits,next_switch:Math.floor(Date.now()/1000)+1800,interval_sec:7200},extra);
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin'});
  if(key==='/api/tunables')return json({});
  if(key==='/api/fish/bite_mode')return json({mode:'legacy'});
  if(key==='/api/fish/zones')return json({zones:[]});
  if(key==='/api/bait/prefs')return json({rows:[
   {fish_key:'carp',name_zh:'普通鲤鱼',has_rule:true,allow:['甜奶油','玉米粒','红蚯蚓','比目鱼煮饵','奶酪块'],deny:[],bottom_only:[],
    tier3:2,tier2:1,total:6,baits:[{bait_id:'甜奶油 PopUp 16',tier:3,evidence:9}],official:[],auto_rotation:true,
    rotation:{enabled:true,mode:'auto',total:47,active:3,quota:3,pool_size:47,active_baits:['甜奶油 PopUp（16/20）','玉米粒（8/14）','红蚯蚓（10/14/18）'],next_switch:0}},
   {fish_key:'beluga',name_zh:'欧洲鳇',has_rule:true,allow:['比目鱼煮饵'],deny:[],bottom_only:[],tier3:1,tier2:0,total:1,baits:[],official:[],auto_rotation:true,
    rotation:{enabled:false,mode:'auto',total:1,active:-1,quota:1,pool_size:1,active_baits:[],next_switch:0}},
   {fish_key:'zander',name_zh:'梭鲈',has_rule:true,allow:['红蚯蚓'],deny:[],bottom_only:[],tier3:1,tier2:0,total:1,baits:[],official:[],auto_rotation:true,
    rotation:{enabled:true,mode:'auto',total:20,active:2,quota:2,pool_size:20,active_baits:['红蚯蚓（14/18）'],next_switch:0}}
  ]});
  if(key==='/api/fish/areas')return json([{loc:'琥珀湖',fish_count:3,level:'level_014_carp_lake',map_kind:'none',texture:'',calibration:null}]);
  if(key==='/api/fish/config')return json({
   area:'琥珀湖',
   setting:{bite_probability:1,bite_delay_fixed:0,bite_delay_scale:1,bite_delay_max:20,bite_delay_lure_max:6},
   game_day:3,game_hour:15,diel_now_segment:1,diel_now_name:'晨',diel_weight_enabled:true,diel_curve_min:0.08,
   fish:[
    {fish_key:'carp',name_zh:'普通鲤鱼',base_weight:100,weight:100,enabled:true,rarity_tier:0,rarity_label:'普通',
     diel_mask:15,diel_labels:'夜/晨/昼/昏',diel_curve:'[1,0.6,0.55,0.8]',diel_active:true,diel_factor:0.6,effective_weight:60,tier_weight:100,
     bait_source:'override',bait_enabled:true,bait_groups:[],bait_rotate_sec:0,bait_rotate_mode:'soft',bait_auto:true,
     bait_pick:pick(['甜奶油 PopUp（16/20）','玉米粒（8/14）','红蚯蚓（10/14/18）'])},
    {fish_key:'beluga',name_zh:'欧洲鳇',base_weight:5,weight:5,enabled:true,rarity_tier:2,rarity_label:'超稀有',
     diel_mask:15,diel_labels:'夜/晨/昼/昏',diel_curve:'[1,0.3,0.2,0.5]',diel_active:false,diel_factor:0.2,effective_weight:1,tier_weight:5,
     bait_source:'override',bait_enabled:true,bait_groups:[],bait_rotate_sec:0,bait_rotate_mode:'soft',bait_auto:true,
     bait_pick:pick([],{enabled:false,quota:1,pool_size:1})},
    {fish_key:'zander',name_zh:'梭鲈',base_weight:40,weight:40,enabled:true,rarity_tier:1,rarity_label:'稀有',
     diel_mask:15,diel_labels:'夜/晨/昼/昏',diel_curve:'[1,0.6,0.55,0.8]',diel_active:true,diel_factor:0.6,effective_weight:24,tier_weight:30,
     bait_source:'override',bait_enabled:true,bait_groups:[],bait_rotate_sec:0,bait_rotate_mode:'soft',bait_auto:true,
     bait_pick:pick(['红蚯蚓（14/18）'],{quota:2,pool_size:20,global_off:true})}
   ]
  });
  if(key.startsWith('/api/'))return json({});
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1600,height:1000}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_local_admin_token','fixture');localStorage.setItem('rf4_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#fish`,{waitUntil:'networkidle'});
  await page.locator('#fishRows table').waitFor();

  const cells=await page.locator('#fishRows tbody tr').allInnerTexts();
  const carp=cells.find(t=>t.includes('普通鲤鱼'))||'',beluga=cells.find(t=>t.includes('欧洲鳇'))||'',zander=cells.find(t=>t.includes('梭鲈'))||'';

  // 正常抽中：本期开口的 pill + 具体饵名（按型号折叠），且不再出现笼统提示。
  assert.match(carp,/本期开口 3\/47/);
  assert.match(carp,/甜奶油 PopUp（16\/20）/);
  assert.match(carp,/红蚯蚓（10\/14\/18）/);
  assert.match(carp,/候选池/,`自动抽取时规则行应改叫候选池：${carp}`);
  assert.doesNotMatch(carp,/按稀有度每期重抽/);
  // 型号不足配额：说明本期全开，不假装抽过。
  assert.match(beluga,/型号只有 1 种，不足配额 1，本期全开/);
  // 总开关未开：抽签结果照算但不生效，要提醒。
  assert.match(zander,/总开关未开，抽签不生效/);

  const headers=await page.locator('#fishRows thead th').allInnerTexts();
  assert.ok(headers.some(h=>h.includes('本期开口')),`表头应提到本期开口：${headers.join('|')}`);

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
