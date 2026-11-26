const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 「天气预览」页：地图下拉 → 7 天卡片 → 24 小时曲线 → 温度区间 → 四季对比 → 全区对比。
//
// fixture 数据刻意做成「阿赫图巴河」的形状：气温跨度大（12~39℃）、
// 一天里晴转雷雨、四季差异明显，这样能同时验证：
//   - 日卡片按 kind_name 选图标和颜色；
//   - 当前小时只高亮一根竖线；
//   - 分档条宽度等于 share，且合计覆盖全部采样点；
//   - 点对比表某行会切换上方明细。
// fixture 用的「今天」。日号已回归世界时钟轴（2026-11-24），面板显示的就是客户端
// 屏幕上的那个数（自 2015-08-01 起算，当前约 99185）。fixture 仍取小日号 1，
// 一是让断言里的字面量好读，二是顺带验证 wxDayLabel 在 day%365 里对小数值也正常
// （第 0/1/2 天 → 今天/明天/后天）。
const NOW_DAY=1;

function mkHours(day,high,low,kind){
  // 24 小时正弦气温曲线 + 中层水温、底层水温按 .34 传导比压缩。
  const out=[];
  for(let h=0;h<24;h++){
    const cyc=(1-Math.cos((h-4)*Math.PI/12))/2;
    const air=+(high-(high-low)*(1-cyc)).toFixed(1);
    const surf=+(air*0.62+3).toFixed(1);
    const mid=+(air*0.42+6).toFixed(1);
    const deep=+(air*0.3+8).toFixed(1);
    const rain=kind==='雷雨'&&h>=14&&h<=18?0.7:0;
    const thunder=kind==='雷雨'&&h>=15&&h<=17?0.8:0;
    out.push({
      day,hour:h,air_temp_c:air,surface_c:surf,water_c:mid,deep_c:deep,
      cloud:kind==='晴'?0.02:0.7,rain,thunder:thunder,
      wind_kmh:12,fog:0,kind:0,kind_name:kind,
      segment:h<5?0:(h<9?1:(h<16?2:3)),
      segment_name:h<5?'夜':(h<9?'晨':(h<16?'昼':'昏')),
      is_now:day===NOW_DAY&&h===2,
    });
  }
  return out;
}

function mkDays(){
  const kinds=['晴','少云','多云','小雨','大雨','雷雨','晴'];
  return kinds.map((k,i)=>{
    const high=39-i*2;
    const low=high-14;
    return {
      day:NOW_DAY+i,kind:0,kind_name:k,
      high_c:high,low_c:low,
      water_high_c:+(high*0.42+6).toFixed(1),water_low_c:+(low*0.42+6).toFixed(1),
      max_wind_kmh:14+i,rain_hours:k==='雷雨'?4:(k==='大雨'?6:(k==='小雨'?3:0)),
    };
  });
}

// 活跃鱼种 fixture：三条活跃 + 两条不活跃（含原因）。
// 分数刻意做成递减，好断言「按综合系数降序」这条排序契约。
function mkActive(mapKey){
  const f=(over)=>({
    fish_key:'k',name_zh:'鱼',rarity:0,rarity_tag:'普通',diel_mask:2,diel_label:'昼',
    temp_min_c:8,temp_pref_c:15,temp_max_c:22,
    diel_factor:1,thermal_fact:1,score:1,active:true,reason:'',...over,
  });
  return {
    map:mapKey,game_hour:2,segment:'夜',water_c:18.4,season:'秋',
    total:5,active_num:3,
    fish:[
      f({fish_key:'perch',name_zh:'河鲈',rarity:2,rarity_tag:'超稀有',score:1.42,diel_factor:1,thermal_fact:0.95}),
      f({fish_key:'pike',name_zh:'白斑狗鱼',rarity:1,rarity_tag:'稀有',score:0.88,diel_factor:0.95,thermal_fact:0.93}),
      f({fish_key:'roach',name_zh:'拟鲤',score:0.41,diel_factor:0.62,thermal_fact:0.66}),
    ],
    idle:[
      f({fish_key:'bream',name_zh:'欧鳊',score:0.12,diel_factor:0.08,thermal_fact:1,active:false,
         reason:'非活跃时段（昼）'}),
      f({fish_key:'hot',name_zh:'热水鱼',rarity:2,rarity_tag:'超稀有',score:0.35,diel_factor:1,thermal_fact:0.35,
         active:false,reason:'水温偏离最适（最适 36.0℃）'}),
    ],
  };
}

function mkPreview(mapKey,mapName){
  const days=mkDays();
  let hours=[];
  for(const d of days)hours=hours.concat(mkHours(d.day,d.high_c,d.low_c,d.kind_name));
  // 分档统计：按 2℃ 分档，模拟后端口径。
  const mids=hours.map(h=>h.water_c);
  const lo=Math.min(...mids),hi=Math.max(...mids);
  const bands=[];
  for(let a=Math.floor(lo/2)*2;a<Math.ceil(hi/2)*2;a+=2){
    const cnt=mids.filter(v=>v>=a&&v<a+2).length;
    bands.push({from_c:a,to_c:a+2,count:cnt,share:cnt/mids.length});
  }
  return {map:mapKey,map_name:mapName,
    day_from:NOW_DAY,day_to:NOW_DAY+6,now_day:NOW_DAY,now_hour:2,game_clock:'02:31',
    water_min_c:lo,water_max_c:hi,air_min_c:12.3,air_max_c:39,
    days,hours,bands,
    seasons:[
      {season:0,season_name:'春',water_min_c:9.7,water_max_c:26,air_min_c:3,air_max_c:31,is_now:false},
      {season:1,season_name:'夏',water_min_c:15,water_max_c:32.2,air_min_c:9,air_max_c:39,is_now:false},
      {season:2,season_name:'秋',water_min_c:11.5,water_max_c:27.4,air_min_c:12.3,air_max_c:29,is_now:true},
      {season:3,season_name:'冬',water_min_c:4.9,water_max_c:21.7,air_min_c:1.7,air_max_c:18,is_now:false},
    ],
    season:2,season_name:'秋'};
}

test('weather preview page renders days chart bands and season compare',async()=>{
  const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
  const maps=[
    {key:'level_001_lake',map_name:'克马羚诺也湖',has_data:true},
    {key:'level_009_ahtuba',map_name:'阿赫图巴河',has_data:true},
    {key:'level_012_north_sea',map_name:'挪威海',has_data:true},
  ];
  const overviewMaps=[
    {map:'level_001_lake',map_name:'克马羚诺也湖',air_min_c:7.3,air_max_c:21.2,water_min_c:10.3,water_max_c:23,season_name:'秋'},
    {map:'level_009_ahtuba',map_name:'阿赫图巴河',air_min_c:12.3,air_max_c:39,water_min_c:11.5,water_max_c:32.2,season_name:'秋'},
    {map:'level_012_north_sea',map_name:'挪威海',air_min_c:1.7,air_max_c:9,water_min_c:7.3,water_max_c:16.7,season_name:'秋'},
  ];
  let previewCalls=[];
  const server=http.createServer((req,res)=>{
    const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
    const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
    if(key==='/api/me')return json({username:'admin',item_categories:{}});
    if(key==='/api/weather/maps')return json({maps});
    if(key==='/api/weather/preview'){
      const m=url.searchParams.get('map');
      previewCalls.push({map:m,days:url.searchParams.get('days')});
      if(!m)return json({now_day:NOW_DAY,game_now:0,days:7,maps:overviewMaps});
      const name=(maps.find(x=>x.key===m)||{}).map_name||m;
      return json({preview:mkPreview(m,name),now_day:NOW_DAY,now_hour:2,game_now:0});
    }
    if(key==='/api/weather/active-fish'){
      const m=url.searchParams.get('map');
      return json({map_name:(maps.find(x=>x.key===m)||{}).map_name||m,game_now:0,active:mkActive(m)});
    }
    if(key.startsWith('/api/'))return json({});
    res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
  });
  await new Promise(r=>server.listen(0,'127.0.0.1',r));
  let browser;
  try{
    browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
    const context=await browser.newContext({viewport:{width:1600,height:1100}});
    await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture')});
    const page=await context.newPage(),errors=[];
    page.on('pageerror',e=>errors.push(e.message));
    await page.goto(`http://127.0.0.1:${server.address().port}/admin/#weather`,{waitUntil:'networkidle'});

    // 1) 默认选中阿赫图巴河，状态行给出图名/天数/季节。
    await page.locator('#wxDayCards .wx-day').first().waitFor();
    assert.equal(await page.locator('#wxMap').inputValue(),'level_009_ahtuba');
    // 下拉必须真有可读的选项文本——只查 inputValue 会漏掉「文本全空」的下拉。
    const opts=await page.locator('#wxMap option').evaluateAll(ns=>ns.map(n=>({v:n.value,t:n.textContent.trim()})));
    assert.equal(opts.length,3);
    for(const o of opts){
      assert.ok(o.t.length>0,`下拉选项 ${o.v} 的文本为空`);
      assert.notEqual(o.t,o.v,'下拉应显示中文图名而不是 map_key');
    }
    assert.equal(opts.find(o=>o.v==='level_009_ahtuba').t,'阿赫图巴河');
    const status=await page.locator('#wxStatus').innerText();
    assert.match(status,/阿赫图巴河/);
    assert.match(status,/7 天/);
    assert.match(status,/秋/);

    // 2) 7 张日卡片，今天那张带「现在」角标；游戏时钟显示真实分钟。
    assert.equal(await page.locator('#wxDayCards .wx-day').count(),7);
    assert.equal(await page.locator('#wxDayCards .wx-day.now').count(),1);
    assert.match(await page.locator('#wxSum').innerText(),/02:31/);
    // 卡片里有天气图标 SVG 与水温区间。
    assert.ok(await page.locator('#wxDayCards .wx-day').first().locator('svg').count()>0);
    assert.match(await page.locator('#wxDayCards .wx-day').first().innerText(),/水 \d/);

    // 3) 温度曲线：四条温度线 + 当前时刻竖线 + 24 个小时刻度（每 3 小时一个）。
    const svg=page.locator('#wxChart svg');
    await svg.waitFor();
    assert.equal(await svg.locator('polyline').count(),4);
    assert.match(await svg.innerHTML(),/#42b8ff/); // 当前时刻竖线颜色
    assert.equal(await svg.locator('text').filter({hasText:/^\d\d:00$/}).count(),8);

    // 4) 温度区间：分档条存在，行数与有样本的档位一致；合计覆盖 168 小时。
    const bandBox=page.locator('#wxBandBox');
    assert.ok(await bandBox.locator('.wx-band i').count()>0);
    const bandText=await bandBox.innerText();
    assert.match(bandText,/168 个采样点/);

    // 5) 四季：四条，恰好一条标当前季（带 ● 且 class=now）。
    assert.equal(await page.locator('#wxSeasonBox .sr').count(),4);
    assert.equal(await page.locator('#wxSeasonBox .sr.now').count(),1);
    assert.match(await page.locator('#wxSeasonBox .sr.now').innerText(),/秋/);

    // 6) 点第 3 天卡片：选中态移动，曲线跟着换（该日 04:xx 时刻的刻度数量不变）。
    const third=page.locator('#wxDayCards .wx-day').nth(2);
    await third.click();
    assert.equal(await page.locator('#wxDayCards .wx-day.on').count(),1);
    assert.ok(await page.locator('#wxDayCards .wx-day').nth(2).evaluate(el=>el.classList.contains('on')));
    // 非今天不该有当前时刻竖线（fixture 只把 is_now 给了 day 0）。
    assert.doesNotMatch(await page.locator('#wxChart svg').innerHTML(),/42b8ff/);

    // 7) 全部水域对比：三行，点挪威海那行会把下拉切过去并重载。
    await page.locator('button:has-text("全部水域对比")').click();
    await page.locator('#wxCmpBox table').waitFor();
    assert.equal(await page.locator('#wxCmpBox tbody tr').count(),3);
    const cmpText=await page.locator('#wxCmpBox').innerText();
    assert.match(cmpText,/阿赫图巴河/);
    assert.match(cmpText,/挪威海/);
    await page.locator('#wxCmpBox tbody tr').filter({hasText:'挪威海'}).click();
    await page.waitForFunction(()=>document.querySelector('#wxMap').value==='level_012_north_sea');
    assert.ok(previewCalls.some(c=>c.map==='level_012_north_sea'));

    // 8) 改天数下拉会重新请求，并把 days 传给后端。
    await page.selectOption('#wxDays','14');
    await page.waitForFunction(()=>true);
    assert.ok(previewCalls.some(c=>c.days==='14'));

    // 9) 当前活跃鱼种：活跃 3 条单独成行，不活跃 2 条折在 details 里且写明原因。
    await page.locator('#wxFishBox .wx-frow:not(.wx-fhead)').first().waitFor();
    const activeRows=page.locator('#wxFishBox .wx-frow:not(.wx-fhead):not(.idle)');
    assert.equal(await activeRows.count(),3,'活跃鱼应正好 3 行');
    // 排序契约：综合系数必须递减（后端已排好，前端不得再乱序）。
    const scores=await page.locator('#wxFishBox .wx-frow:not(.wx-fhead):not(.idle) .num.sc')
      .evaluateAll(ns=>ns.map(n=>parseFloat(n.textContent)));
    assert.equal(scores.length,3);
    for(let i=1;i<scores.length;i++){
      assert.ok(scores[i-1]>=scores[i],`综合系数应降序：${scores.join(' > ')}`);
    }
    // 头部三个 chip：活跃比、时段、水温。
    const fishSum=await page.locator('#wxFishSum').innerText();
    assert.match(fishSum,/3/);
    assert.match(fishSum,/5/);
    assert.match(fishSum,/夜/);
    assert.match(fishSum,/18\.4/);
    // 稀有度标签要能区分档位（超稀有/稀有不能都渲染成普通）。
    const boxText=await page.locator('#wxFishBox').innerText();
    assert.match(boxText,/超稀有/);
    assert.match(boxText,/稀有/);
    assert.match(boxText,/河鲈/);
    // 不活跃段默认折叠，展开后能看到原因。
    await page.locator('#wxFishBox .wx-fidle summary').click();
    const idleText=await page.locator('#wxFishBox .wx-fidle').innerText();
    assert.match(idleText,/非活跃时段/);
    assert.match(idleText,/水温偏离最适/);
    // 口径解释必须写在页面上，否则管理员会以为这是「一定能钓到」。
    assert.match(boxText,/不含饵料/);

    // 10) 切图时活跃鱼种要跟着换（它按 map 单独请求）。
    await page.selectOption('#wxMap','level_012_north_sea');
    await page.waitForFunction(()=>document.querySelector('#wxStatus').textContent.includes('挪威海'));
    await page.locator('#wxFishBox .wx-frow:not(.wx-fhead)').first().waitFor();
    assert.equal(await page.locator('#wxFishBox .wx-frow:not(.wx-fhead):not(.idle)').count(),3);

    assert.deepEqual(errors,[]);
  }finally{
    if(browser)await browser.close();
    await new Promise(r=>server.close(r));
  }
});