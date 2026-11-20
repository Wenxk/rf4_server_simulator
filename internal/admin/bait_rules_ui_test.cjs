const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');

test('bait rule editor supports cascade, rotation groups and unsaved close guard',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  let saved=null,autoPayload=null;
  await page.route('**/*',route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/')return route.fulfill({contentType:'text/html; charset=utf-8',body:html});
   if(url.pathname==='/api/me')return route.fulfill({contentType:'application/json',body:JSON.stringify({username:'admin'})});
   if(url.pathname==='/api/bait/prefs')return route.fulfill({contentType:'application/json',body:JSON.stringify({rules_enabled:true,rule_count:1,rows:[{
    fish_key:'c.roach',name_zh:'常见拟鲤',has_rule:true,source:'override',enabled:true,
    allow:['测试蚯蚓'],deny:[],bottom_only:[],condition:'',groups:[['测试蚯蚓']],
    rotate_sec:600,rotate_mode:'soft',tier3:0,tier2:1,total:1,baits:[],in_strict:true,
    rotation:{enabled:false,mode:'soft',total:1,active:-1,active_baits:[],interval_sec:600,next_switch:0}
   },{
    fish_key:'c.perch',name_zh:'河鲈',has_rule:true,source:'override',enabled:true,
    allow:['测试蚯蚓'],deny:[],bottom_only:[],condition:'海图120号区域',groups:[['测试蚯蚓']],
    rotate_sec:600,rotate_mode:'soft',tier3:0,tier2:1,total:1,baits:[],in_strict:true,
    rotation:{enabled:false,mode:'soft',total:1,active:-1,active_baits:[],interval_sec:600,next_switch:0}
   }]})});
   if(url.pathname==='/api/bait/categories')return route.fulfill({contentType:'application/json',body:JSON.stringify({categories:[
    {key:27,label:'饵料',count:1,children:[{sub:6,label:'测试饵',count:1}]},
    {key:17,label:'拟饵',count:0,children:[]}
   ]})});
   if(url.pathname==='/api/bait/items')return route.fulfill({contentType:'application/json',body:JSON.stringify({rows:[
    {item_id:'test_worm',name_zh:'测试蚯蚓',category:27,sub:6,sub_label:'测试饵'}
   ],total:1})});
   if(url.pathname==='/api/bait/auto-rotation'&&route.request().method()==='POST'){
    autoPayload=JSON.parse(route.request().postData()||'{}');
    return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
   }
   if(url.pathname==='/api/bait/auto-rotation/reroll'){
    return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true,round:7})});
   }
   if(url.pathname==='/api/bait/rules'&&route.request().method()==='PUT'){
    saved=JSON.parse(route.request().postData()||'{}');
    return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
   }
   return route.fulfill({contentType:'application/json',body:'{}'});
  });
  await page.goto('http://127.0.0.1:19214/#bait',{waitUntil:'networkidle'});
  // 自动抽取是表格里的独立开关列，本期开口单独一列（不再混在规则列里）。
  const heads=await page.locator('#baitTable thead th').allInnerTexts();
  assert.deepEqual(heads.slice(0,3),['鱼种','自动抽取','本期开口']);

  // 立即重抽：需要二次确认，确认后 POST 到重抽接口（不能直接改表格状态骗人）。
  const [rerollReq]=await Promise.all([
   page.waitForRequest(r=>r.url().includes('/api/bait/auto-rotation/reroll')),
   (async()=>{
    await page.getByRole('button',{name:'↻ 立即重抽本期'}).click();
    await page.getByRole('button',{name:'确认执行'}).click();
   })()
  ]);
  assert.equal(rerollReq.method(),'POST');
  await page.locator('#baitTable .bait-edit').first().waitFor();
  const autoBox=page.locator('#baitTable tbody tr').first().locator('td').nth(1).locator('input[type=checkbox]');
  assert.equal(await autoBox.count(),1);
  await autoBox.check();
  await page.waitForFunction(()=>document.querySelector('#baitTable tbody tr td:nth-child(2) input')!==null);
  assert.deepEqual(autoPayload,{fish_key:'c.roach',enabled:true});
  await page.locator('#baitTable .bait-edit').first().click();
  await page.locator('#brInterval').waitFor();
  assert.equal(await page.locator('#brInterval').inputValue(),'10');
  assert.equal(await page.locator('#brMode').inputValue(),'soft');
  assert.equal(await page.locator('#brMode option[value="auto"]').count(),0,'自动抽取已移出轮换模式下拉');
  assert.equal(await page.getByText('轮换组 1',{exact:true}).count(),1);
  await page.getByRole('button',{name:'＋ 新增一组'}).click();
  assert.equal(await page.getByText('轮换组 2',{exact:true}).count(),1);
  await page.getByRole('button',{name:'＋ 添加饵'}).last().click();
  await page.locator('#brPickCat').waitFor();
  await page.locator('#brPickCat').selectOption('27');
  await page.locator('#brPickList .result').filter({hasText:'测试蚯蚓'}).click();
  await page.locator('#brInterval').waitFor();
  assert.ok(await page.locator('#modal .tag').filter({hasText:'测试蚯蚓'}).count()>=2);

  let guarded=false;
  page.once('dialog',async d=>{guarded=true;assert.match(d.message(),/未保存/);await d.dismiss();});
  await page.locator('#modal .section-head button').click();
  await page.waitForTimeout(100);
  assert.equal(guarded,true);
  assert.equal(await page.locator('#modalBg').evaluate(e=>e.classList.contains('on')),true);

  page.once('dialog',async d=>{assert.match(d.message(),/未保存/);await d.accept();});
  await page.locator('#modal .section-head button').click();
  await page.waitForFunction(()=>!document.getElementById('modalBg').classList.contains('on'));
  assert.equal(saved,null);

  // 前置条件是下拉：空值打开=「不限制」，选「海图·指定区号」后按区号拼回服务端认的原句。
  await page.locator('#baitTable .bait-edit').first().click();
  await page.locator('#brCondType').waitFor();
  assert.equal(await page.locator('#brCondType').inputValue(),'none');
  assert.equal(await page.locator('#brCondZones').count(),1);
  await page.locator('#brCondType').selectOption('chart');
  await page.locator('#brCondZones').fill('30 / 34');
  await page.getByRole('button',{name:'保存'}).click();
  await page.waitForFunction(()=>!document.getElementById('modalBg').classList.contains('on'));
  assert.equal(saved.condition,'海图30或34号区域');

  // 已有条件要能反解回下拉：海图120号区域 -> chart + 120，保存后原样回写。
  await page.locator('#baitTable .bait-edit').nth(1).click();
  await page.locator('#brCondType').waitFor();
  assert.equal(await page.locator('#brCondType').inputValue(),'chart');
  assert.equal(await page.locator('#brCondZones').inputValue(),'120');
  await page.getByRole('button',{name:'保存'}).click();
  await page.waitForFunction(()=>!document.getElementById('modalBg').classList.contains('on'));
  assert.equal(saved.condition,'海图120号区域');
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();}
});
