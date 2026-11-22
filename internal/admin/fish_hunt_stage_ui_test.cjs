const {test}=require('node:test');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');

// 鱼猎四阶段 4×4 表格：默认值、实时占比、保存时的 16 次归一写入。
test('fish hunt four-stage weights are editable and normalized on save',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||undefined});
 try{
  const writes=[];
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/*',route=>{
   const url=new URL(route.request().url()),key=url.pathname.replace(/^\/admin/,'');
   if(url.pathname==='/admin/')return route.fulfill({contentType:'text/html; charset=utf-8',body:html});
   let data=key==='/api/me'?{username:'admin'}:[];
   if(key==='/api/tunables'){
    if(route.request().method()==='POST'){
     const body=route.request().postDataJSON();
     if(String(body.key).startsWith('hunt_stage_'))writes.push(body);
     return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
    }
    data={grade_ratio_enabled:'0',fish_hunt_enabled:'1',two_stage_hunt_enabled:'0'};
   }
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)});
  });
  await page.goto('http://127.0.0.1:19231/admin/#params');
  await page.evaluate(()=>setParamsPage('reward'));
  const daily=page.locator('#fs_daily_0');
  await daily.waitFor();
  assert.equal(await daily.inputValue(),'74.7');
  assert.equal(await page.locator('#fs_giant_3').inputValue(),'6');
  assert.match(await page.locator('#fs_share_daily').textContent(),/^74\.7% \/ 24\.0% \/ 1\.0% \/ 0\.3%$/);

  // 改一格，实时占比按四档之和重新归一：74.7/24/9/0.3 → 69.2/22.2/8.3/0.3
  await page.locator('#fs_daily_2').fill('9');
  assert.match(await page.locator('#fs_share_daily').textContent(),/^69\.2% \/ 22\.2% \/ 8\.3% \/ 0\.3%$/);

  await page.getByRole('button',{name:'保存四阶段比例'}).click();
  for(let i=0;i<60&&writes.length<16;i++)await page.waitForTimeout(100);
  assert.equal(writes.length,16);
  const byKey=Object.fromEntries(writes.map(w=>[w.key,Number(w.value)]));
  const sum=['below','qualified','trophy','super'].reduce((n,k)=>n+byKey['hunt_stage_daily_'+k],0);
  assert.ok(Math.abs(sum-100)<0.02,`四档应归一到 100，实得 ${sum}`);
  assert.ok(byKey['hunt_stage_daily_trophy']>8.2&&byKey['hunt_stage_daily_trophy']<8.5,'奖杯档应按归一后的 8.3% 写入');
  assert.ok(byKey['hunt_stage_giant_super']>5.9&&byKey['hunt_stage_giant_super']<6.1,'未改动的行也按当前值写回');
  assert.deepEqual(errors,[]);
 }finally{await browser.close()}
});
