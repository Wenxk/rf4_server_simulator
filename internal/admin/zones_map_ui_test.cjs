// 地图区域编辑器 UI：纸地图渲染 + 拖拽吸附 40 米网格 + 本图名册选鱼 + 保存回传。
// 运行前提：npm i --no-save playwright；浏览器可用 RF4_TEST_BROWSER 指定（默认系统 Edge）。
// 期望值按 docs/fish-query-coordinates.md 公式手算（图像像素=世界×scale+offset；
// 网格=floor(世界/40)），不用被测代码复算。
//
// 迁移时的取舍（批次 6）：
//   这才是真的「一套业务逻辑」而不是一堆 id。旧后台靠固定的
//   `#zoneMapImg` / `#zoneMapStage` / `#zoneGX0` / `#zonePanelFooter` /
//   `.zone-fish-row[data-key=…]` 定位；SPA 的地图画布改用「三层 canvas 叠起来
//   （底图 img + 网格 + 区域 + 操作层）」，坐标换算与 gridAt 一个都没变。
//   所以这里保留全部数学断言（拖拽后的网格号、保存回传的 gx0/gz0/gx1/gz1、
//   名册加入时是否带上基础权重），只把选择器换成 SPA 的真实结构：
//     .zmap-stage / .zmap-inner>img / .zmap-side / .zside-edit
//   三条旧的「入口可见性」断言也照搬过来，因为它们守的是**产品规则**不是布局：
//     海图不划区、无图地图只能手输、海图不该出现建区按钮。
const {test}=require('node:test');
const assert=require('node:assert/strict');
const http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 旧奥斯特罗格湖标定（与 fishmaps.Scenes 同源）：origin 0/0，scale 4.824，offset 56/37。
// 拖拽 world(85,85)→(195,155)：起点格 floor(85/40)=2，终点格 floor(195/40)=4 / floor(155/40)=3。
const CAL={origin_x:0,origin_z:0,size:400,paper:{offset_x:56,offset_z:37,scale:4.824,width:2048,height:2048}};
const SEA_CAL={origin_x:0,origin_z:0,size:0,paper:{offset_x:78,offset_z:78,scale:0.38,width:2048,height:2048}};

const AREAS=[
 {loc:'旧奥斯特罗格湖',fish_count:16,level:'level_004_torfyanoe_ozero',map_kind:'grid',texture:'maps/map_level_004_torfyanoe.webp',calibration:CAL},
 {loc:'挪威海',fish_count:57,level:'level_012_north_sea',map_kind:'sea',texture:'maps/map_level_012_north_sea.webp',calibration:SEA_CAL},
 {loc:'无名塘',fish_count:3,level:'',map_kind:'none',texture:'',calibration:null},
];
const SETTING={bite_probability:1,bite_delay_fixed:0,bite_delay_scale:1,bite_delay_max:20,bite_delay_lure_max:6};

function serve(posts){
 return http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const key=url.pathname.replace(/^\/admin/,'');
  const json=(v,code=200)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(v));};
  if(req.method==='POST'){
   let body='';req.on('data',c=>body+=c);
   req.on('end',()=>{posts.push({key,body});json({ok:true,id:9});});
   return;
  }
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/areas')return json(AREAS);
  if(key==='/api/fish/bite_mode')return json({mode:'realistic'});
  if(key==='/api/tunables')return json({});
  if(key==='/api/bait/prefs')return json({rows:[]});
  if(key==='/api/fish/config'){
   const loc=url.searchParams.get('loc');
   if(loc==='挪威海')return json({area:'挪威海',setting:SETTING,fish:[{fish_key:'cod',name_zh:'鳕鱼',base_weight:2,weight:2,enabled:true}]});
   if(loc==='无名塘')return json({area:'无名塘',setting:SETTING,fish:[]});
   return json({area:'旧奥斯特罗格湖',setting:SETTING,fish:[
    {fish_key:'crucian',name_zh:'银鲫',base_weight:3.2,weight:3.2,enabled:true},
    {fish_key:'pike',name_zh:'狗鱼',base_weight:5,weight:5,enabled:true},
    {fish_key:'bream',name_zh:'欧鳊',base_weight:2.1,weight:2.1,enabled:true}]});
  }
  if(key==='/api/fish/sea-zones')return json({zones:[{zone:120,x0:3450,z0:850,x1:3690,z1:630},{zone:121,x0:3690,z0:850,x1:3930,z1:630}]});
  if(key==='/api/fish/zones')return json({zones:[
   {id:7,loc:'旧奥斯特罗格湖',name:'湾口鲤区',kind:'grid_rect',sea_zone:0,gx0:0,gz0:0,gx1:2,gz1:1,mode:'exclusive',boost:3,keep_others:0.35,enabled:true,fish:[{fish_key:'crucian',weight:3,enabled:true}]}]});
  if(key.startsWith('/api/'))return json({});
  // 贴图与生产 /maps/ 同口径：素材不入仓库，缺图回合成占位 SVG。
  // 占位必须带显式 width/height——没有固有尺寸 <img> 的 naturalWidth 恒为 0，
  // 下面的 waitForFunction 会永远等不到（2026-11-16 评审 P2：干净环境固定超时）。
  if(key.startsWith('/maps/')&&key.endsWith('.webp')){
   res.writeHead(200,{'Content-Type':'image/svg+xml; charset=utf-8'});
   return res.end('<svg xmlns="http://www.w3.org/2000/svg" width="2048" height="2048" viewBox="0 0 2048 2048"><rect width="2048" height="2048" fill="#1b2430"/></svg>');
  }
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
}

test('zone map editor draws on paper map with 40m grid snap',async()=>{
 const posts=[];
 const server=serve(posts);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1440,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  // 直接落到「旧奥斯特罗格湖 · 鱼群区域」：SPA 用 #/waters/<水域>/<页签> 路由。
  await page.goto(`${base}/#/waters/${encodeURIComponent('旧奥斯特罗格湖')}/zones`,{waitUntil:'networkidle'});

  // 纸地图加载后才能交互。底图是 .zmap-inner 里的第一个 <img>。
  const mapImg=page.locator('.zmap-inner img');
  await page.waitForFunction(()=>{
    const i=document.querySelector('.zmap-inner img');
    return i&&i.complete&&i.naturalWidth>0;
  },null,{timeout:8000});
  assert.ok((await mapImg.getAttribute('src')).includes('map_level_004_torfyanoe.webp'),'贴图 URL 应含特例名');
  // webURL 会加 BASE 前缀，所以按后缀断言而不是写死开头。
  assert.ok((await mapImg.getAttribute('src')).startsWith('/admin/'),'贴图应走 webURL（带 /admin 前缀）');

  // 拖拽 world(85,85)→(195,155)：期望网格 2:2 ~ 4:3
  // 注意 Z 轴：客户端纸地图公式的 Y 从图片底部往上量，位图行号从顶部往下数 → 行 = H−(w×scale+off)
  //
  // 舞台坐标不能直接用 rect.width 当 2048：SPA 会「看全图」把地图缩放并居中，
  // 舞台不是正方形（高度被 max-height 卡住）时左右会留出 tx 的空白。
  // 这里读 .zmap-inner 的实际 transform（k/tx/ty），把「图片像素」换算成屏幕坐标。
  // 读的是视图变换（显示细节），换算公式仍是文档里那套，没拿被测代码复算。
  const stage=page.locator('.zmap-stage');
  const view=async()=>page.evaluate(()=>{
    const st=document.querySelector('.zmap-stage').getBoundingClientRect();
    const inner=getComputedStyle(document.querySelector('.zmap-inner')).transform;
    const m=new DOMMatrixReadOnly(inner);
    return {left:st.left,top:st.top,k:m.a,tx:m.e,ty:m.f};
  });
  const v=await view();
  const toScreen=(wx,wz)=>{
    const px=(wx*CAL.paper.scale+CAL.paper.offset_x);
    const pz=2048-(wz*CAL.paper.scale+CAL.paper.offset_z);
    return [v.left+v.tx+px*v.k, v.top+v.ty+pz*v.k];
  };
  const [sx,sy]=toScreen(85,85), [ex,ey]=toScreen(195,155);
  await page.mouse.move(sx,sy);await page.mouse.down();
  await page.mouse.move((sx+ex)/2,(sy+ey)/2);
  await page.mouse.move(ex,ey);await page.mouse.up();

  // 松手后右侧进入编辑面板：.zside-edit 出现，范围行会写出网格/坐标文字。
  const edit=page.locator('.zside-edit');
  await edit.waitFor({timeout:5000});
  // 网格数字在「按网格数字调整」折叠区里（SPA 把精确输入收到 details 里，
  // 主界面先给一句话范围描述）。展开它才能读到值。
  await edit.locator('summary', {hasText:'按网格数字调整'}).click();
  const val=async(label)=>{
    const field=edit.locator('.field').filter({has:page.locator(`label:text-is("${label}")`)});
    return field.locator('input').inputValue();
  };
  assert.equal(await val('起点 X'),'2','起点 X 应吸附到网格 2');
  assert.equal(await val('起点 Z'),'2','起点 Z 应吸附到网格 2');
  assert.equal(await val('终点 X'),'4','终点 X 应吸附到网格 4');
  assert.equal(await val('终点 Z'),'3','终点 Z 应吸附到网格 3');
  // 范围描述要跟网格一致：3 格 × 2 格 = 120×80 米。
  assert.match(await edit.locator('.zsec').filter({hasText:'范围'}).innerText(),/120×80 米/,
    '范围描述应为 120×80 米（3×40 与 2×40）');

  // 本图名册选鱼：列表来自 /api/fish/config 的鱼种，带基础权重。
  await edit.locator('input[placeholder="筛选鱼名"]').fill('狗');
  await edit.locator('.list-item', {hasText:'狗鱼'}).first().click();
  await edit.locator('input[placeholder="筛选鱼名"]').fill('欧鳊');
  await edit.locator('.list-item', {hasText:'欧鳊'}).first().click();
  await edit.locator('input[placeholder="例如 湾口鲤鱼区"]').fill('湾嘴狗鱼区');
  await edit.locator('button', {hasText:'保存'}).click();
  await page.locator('.toast').first().waitFor({timeout:5000});

  const saved=posts.find(p=>p.key==='/api/fish/zones');
  assert.ok(saved,'应 POST /api/fish/zones');
  const body=JSON.parse(saved.body);
  assert.equal(body.gx0,2);assert.equal(body.gz0,2);assert.equal(body.gx1,4);assert.equal(body.gz1,3);
  assert.equal(body.mode,'exclusive');assert.equal(body.name,'湾嘴狗鱼区');
  assert.equal(body.loc,'旧奥斯特罗格湖','保存必须带水域，否则会写进别的水域');
  assert.deepEqual(body.fish.map(f=>f.fish_key).sort(),['bream','pike']);
  assert.equal(body.fish.find(f=>f.fish_key==='pike').weight,5,'名册加入时默认带基础权重');
  assert.equal(body.fish.find(f=>f.fish_key==='bream').weight,2.1);

  // 点击既有区域 → 面板载入该区。
  // 保存后服务端只回 id 9、列表里没有它，SPA 会把编辑面板收起回列表，
  // 所以这里直接点地图即可（不用先返回列表）。
  // 但收起面板会改变右侧栏宽度 → 舞台重新 fit → k/tx/ty 全变，
  // 必须重新读一次视图变换，否则点击会落到地图外面（这里踩过）。
  await page.locator('.zcard').first().waitFor();
  const v2=await view();
  const toScreen2=(wx,wz)=>{
    const px=(wx*CAL.paper.scale+CAL.paper.offset_x);
    const pz=2048-(wz*CAL.paper.scale+CAL.paper.offset_z);
    return [v2.left+v2.tx+px*v2.k, v2.top+v2.ty+pz*v2.k];
  };
  const [cx,cy]=toScreen2(40,20);
  await page.mouse.click(cx,cy);
  await page.waitForTimeout(200);
  const nameBox=page.locator('.zside-edit input[placeholder="例如 湾口鲤鱼区"]');
  await nameBox.waitFor();
  assert.equal(await nameBox.inputValue(),'湾口鲤区','点已有区域应载入它的名字');
  assert.ok((await page.locator('.zside-edit').innerText()).includes('矩形'),'面板应标出区域类型');

  // 切水域用的是下拉框 `<select aria-label="选择水域">`（18 个水域用下拉比按钮组省地方）。
  // 它和右侧编辑面板是两个独立区块，编辑时也必须还能用。
  const areaSel=page.locator('select[aria-label="选择水域"]');
  assert.ok(await areaSel.count()>0,'水域切换应始终可用（它与右侧编辑面板是两个独立区块）');

  // 海图：纯浏览（2026-11-14 起不做区域划分）——底图加载、建区入口全部隐藏。
  await areaSel.selectOption({label:'挪威海（海图）'});
  // 等结构本身变化，不要等「图片已加载」——旧图的 img 本来就已加载完，
  // 那个条件在重绘前就为真，等于什么都没等（这里踩过）。
  await page.waitForFunction(()=>!document.querySelector('.zmap-stage'),null,{timeout:8000});
  const zmap=page.locator('.zmap');
  assert.equal(await zmap.locator('.zmap-stage').count(),0,'海图不该出现可绘制舞台');
  assert.equal(await zmap.locator('.zmap-tools').count(),0,'海图不该出现绘图工具栏');
  const seaText=await zmap.innerText();
  assert.match(seaText,/不划分鱼群区域/,'海图提示应说明不做区域划分');
  assert.match(seaText,/整张图的鱼种/,'海图应说明按整图鱼种出鱼');

  // 无图地图：手动输入入口可见，且没有贴图。
  await areaSel.selectOption({label:'无名塘'});
  // 同样等结构：无图地图没有 .zmap-inner。
  await page.waitForFunction(()=>!document.querySelector('.zmap-inner'),null,{timeout:8000});
  const noneMap=page.locator('.zmap');
  assert.equal(await noneMap.locator('.zmap-inner img').count(),0,'无图地图不应有贴图');
  const noneText=await noneMap.innerText();
  assert.match(noneText,/没有可用的纸地图/,'无图地图应说明原因');
  assert.match(noneText,/按网格数字新建/,'无图地图应指出手动输入这条路');
  assert.ok(await noneMap.locator('button', {hasText:'新建区域'}).count()>0,'无图地图应给出新建入口');

  assert.deepEqual(errors,[],'页面不应有脚本错误');
 }finally{
  if(browser)await browser.close();
  await new Promise(r=>server.close(r));
 }
});