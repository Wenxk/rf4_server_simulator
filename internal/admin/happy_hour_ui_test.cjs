const {test}=require('node:test');
const assert=require('node:assert/strict');
const {chromium}=require('playwright');
const {serveAsset}=require('./web_fixture.cjs');

// 欢乐时光：总开关 + 剩余时长文案在窄屏/高 DPI 下不溢出。
//
// 迁移时的取舍（批次 6）：
//   - 旧后台的 `#happyHourSwitch` → SPA 规则页的 Form bool 行，id 由
//     `f_<key>_<seq>` 生成，所以按 `input[id^="f_happy_hour_enabled_"]` 定位。
//   - 旧后台玩家页有专用 `#playerHappyHour` 时钟组件；SPA 把它并进侧栏一句
//     hint（「欢乐时光有效，剩余约 N 分钟。」）。断言跟着验这句话，不再量一个
//     已不存在的小组件——把「必须有一个独立时钟元素」钉在新实现上是反模式。
//     真正要守的是：剩余分钟数算得对、长文案不撑破侧栏。
test('happy hour switch and remaining time fit supported sizes and long text',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||undefined});
 const measurements=[];
 try{
  for(const width of [1280,1024,360])for(const dpi of [1,1.25,1.5]){
   const writes=[];
   // 剩余秒数按场景改。page.route 是「先注册者优先」，所以不能每轮再注册一条
   // 覆盖用的 handler——新的会被忽略，detail 永远返回第一轮的值（这里踩过）。
   let remainingSec=3600;
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:dpi});
   await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
   const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/*',route=>{
    const url=new URL(route.request().url()),key=url.pathname.replace(/^\/admin/,'');
    if(key.startsWith('/api/')){
     if(key==='/api/tunables'&&route.request().method()==='POST'){
      const body=route.request().postDataJSON();writes.push(body);
      return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
     }
     if(key==='/api/players/detail')return route.fulfill({contentType:'application/json',body:JSON.stringify({
       uid:1,name:'测试玩家',level:25,xp:0,silver:0,gold:0,bank:0,membership:1,membership_exp:'',membership_active:true,
       item_count:0,keepnet_count:0,mail_count:0,banned:false,items:[],keepnet:[],mails:[],skills:[],
       skill_total:0,skill_spent:0,skill_bonus:0,available_points:0,
       happy_hour:{enabled:true,qualified:true,unlimited:false,expires_at:0,active:remainingSec>0,remaining_seconds:remainingSec},
     })});
     if(key==='/api/players')return route.fulfill({contentType:'application/json',body:JSON.stringify([
       {uid:1,name:'测试玩家',level:25,online:false,online_known:true,banned:false},
     ])});
     const data=key==='/api/me'?{username:'admin'}
       :key==='/api/tunables'?{fish_bait_rules_enabled:'0',fish_distribution_enabled:'0',fish_weather_enabled:'1'}
       :key==='/api/fish/bite_mode'?{mode:'mixed'}
       :null;
     return route.fulfill({contentType:'application/json',body:JSON.stringify(data??[])});
    }
    if(serveAsset(route,url))return;
    return route.fulfill({status:404,body:''});
   });
   await page.goto('http://127.0.0.1:19214/admin/#/rules/switches');
   // 六项规则里的第 6 项：欢乐时光。默认开（后端 def='1'）。
   const sw=page.locator('input[id^="f_happy_hour_enabled_"]');
   await sw.waitFor();
   assert.equal(await sw.isChecked(),true,'欢乐时光默认应为开');
   await sw.click();
   await page.waitForFunction(()=>{const e=document.querySelector('input[id^="f_happy_hour_enabled_"]');return e&&!e.disabled&&!e.checked;});
   // 关掉它不该顺带改动别的开关。
   assert.equal(await page.locator('input[id^="f_fish_bait_rules_enabled_"]').isChecked(),false);
   assert.equal(await page.locator('input[id^="f_fish_distribution_enabled_"]').isChecked(),false);

   // 保存：只写这一个键。
   await page.getByRole('button',{name:/保存/}).first().click();
   await page.waitForFunction(()=>document.querySelector('input[id^="f_happy_hour_enabled_"]')?.checked===false);
   assert.ok(writes.length>0,'点保存后应有写入');
   const hh=writes.filter(w=>w.key==='happy_hour_enabled');
   assert.deepEqual(hh, [{key:'happy_hour_enabled',value:'0'}], '应只写欢乐时光这一个键：'+JSON.stringify(writes));

   // 剩余时长文案：三档剩余秒数，分钟数要按 ceil 换算，且不撑破文档宽度。
   for(const [scenario,remaining] of [['normal',3600],['chinese',1800],['zero',0]]){
    remainingSec=remaining;
    // 用不同的 hash 强制一次真实重载：goto 到同一个 URL 时浏览器不一定重新解析
    // 文档，页面会停在上一轮的数据上（这里实际踩到，三个场景都拿到第一轮的值）。
    // 必须 reload 而不是改 hash 后再 goto：只改 hash 只是换路由，SPA 复用同一
    // 个页面实例、不会再请求 detail，拿到的还是上一轮的数据。
    await page.goto('http://127.0.0.1:19214/admin/#/players/1',{waitUntil:'networkidle'});
    if(scenario!=='normal')await page.reload({waitUntil:'networkidle'});
    await page.waitForFunction(()=>document.querySelector('.work aside'));
    // 取「含 UID 的那块 aside」：页面上 aside 不止一处，规则页、hero 要点块都会
    // 命中 .work aside。按内容筛比重排位置稳——innerText 在多元素时也只取第一个。
    const hintText=await page.locator('.work aside').filter({hasText:'这份档案'}).or(
      page.locator('.work aside').filter({hasText:'这个账号'})).first().innerText();
    // 「剩余约 N 分钟」：3600 秒 → 60 分钟，1800 → 30，0 → 0。
    assert.match(hintText,new RegExp('剩余约 '+Math.ceil(remaining/60)+' 分钟'),'剩余时长换算不对（'+scenario+'）：'+hintText);
    const box=await page.evaluate(()=>({scroll:document.documentElement.scrollWidth,width:innerWidth}));
    assert.ok(box.scroll<=width+1,JSON.stringify({width,dpi,scenario,box}));
    measurements.push({width,dpi,scenario,remaining,scroll:box.scroll});
   }
   assert.deepEqual(errors,[]);await context.close();
  }
 }finally{await browser.close();}
 console.log(JSON.stringify({renderedCases:measurements.length}));
});