const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 「鱼种档案」页：列表三层对比 → 编辑弹窗 → 保存，POST 必须带全套生态字段。
// 重点守两件事：
//   1) 列表里「基线灰、覆盖金」不能串位（override 与 base 是两份独立对象）；
//   2) 留空字段必须发 null（= 继承基线），不能发 0。
test('ecology species page edits full ecology profile',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/spa.html'));
 let posted=null;

 // 基线：江鳕的合成值（18.9℃）——正是要纠正的那类数据。
 const base={
  fish_key:'burbot',layer_min:0.3,layer_pref:0.75,layer_max:1,
  temp_min_c:10.2,temp_pref_c:18.9,temp_max_c:27.1,
  diel_mask:15,diel_curve:'[1,0.3,0.2,0.5]',season_mask:15,
  diet_json:'{"worm":1}',weather_json:'{}',action_json:'{}',
  substrate_json:'{}',color_json:'{}',
  current_pref:0.5,activity:0.87,light_pref:0.5,clarity_pref:0.5,aggression:1,
  enabled:1,origin:'baseline',updated_by:'',updated_ts:0,
 };
 // 第二行：已覆盖过季节掩码（只有 1 春 8 冬 = 9）。
 const overridden={
  ...base,fish_key:'pike',layer_min:0.1,layer_pref:0.2,layer_max:0.5,
  temp_min_c:4,temp_pref_c:12,temp_max_c:20,season_mask:9,
  origin:'manual',updated_by:'admin',updated_ts:1732000000,
 };

 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/ecology-species'&&req.method==='GET')return json({
   items:[
    {fish_key:'burbot',name_zh:'江鳕',rarity_tier:1,origin:'',overlaid:false,base,override:null,
     locs:['旧奥斯特罗格湖','琥珀湖']},
    {fish_key:'pike',name_zh:'狗鱼',rarity_tier:0,origin:'manual',overlaid:true,base,override:overridden,
     locs:['铜湖']},
   ],count:2,total:2,
  });
  if(key==='/api/fish/ecology-species/reset'&&req.method==='POST'){
   let body='';req.on('data',c=>body+=c);req.on('end',()=>{posted={reset:JSON.parse(body)};json({ok:true})});return;
  }
  if(key==='/api/fish/ecology-species'&&req.method==='POST'){
   let body='';req.on('data',c=>body+=c);req.on('end',()=>{posted=JSON.parse(body);json({ok:true})});return;
  }
  // 饵料页只用来核对「水层/水域已从那里移走」，给一行最小数据即可。
  if(key==='/api/bait/prefs')return json({rules_enabled:true,rule_count:0,
   rows:[{fish_key:'burbot',name_zh:'江鳕',has_rule:false,allow:[],deny:[],bottom_only:[],
          condition:'',official:[],source:'default',enabled:false,auto_rotation:false,
          rotation:{enabled:false,pool_size:0,quota:0,active_baits:[],next_switch:0},
          layer:[0.3,0.75,1],qual_g:0,trophy_g:0}]});
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/, ''));
  const file=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file) && fs.statSync(file).isFile()){
   const ext=path.extname(file).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file));return;
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
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/spa.html#/ecology`,{waitUntil:'networkidle'});

  // 列表：两张卡片、状态分别是「基线」和「已覆盖」，窗宽 = 上限-下限。
  // 2026-11-26 起本页是卡片式（与鱼饵页一致），不再是宽表：
  // 一张卡 = 一个鱼种，卡片数就是行数。
  const table=page.locator('#ecoTable');
  await table.locator('text=江鳕').waitFor();
  const text=await table.innerText();
  assert.match(text,/10\.2 ~ 27\.1/);
  assert.match(text,/16\.9/);            // 江鳕窗宽 27.1-10.2
  assert.match(text,/基线/);
  assert.match(text,/已覆盖/);
  assert.match(text,/春\/冬/);            // pike 覆盖后的季节掩码 = 1|8
  assert.equal(await page.locator('#ecoStatus').innerText(),'2 / 2 个鱼种 · 1 个已覆盖');
  // 稀有度标记读的是 rarity_tier 而不是 rarity：写错会渲染成 undefined。
  assert.doesNotMatch(text,/undefined/);
  assert.match(text,/稀有/);

  // 水层与水域原本在「饵料规则」页，2026-11-25 搬到本页（都是鱼种属性，不是饵料属性）。
  // 卡片没有 <thead>，改为逐卡断言这两块内容真的在，否则等于悄悄丢了信息。
  const cards=table.locator('.fishcard');
  assert.equal(await cards.count(),2,'两个鱼种应渲染成两张卡片');
  const burbotCard=cards.filter({hasText:'江鳕'});
  assert.match(await burbotCard.innerText(),/栖息水层/,'卡片缺少水层块');
  assert.match(await burbotCard.innerText(),/出现的水域/,'卡片缺少水域块');
  // 水层要显示活动区间（min~max）而不只是偏好单值。
  assert.match(text,/0\.30~1\.00/,'水层未显示活动区间');
  // 水域要按鱼分别显示，不能两条都渲染成同一串。
  assert.match(text,/旧奥斯特罗格湖/);
  assert.match(text,/琥珀湖/);
  assert.match(text,/铜湖/);
  // 鱼饵页不再重复这两列（那边同样是卡片，按卡片正文判断）。
  await page.locator('#ecoTabs button[data-etab="bait"]').click();
  await page.locator('#baitTable .fishcard').first().waitFor();
  const baitText=await page.locator('#baitTable').innerText();
  assert.doesNotMatch(baitText,/活动水层|出现的水域/,`饵料页仍在展示水层/水域 → ${baitText.slice(0,300)}`);
  await page.locator('#ecoTabs button[data-etab="eco"]').click();
  await table.locator('text=江鳕').waitFor();

  // 筛选：只看已覆盖，只剩狗鱼。
  await page.locator('#ecoFilter').selectOption('overlaid');
  assert.equal(await cards.count(),1);
  assert.match(await table.innerText(),/狗鱼/);
  await page.locator('#ecoFilter').selectOption('');

  // 编辑江鳕：温度输入框应为空（未覆盖过），继承提示写清基线的 18.9。
  await cards.filter({hasText:'江鳕'}).locator('button:has-text("展开修改")').click();
  await page.locator('#eco_s_tpref').waitFor();
  assert.equal(await page.locator('#eco_s_tpref').inputValue(),'');
  assert.match(await page.locator('#eco_s_tpref').getAttribute('placeholder'),/继承 18\.9/);
  // 冷鱼人眼一看就该在 10~14℃：填 4/10/14，窗宽 10。
  await page.locator('#eco_s_tmin').fill('4');
  await page.locator('#eco_s_tpref').fill('10');
  await page.locator('#eco_s_tmax').fill('14');
  await page.waitForFunction(()=>document.querySelector('#ecoTempHint').textContent.includes('10.0'),null,{timeout:4000});
  assert.match(await page.locator('#ecoTempHint').innerText(),/热响应容差/);

  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,400));
  assert.ok(posted,'没有发出保存请求');
  assert.equal(posted.fish_key,'burbot');
  // 填了的三项按数值下发。
  assert.equal(posted.temp_min_c,4);
  assert.equal(posted.temp_pref_c,10);
  assert.equal(posted.temp_max_c,14);
  // 没填的一律 null = 继承基线，绝不能变成 0（0 会被当成合法覆盖值写进去）。
  assert.equal(posted.layer_min,null);
  assert.equal(posted.layer_pref,null);
  assert.equal(posted.activity,null);
  assert.equal(posted.aggression,null);
  assert.equal(posted.season_mask,null);
  assert.equal(posted.diel_mask,null);
  assert.equal(posted.enabled,null);
  // 五个偏好矩阵（食性/天气/拟饵动作/底质/水色）已从弹窗移除：它们是多键 JSON，
  // 手改容易打错且写进覆盖后没有回滚入口。保存时**根本不下发**这些键，
  // 而不是发 null——发 null 会把已有的矩阵覆盖清成「继承基线」，等于静默丢数据。
  assert.equal(posted.diet_json,undefined,'矩阵不该再下发（发 null 会清掉已有覆盖）');
  assert.equal(posted.weather_json,undefined);
  assert.equal(posted.action_json,undefined);
  assert.equal(posted.substrate_json,undefined);
  assert.equal(posted.color_json,undefined);
  // 界面上也不该再有这些输入框。
  assert.equal(await page.locator('#eco_s_diet').count(),0,'食性矩阵输入框应已移除');
  assert.equal(await page.locator('#eco_s_color').count(),0,'水色矩阵输入框应已移除');
  assert.equal(await page.locator('textarea#eco_s_action').count(),0,'拟饵动作矩阵输入框应已移除');

  // 编辑已覆盖的狗鱼：季节掩码应回显为「自定义」且只有春、冬两个勾。
  await page.locator('#ecoFilter').selectOption('overlaid');
  await cards.filter({hasText:'狗鱼'}).locator('button:has-text("展开修改")').click();
  await page.locator('#eco_s_season_mode').waitFor();
  assert.equal(await page.locator('#eco_s_season_mode').inputValue(),'1');
  assert.equal(await page.locator('#eco_s_season_b0').isChecked(),true);   // 春 1
  assert.equal(await page.locator('#eco_s_season_b1').isChecked(),false);  // 夏 2
  assert.equal(await page.locator('#eco_s_season_b2').isChecked(),false);  // 秋 4
  assert.equal(await page.locator('#eco_s_season_b3').isChecked(),true);   // 冬 8
  // 切回「继承」后保存，season_mask 必须回到 null。
  await page.locator('#eco_s_season_mode').selectOption('');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,400));
  assert.equal(posted.fish_key,'pike');
  assert.equal(posted.season_mask,null);

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});

// 昼夜曲线用逗号输入、JSON 数组下发；非法值必须在前端就被拦住，不发请求。
test('ecology curve input converts to json and rejects bad values',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/spa.html'));
 let calls=0;
 const base={fish_key:'pike',layer_min:0,layer_pref:0.5,layer_max:1,
  temp_min_c:4,temp_pref_c:12,temp_max_c:20,diel_mask:15,diel_curve:'',season_mask:15,
  diet_json:'{}',weather_json:'{}',action_json:'{}',substrate_json:'{}',color_json:'{}',
  current_pref:0.5,activity:1,light_pref:0.5,clarity_pref:0.5,aggression:1,
  enabled:1,origin:'baseline',updated_by:'',updated_ts:0};
 const server=http.createServer((req,res)=>{
  const key=new URL(req.url,'http://fixture').pathname.replace(/^\/admin/,'');
  const json=v=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(v))};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/ecology-species'&&req.method==='GET')
   return json({items:[{fish_key:'pike',name_zh:'狗鱼',rarity_tier:0,origin:'',overlaid:false,base,override:null}],count:1,total:1});
  if(key==='/api/fish/ecology-species'&&req.method==='POST'){calls++;return json({ok:true})}
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/, ''));
  const file=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file) && fs.statSync(file).isFile()){
   const ext=path.extname(file).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file));return;
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
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/spa.html#/ecology`,{waitUntil:'networkidle'});
  await page.locator('#ecoTable').locator('text=狗鱼').waitFor();
  await page.locator('#ecoTable button:has-text("展开修改")').first().click();
  await page.locator('#eco_s_curve').waitFor();

  // 三个数：拦下，不发请求。
  await page.locator('#eco_s_curve').fill('1,0.3,0.2');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,300));
  assert.equal(calls,0,'非法曲线不该发出保存请求');
  assert.match(await page.locator('#toast').innerText(),/四个 0~1 的数/);

  // 超范围：同样拦下。
  await page.locator('#eco_s_curve').fill('1,0.3,2,0.5');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,300));
  assert.equal(calls,0);

  // 合法：放行。
  await page.locator('#eco_s_curve').fill('1, 0.5, 0.2, 0.8');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,400));
  assert.equal(calls,1);

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
// 勾选与曲线打架时必须拦下：判鱼侧只认曲线，界面却按勾选写「已覆盖」，
// 结果是「改了勾选、咬口没变」——这是最难排查的一类问题（2026-11-26 实际踩过）。
test('saving mismatched diel mask and curve is blocked',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/spa.html'));
 let calls=0;
 const base={fish_key:'burbot',layer_min:0.3,layer_pref:0.75,layer_max:1,
  temp_min_c:4,temp_pref_c:8,temp_max_c:14,
  // 基线本身就是「夜+昏」，曲线与掩码自洽
  diel_mask:9,diel_curve:'[1,0.3,0.2,0.65]',season_mask:15,
  diet_json:'{}',weather_json:'{}',action_json:'{}',substrate_json:'{}',color_json:'{}',
  current_pref:0.5,activity:1,light_pref:0.5,clarity_pref:0.5,aggression:1,
  enabled:1,origin:'baseline',updated_by:'',updated_ts:0};
 const server=http.createServer((req,res)=>{
  const key=new URL(req.url,'http://fixture').pathname.replace(/^\/admin/,'');
  const json=v=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(v))};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/ecology-species'&&req.method==='GET')
   return json({items:[{fish_key:'burbot',name_zh:'江鳕',rarity_tier:0,origin:'',overlaid:false,base,override:null}],count:1,total:1});
  if(key==='/api/fish/ecology-species'&&req.method==='POST'){calls++;return json({ok:true})}
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/, ''));
  const file=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file) && fs.statSync(file).isFile()){
   const ext=path.extname(file).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file));return;
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
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/spa.html#/ecology`,{waitUntil:'networkidle'});
  await page.locator('#ecoTable').locator('text=江鳕').waitFor();
  await page.locator('#ecoTable button:has-text("展开修改")').first().click();
  await page.locator('#eco_s_curve').waitFor();

  // 一进来就该看到「曲线 → 实际开口」的实时提示，且与勾选一致。
  await page.waitForFunction(()=>document.querySelector('#ecoDielHint').textContent.includes('实际开口'),null,{timeout:4000});
  assert.match(await page.locator('#ecoDielHint').innerText(),/与勾选一致/);

  // 把勾选改成「只有夜」，但曲线仍让夜+昏开口 → 保存必须被拦。
  await page.locator('#eco_s_diel_mode').selectOption('1');
  await page.locator('#eco_s_diel_b0').check();   // 夜
  await page.locator('#eco_s_diel_b1').uncheck(); // 晨
  await page.locator('#eco_s_diel_b2').uncheck(); // 昼
  await page.locator('#eco_s_diel_b3').uncheck(); // 昏
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,300));
  assert.equal(calls,0,'勾选(仅夜)与曲线(夜+昏)不一致，不该发出保存请求');
  assert.match(await page.locator('#toast').innerText(),/不一致/);

  // 把曲线改成与勾选一致（只有夜是活跃段）→ 放行。
  await page.locator('#eco_s_curve').fill('1,0.3,0.2,0.2');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,400));
  assert.equal(calls,1,'勾选与曲线一致后应放行');

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
// 冷暖筛选的阈值必须跟着现有数据走。历史 bug：阈值写死 22℃，
// 而全库偏好温度最高只有 21.2℃，导致「暖水鱼」永远 0 行——看起来像坏掉了。
test('cold and warm filters stay non-empty when data is cool-shifted',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/spa.html'));
 // 造 4 条偏好温度都挤在 16~18℃ 的鱼，模拟现有合成基线整体偏冷的情况。
 const mk=(key,name,pref)=>({
  fish_key:key,name_zh:name,rarity_tier:0,origin:'',overlaid:false,
  base:{fish_key:key,layer_min:0,layer_pref:0.5,layer_max:1,
   temp_min_c:pref-8,temp_pref_c:pref,temp_max_c:pref+8,
   diel_mask:15,diel_curve:'',season_mask:15,
   diet_json:'{}',weather_json:'{}',action_json:'{}',substrate_json:'{}',color_json:'{}',
   current_pref:0.5,activity:1,light_pref:0.5,clarity_pref:0.5,aggression:1,
   enabled:1,origin:'baseline',updated_by:'',updated_ts:0},
  override:null,
 });
 const items=[mk('a','冷鱼',16),mk('b','中鱼',17),mk('c','暖鱼',17.5),mk('d','最暖',18)];
 const server=http.createServer((req,res)=>{
  const key=new URL(req.url,'http://fixture').pathname.replace(/^\/admin/,'');
  const json=v=>{res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(v))};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/ecology-species')return json({items,count:4,total:4});
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/, ''));
  const file=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file) && fs.statSync(file).isFile()){
   const ext=path.extname(file).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file));return;
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
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/spa.html#/ecology`,{waitUntil:'networkidle'});
  await page.locator('#ecoTable').locator('text=冷鱼').waitFor();
  const table=page.locator('#ecoTable');

  await page.locator('#ecoFilter').selectOption('cold');
  const cold=await table.locator('.fishcard').count();
  assert.ok(cold>0,`冷水鱼筛出 ${cold} 张卡，阈值写死会让整档空掉`);
  assert.ok(cold<4,`冷水鱼筛出全部 ${cold} 张卡，说明没有真的在筛`);

  await page.locator('#ecoFilter').selectOption('warm');
  const warm=await table.locator('.fishcard').count();
  assert.ok(warm>0,`暖水鱼筛出 ${warm} 张卡`);
  assert.ok(warm<4,`暖水鱼筛出全部 ${warm} 张卡`);

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
