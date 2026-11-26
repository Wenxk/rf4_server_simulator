const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');

test('bait rule editor supports cascade, whole-category terms and unsaved close guard',async()=>{
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
    allow:['测试蚯蚓'],deny:[],bottom_only:[],condition:''
   },{
    fish_key:'c.perch',name_zh:'河鲈',has_rule:true,source:'override',enabled:true,
    allow:['测试蚯蚓'],deny:[],bottom_only:[],condition:'海图120号区域'
   }]})});
   // 夹具按 2026-11-25 后的真实形状：大类 label 与 term 一致（都是「鱼饵」），
   // 二级类型带 term（写进规则的就是这个整类词），且 brands/lines 供型号层使用。
   if(url.pathname==='/api/bait/categories')return route.fulfill({contentType:'application/json',body:JSON.stringify({categories:[
    {key:27,label:'鱼饵',count:1,term:'鱼饵',children:[
      {sub:6,label:'测试饵',count:1,term:'虫类',brands:[
        {kind:'',label:'',count:1,lines:[{base:'测试蚯蚓',count:1,term:'测试蚯蚓'}]}
      ]}
    ]},
    {key:17,label:'拟饵',count:0,term:'拟饵',children:[]}
   ],terms:[
    {term:'各类饵',label:'全部（鱼饵 + 拟饵）',cat:0,sub:0,count:1,alias:false,shape:''},
    {term:'鱼饵',label:'鱼饵（全部天然饵）',cat:27,sub:0,count:1,alias:false,shape:''},
    {term:'虫类',label:'虫类（幼虫 / 红虫）',cat:27,sub:6,count:1,alias:false,shape:''},
    {term:'拟饵',label:'拟饵（全部路亚）',cat:17,sub:0,count:0,alias:false,shape:''}
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
  await page.goto('http://127.0.0.1:19214/#ecology',{waitUntil:'networkidle'});
  // 鱼饵偏好已并入「鱼种档案」的第 2 个页签。
  await page.locator('#ecoTabs button[data-etab="bait"]').click();
  await page.locator('#baitTable thead th').first().waitFor();
  const heads=await page.locator('#baitTable thead th').allInnerTexts();
  assert.deepEqual(heads.slice(0,3),['鱼种','自动抽取','本期开口']);
  // 2026-11-24 按用户要求删掉「偏好口（按类）」「饵效证据」「吃不吃」三列。
  for(const gone of ['偏好口（按类）','饵效证据','吃不吃']){
   assert.ok(!heads.some(h=>h.includes(gone)),`已删列仍在表头：${gone} → ${heads.join('|')}`);
  }

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
  await page.locator('#brCondType').waitFor();
  assert.equal(await page.locator('#brInterval').count(),0,'手工轮换间隔已删除');
  assert.equal(await page.locator('#brMode').count(),0,'手工轮换模式已删除');
  assert.equal(await page.getByText('定时轮换（手工编组）',{exact:true}).count(),0,'手工轮换组已删除');

  // 不吃：走层级选择器，收到具体饵再加。
  // 2026-11-25 选择器从「两级下拉 + 平铺列表」重构成「类型 → 品牌 → 型号 → 规格」级联，
  // 旧断言用的 #brPickCat / #brPickList 已不存在。这里只保留本文件该管的语义：
  // 能从入口进去、能把选中的词加进当前 bucket。级联本身由
  // bait_picker_cascade_ui_test.cjs 专测，不在这里重复。
  await page.getByRole('button',{name:'＋ 添加饵'}).nth(1).click();
  await page.locator('.br-layer').first().waitFor();
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'鱼饵'}).click();
  await page.locator('.br-layer button.br-opt').filter({hasText:'测试饵'}).click();
  await page.locator('button.br-opt.add').filter({hasText:'加入整类'}).click();
  // 多选后弹窗不再自动关闭（用户 2026-11-25 要求多选），要点「一次性加入」才落进 bucket。
  await page.locator('.br-pickbar .acts button.btn.good').click();
  await page.locator('#brCondType').waitFor();
  assert.ok(await page.locator('#modal .tag').filter({hasText:'虫类'}).count()>=1,
    '整类词应能加进「不吃」bucket');

  // 已经加过的整类，再打开选择器时按钮应 disabled，防止重复加入。
  await page.getByRole('button',{name:'＋ 添加饵'}).nth(1).click();
  await page.locator('.br-layer').first().waitFor();
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'鱼饵'}).click();
  await page.locator('.br-layer button.br-opt').filter({hasText:'测试饵'}).click();
  assert.equal(await page.locator('button.br-opt.add[disabled]').count(),1,
    '已加入的整类词应禁用，不可重复添加');
  await page.getByRole('button',{name:'返回编辑'}).click();
  await page.locator('#brCondType').waitFor();

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
  assert.equal(saved.groups,undefined,'保存不应再带手工轮换组');
  assert.equal(saved.rotate_sec,undefined,'保存不应再带手工轮换间隔');

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
