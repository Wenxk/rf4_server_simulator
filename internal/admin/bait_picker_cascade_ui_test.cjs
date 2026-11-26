const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');

// 鱼饵层级选择器：类型 → 品牌 → 型号 → 规格，一级一级往下收，越深越窄。
// 夹具刻意做成真实形状：拟饵 17 → 旋转亮片路亚(1) → 品牌 zeiman → 型号 Spiker #2 → 18 个规格，
// 以及天然饵 27 → 香煮成饵(8) 带「下沉 / Pop-up」两种形态。
// 只有形状对得上，才能真的测出「层级越深收束越窄」和「写进去的是哪个词」。
const CATEGORIES={
  categories:[
    {key:27,label:'鱼饵',count:578,term:'鱼饵',children:[
      {sub:1,label:'自然的',count:21,term:'自然的',brands:[
        {kind:'',label:'',count:21,lines:[{base:'一束藻类',count:3,term:'一束藻类'}]}
      ]},
      {sub:8,label:'香煮成饵（下沉 + Pop-up）',count:578,term:'香煮成饵',
       shapes:[
         {shape:'sink',label:'下沉香煮成饵',term:'下沉香煮成饵',count:356},
         {shape:'popup',label:'Pop-up 香煮成饵',term:'Pop-up 香煮成饵',count:222}
       ],
       brands:[
         {kind:'craft_baits',label:'craft_baits',count:155,lines:[
           {base:'基础的橙色',count:6,term:'基础的橙色'},
           // 跨类别同名：`鱼子` 在 香煮成饵 与 颗粒 两处都有（真实库 3 处，14 款）。
           // 后端算法：同名的第二个类别必须给出 scope_term，否则一个 `鱼子`
           // 会把另外几个类别下的同名饵一起放行。
           {base:'鱼子',count:4,term:'鱼子',scope_term:'香煮成饵@鱼子',shared_with:['颗粒']}
         ]}
       ]},
      {sub:9,label:'颗粒',count:3,term:'颗粒',brands:[
        {kind:'craft_baits',label:'craft_baits',count:3,lines:[
          {base:'鱼子',count:3,term:'鱼子',scope_term:'颗粒@鱼子',shared_with:['香煮成饵']}
        ]}
      ]}
    ]},
    {key:17,label:'拟饵',count:296,term:'拟饵',children:[
      {sub:1,label:'旋转亮片路亚',count:296,term:'旋转亮片路亚',brands:[
        {kind:'zeiman',label:'zeiman',count:72,lines:[
          {base:'Spiker #2',count:18,term:'Spiker #2'},
          {base:'Hornet #1',count:18,term:'Hornet #1'}
        ]},
        {kind:'syberia',label:'syberia',count:71,lines:[
          {base:'Dragonfly Original 1',count:8,term:'Dragonfly Original 1'}
        ]}
      ]}
    ]}
  ],
  terms:[
    {term:'各类饵',label:'全部（鱼饵 + 拟饵）',cat:0,sub:0,count:874,alias:false,shape:''},
    {term:'鱼饵',label:'鱼饵（全部天然饵）',cat:27,sub:0,count:578,alias:false,shape:''},
    {term:'拟饵',label:'拟饵（全部路亚）',cat:17,sub:0,count:296,alias:false,shape:''},
    {term:'香煮成饵',label:'香煮成饵',cat:27,sub:8,count:578,alias:false,shape:''},
    {term:'下沉香煮成饵',label:'下沉香煮成饵',cat:27,sub:8,count:356,alias:false,shape:'sink'},
    {term:'Pop-up 香煮成饵',label:'Pop-up 香煮成饵',cat:27,sub:8,count:222,alias:false,shape:'popup'}
  ]
};

// Spiker #2 下的 18 个规格：编号 001..018，全部 base 都是 `Spiker #2`。
const SPIKER=Array.from({length:18},(_,i)=>({
  item_id:'spk2_'+String(i+1).padStart(3,'0'),
  name_zh:'Spiker #2 '+String(i+1).padStart(3,'0'),
  category:17,sub:1,sub_label:'旋转亮片路亚',
  brand:'zmn_spiker2',kind:'zeiman',base:'Spiker #2'
}));

// 读 bucket 里所有标签的纯文本（不含「×」删除按钮）。
// 不能用 childNodes[0].nodeValue：类别限定标签 `颗粒@鱼子` 的第一个子节点是
// <span class="sc">（元素节点，nodeValue 为 null），会取到空串。
async function tagTexts(page){
  return await page.locator('#modal .tag').evaluateAll(
    els=>els.map(e=>e.textContent.replace(/\s*×\s*$/,'').trim())
  );
}

function rowsFor(url){
  const q=url.searchParams;
  const cat=Number(q.get('cat')||0),sub=Number(q.get('sub')||0);
  const kind=q.get('kind')||'',base=q.get('base')||'';
  let pool=[];
  if(cat===17&&sub===1&&base==='Spiker #2')pool=SPIKER.slice();
  else if(cat===17&&sub===1)pool=SPIKER.slice();
  else if(cat===27&&sub===8)pool=[{item_id:'b1',name_zh:'甜奶油 16',category:27,sub:8,sub_label:'香煮成饵',brand:'hvz_boils_pp_van',kind:'craft_baits',base:'甜奶油'}];
  else if(cat===27&&sub===1)pool=[{item_id:'a1',name_zh:'一束藻类 001',category:27,sub:1,sub_label:'自然的',brand:'',kind:'',base:'一束藻类'}];
  if(kind)pool=pool.filter(x=>x.kind===kind);
  return {rows:pool,matched:pool.length,total:874};
}

test('bait picker cascades type → brand → model → spec and narrows at each level',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1400,height:1000}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  let saved=null;
  const itemCalls=[];
  await page.route('**/*',route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/')return route.fulfill({contentType:'text/html; charset=utf-8',body:html});
   if(url.pathname==='/api/me')return route.fulfill({contentType:'application/json',body:JSON.stringify({username:'admin'})});
   if(url.pathname==='/api/bait/prefs')return route.fulfill({contentType:'application/json',body:JSON.stringify({rules_enabled:true,rule_count:1,rows:[{
     fish_key:'c.perch',name_zh:'河鲈',has_rule:true,source:'override',enabled:true,
     allow:[],deny:[],bottom_only:[],condition:''
   }]})});
   if(url.pathname==='/api/bait/categories')return route.fulfill({contentType:'application/json',body:JSON.stringify(CATEGORIES)});
   if(url.pathname==='/api/bait/items'){
     itemCalls.push(url.search);
     return route.fulfill({contentType:'application/json',body:JSON.stringify(rowsFor(url))});
   }
   if(url.pathname==='/api/bait/rules'&&route.request().method()==='PUT'){
     saved=JSON.parse(route.request().postData()||'{}');
     return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
   }
   if(url.pathname==='/api/bait/auto-rotation/reroll')return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true,round:1})});
   return route.fulfill({contentType:'application/json',body:'{}'});
  });
  await page.goto('http://127.0.0.1:19215/#ecology',{waitUntil:'networkidle'});
  await page.locator('#ecoTabs button[data-etab="bait"]').click();
  await page.locator('#baitTable .bait-edit').first().click();
  await page.locator('#brCondType').waitFor();

  // 打开「吃」bucket 的选择器（第 1 个「＋ 添加饵」）。
  await page.getByRole('button',{name:'＋ 添加饵'}).first().click();
  await page.locator('.br-layer').first().waitFor();

  const layers=()=>page.locator('.br-layer .k').allInnerTexts();
  // --- 第 1 层：类型 ---
  let ks=await layers();
  assert.ok(ks.includes('类型'),'缺类型层：'+ks.join('|'));

  // --- 选拟饵：出现二级类型，且不出现品牌/型号 ---
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'拟饵'}).click();
  ks=await layers();
  assert.ok(!ks.includes('品牌'),'未选二级类型时不该出现品牌层：'+ks.join('|'));
  assert.ok(!ks.includes('型号'),'未选二级类型时不该出现型号层：'+ks.join('|'));

  // --- 选「旋转亮片路亚」（二级类型）：出现品牌 + 型号 ---
  await page.locator('.br-layer button.br-opt').filter({hasText:'旋转亮片路亚'}).click();
  ks=await layers();
  assert.ok(ks.includes('品牌'),'选类型后应出现品牌层：'+ks.join('|'));
  assert.ok(ks.includes('型号'),'选类型后应出现型号层：'+ks.join('|'));
  // 未选品牌时，所有品牌的型号平铺
  let modelBtns=await page.locator('.br-layer').filter({has:page.locator('.k', {hasText:'型号'})}).locator('button.br-opt').allInnerTexts();
  assert.ok(modelBtns.some(x=>x.includes('Spiker #2')),'型号层应含 Spiker #2：'+modelBtns.join('|'));
  assert.ok(modelBtns.some(x=>x.includes('Dragonfly Original 1')),'未选品牌时应平铺所有品牌型号：'+modelBtns.join('|'));

  // --- 选品牌 zeiman：型号层只剩该品牌型号（收束变窄）---
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'品牌'})}).locator('button.br-opt').filter({hasText:'zeiman'}).click();
  modelBtns=await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'型号'})}).locator('button.br-opt').allInnerTexts();
  assert.ok(modelBtns.some(x=>x.includes('Spiker #2')),'zeiman 下应有 Spiker #2');
  assert.ok(!modelBtns.some(x=>x.includes('Dragonfly')),'选 zeiman 后不该再出现 syberia 的型号：'+modelBtns.join('|'));

  // --- 选型号 Spiker #2：收束范围预览要写出「型号名」，并提示覆盖全部规格 ---
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'型号'})}).locator('button.br-opt').filter({hasText:'Spiker #2'}).click();
  const preview=await page.locator('.br-preview').innerText();
  assert.ok(preview.includes('拟饵 › 旋转亮片路亚 › zeiman › Spiker #2'),'收束路径不对：'+preview);
  assert.match(preview,/Spiker #2/);
  // 关键：预览必须说清「写进规则的是型号名」，不是某个规格
  assert.match(preview,/写进规则的是/);
  const codeTerm=await page.locator('.br-preview code').innerText();
  assert.equal(codeTerm,'Spiker #2','写进规则的词应是型号名');

  // --- 规格层：选了型号才拉规格，且只列该型号的 ---
  // 必须先验证规格层再点「加入型号」——加入后弹窗会关掉回到编辑视图，
  // #brSpecBox 就不存在了（顺序写反会 30s 超时）。
  assert.ok(itemCalls.some(s=>s.includes('base=Spiker')),'选型号后应请求规格列表：'+itemCalls.join(' ; '));
  const specText=await page.locator('#brSpecBox').innerText();
  assert.match(specText,/该型号共 18 个规格/,'规格数应取 matched，不是 rows 长度：'+specText);
  assert.match(specText,/只加入型号名就已全部覆盖/);
  const specCount=await page.locator('#brSpecBox .br-spec').count();
  assert.equal(specCount,18,'规格列表应列出 18 项');

  // --- 只需单个规格时：勾选一个具体规格，再统一「一次性加入」---
  // 2026-11-25 改成多选：点规格只切换勾选状态，弹窗不再自动关闭（用户要求多选）。
  await page.locator('#brSpecBox .br-spec').first().click();
  // 勾选后仍在选择器里，底部工具条应显示 1 项
  const barInfo=await page.locator('.br-pickbar .info').innerText();
  assert.match(barInfo,/已勾选 1 项/,'勾选后底部应显示 1 项：'+barInfo);
  await page.locator('.br-pickbar .acts button.btn.good').click();
  await page.locator('#brCondType').waitFor();
  let tags=await tagTexts(page);
  assert.ok(tags.includes('Spiker #2 001'),'点具体规格应加入完整商品名：'+tags.join('|'));

  // --- 再进一次，勾选整型号：写入的必须正好是型号名，而不是完整商品名 ---
  await page.getByRole('button',{name:'＋ 添加饵'}).first().click();
  await page.locator('.br-layer').first().waitFor();
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'拟饵'}).click();
  await page.locator('.br-layer button.br-opt').filter({hasText:'旋转亮片路亚'}).click();
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'型号'})}).locator('button.br-opt').filter({hasText:'Spiker #2'}).click();
  // 点「加入」按钮的勾选圆点（不是整个按钮——整按钮是「收窄范围」）
  await page.locator('button.br-opt.add').filter({hasText:'加入型号'}).click();
  await page.locator('.br-pickbar .acts button.btn.good').click();
  await page.locator('#brCondType').waitFor();
  tags=await tagTexts(page);
  assert.ok(tags.includes('Spiker #2'),'型号名应作为一个标签加进 bucket：'+tags.join('|'));
  const spikerOnly=tags.filter(t=>t==='Spiker #2');
  assert.equal(spikerOnly.length,1,'型号名不应重复加入：'+tags.join('|'));

  // --- 香煮成饵的形态层：下沉 / Pop-up 必须能分别选 ---
  await page.getByRole('button',{name:'＋ 添加饵'}).first().click();
  await page.locator('.br-layer').first().waitFor();
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'鱼饵'}).click();
  await page.locator('.br-layer button.br-opt').filter({hasText:'香煮成饵'}).click();
  ks=await layers();
  assert.ok(ks.includes('形态'),'香煮成饵应出现形态层：'+ks.join('|'));
  const shapeBtns=await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'形态'})}).locator('button.br-opt').allInnerTexts();
  assert.ok(shapeBtns.some(x=>x.includes('下沉')),'形态层缺下沉：'+shapeBtns.join('|'));
  assert.ok(shapeBtns.some(x=>x.includes('Pop-up')),'形态层缺 Pop-up：'+shapeBtns.join('|'));
  // 选 Pop-up 后，写进规则的必须是形态词而不是整类词
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'形态'})}).locator('button.br-opt').filter({hasText:'Pop-up'}).click();
  const shapeTerm=await page.locator('.br-preview code').innerText();
  assert.equal(shapeTerm,'Pop-up 香煮成饵','形态层写进规则的应是形态词');
  await page.locator('button.br-opt.add').filter({hasText:'加入整类'}).click();
  await page.locator('.br-pickbar .acts button.btn.good').click();
  await page.locator('#brCondType').waitFor();
  tags=await tagTexts(page);
  assert.ok(tags.includes('Pop-up 香煮成饵'),'应加入 Pop-up 形态词：'+tags.join('|'));

  // --- 保存：规则里带的是型号名与形态词 ---
  await page.getByRole('button',{name:'保存'}).click();
  await page.waitForFunction(()=>!document.getElementById('modalBg').classList.contains('on'));
  const allow=saved&&saved.allow||[];
  assert.ok(allow.includes('Spiker #2'),'保存的 allow 应含型号名 Spiker #2：'+allow.join('|'));
  assert.ok(allow.includes('Spiker #2 001'),'保存的 allow 应含单独加入的规格名');
  assert.ok(allow.includes('Pop-up 香煮成饵'),'保存的 allow 应含形态词');
  // 品类导航层（品牌名）绝不能进规则——它匹配不到任何饵。
  assert.ok(!allow.includes('zeiman'),'品牌名不该写进规则：'+allow.join('|'));

  assert.deepEqual(errors,[],'页面不应有 JS 报错：'+errors.join(' | '));
  await context.close();
 }finally{await browser.close();}
});