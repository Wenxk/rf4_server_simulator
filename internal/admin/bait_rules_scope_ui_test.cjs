const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const html=fs.readFileSync(path.join(__dirname,'web/index.html'),'utf8');

// 吃饵规则编辑器的三项改造（用户 2026-11-25 反馈）：
//   1) 同名型号跨类别加不进去 —— `鱼子` 同时存在于多个类别，前端按名字去重就判重复；
//      而且即使写进去，一个 `鱼子` 会命中全部同名款，表达不了「只要香煮成饵的鱼子」；
//   2) 编辑框太小，全缩在一起 —— 弹窗从 760px 提到 1180px；
//   3) 编辑时要能多选 —— 点一下就关窗的老交互改成「勾选暂存 + 一次性加入」。
//
// 夹具刻意做成真实形状：`鱼子` 同时出现在 香煮成饵(8) 与 颗粒(9)，
// 且后端给了 scope_term（`香煮成饵@鱼子` / `颗粒@鱼子`）与 shared_with。
const CATEGORIES={
  categories:[
    {key:27,label:'鱼饵',count:7,term:'鱼饵',children:[
      {sub:8,label:'香煮成饵（下沉 + Pop-up）',count:4,term:'香煮成饵',
       brands:[{kind:'craft_baits',label:'craft_baits',count:4,lines:[
         {base:'鱼子',count:4,term:'鱼子',scope_term:'香煮成饵@鱼子',shared_with:['颗粒']}
       ]}]},
      {sub:9,label:'颗粒',count:3,term:'颗粒',
       brands:[{kind:'craft_baits',label:'craft_baits',count:3,lines:[
         {base:'鱼子',count:3,term:'鱼子',scope_term:'颗粒@鱼子',shared_with:['香煮成饵']}
       ]}]}
    ]},
    {key:17,label:'拟饵',count:0,term:'拟饵',children:[]}
  ],
  terms:[
    {term:'各类饵',label:'全部（鱼饵 + 拟饵）',cat:0,sub:0,count:7,alias:false,shape:''},
    {term:'鱼饵',label:'鱼饵（全部天然饵）',cat:27,sub:0,count:7,alias:false,shape:''},
    {term:'拟饵',label:'拟饵（全部路亚）',cat:17,sub:0,count:0,alias:false,shape:''},
    {term:'香煮成饵',label:'香煮成饵',cat:27,sub:8,count:4,alias:false,shape:''},
    {term:'颗粒',label:'颗粒',cat:27,sub:9,count:3,alias:false,shape:''}
  ]
};

const SPECS={
  '香煮成饵@鱼子':[
    {item_id:'boils_cb_ca10',name_zh:'鱼子10',category:27,sub:8,sub_label:'香煮成饵',brand:'boils_cb_ca',kind:'craft_baits',base:'鱼子'},
    {item_id:'boils_cb_ca16',name_zh:'鱼子16',category:27,sub:8,sub_label:'香煮成饵',brand:'boils_cb_ca',kind:'craft_baits',base:'鱼子'}
  ],
  '颗粒@鱼子':[
    {item_id:'plt_cb_cvr14',name_zh:'鱼子 14',category:27,sub:9,sub_label:'颗粒',brand:'plt_cb',kind:'craft_baits',base:'鱼子'},
    {item_id:'plt_cb_cvr18',name_zh:'鱼子 18',category:27,sub:9,sub_label:'颗粒',brand:'plt_cb',kind:'craft_baits',base:'鱼子'}
  ]
};
function rowsFor(url){
  const q=url.searchParams;
  const sub=Number(q.get('sub')||0);
  const pool=sub===8?SPECS['香煮成饵@鱼子']:(sub===9?SPECS['颗粒@鱼子']:[]);
  return {rows:pool,matched:pool.length,total:7};
}

// 标签纯文本（剥掉「×」删除按钮）
async function tagTexts(page){
  // 取标签的可见文本并剥掉尾部「×」删除按钮。
  // 不能用 childNodes[0].nodeValue：类别限定标签 `颗粒@鱼子` 的第一个子节点是
  // <span class="sc">（元素节点，nodeValue 为 null），会取到空串。
  return await page.locator('#modal .tag').evaluateAll(
    els=>els.map(e=>e.textContent.replace(/\s*×\s*$/,'').trim())
  );
}

test('bait rule editor: wider modal, multi-select, and same-name-model disambiguation',async()=>{
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1500,height:1000}});
  await context.addInitScript(()=>localStorage.setItem('rf4_admin_token','fixture'));
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  let saved=null;
  await page.route('**/*',route=>{
   const url=new URL(route.request().url());
   if(url.pathname==='/')return route.fulfill({contentType:'text/html; charset=utf-8',body:html});
   if(url.pathname==='/api/me')return route.fulfill({contentType:'application/json',body:JSON.stringify({username:'admin'})});
   if(url.pathname==='/api/bait/prefs')return route.fulfill({contentType:'application/json',body:JSON.stringify({rules_enabled:true,rule_count:1,rows:[{
     fish_key:'alb_barbel',name_zh:'白化鲃鱼',has_rule:true,source:'override',enabled:true,
     allow:[],deny:[],bottom_only:[],condition:''
   }]})});
   if(url.pathname==='/api/bait/categories')return route.fulfill({contentType:'application/json',body:JSON.stringify(CATEGORIES)});
   if(url.pathname==='/api/bait/items')return route.fulfill({contentType:'application/json',body:JSON.stringify(rowsFor(url))});
   if(url.pathname==='/api/bait/rules'&&route.request().method()==='PUT'){
     saved=JSON.parse(route.request().postData()||'{}');
     return route.fulfill({contentType:'application/json',body:JSON.stringify({ok:true})});
   }
   return route.fulfill({contentType:'application/json',body:'{}'});
  });
  await page.goto('http://127.0.0.1:19216/#ecology',{waitUntil:'networkidle'});
  await page.locator('#ecoTabs button[data-etab="bait"]').click();
  await page.locator('#baitTable .bait-edit').first().click();
  await page.locator('#brCondType').waitFor();

  // ===== 需求 2：编辑弹窗要放大 =====
  // 编辑器本身（三项标签 + 前置条件 + 启用开关）就用了宽弹窗，
  // 因为 760px 里三行标签会折成一坨，用户反馈「全部缩在一起看太挤」。
  const editorW=await page.locator('#modal').evaluate(e=>e.getBoundingClientRect().width);
  assert.ok(editorW>=1100,`编辑器弹窗应加宽到 ≥1100px，实际 ${Math.round(editorW)}px`);

  await page.getByRole('button',{name:'＋ 添加饵'}).first().click();
  await page.locator('.br-layer').first().waitFor();
  const pickerW=await page.locator('#modal').evaluate(e=>e.getBoundingClientRect().width);
  assert.ok(pickerW>=1100,`选择器弹窗也应加宽，实际 ${Math.round(pickerW)}px`);

  // ===== 需求 1：同名型号跨类别必须能区分 =====
  await page.locator('.br-layer').first().locator('button.br-opt').filter({hasText:'鱼饵'}).click();
  // 选「颗粒」类别 → 型号层里的鱼子写进规则应是 `颗粒@鱼子`
  await page.locator('.br-layer button.br-opt').filter({hasText:'颗粒'}).first().click();
  let codeTerm=await page.locator('.br-preview code').first().innerText();
  // 未选型号时写的是整类词「颗粒」
  assert.equal(codeTerm,'颗粒','未选型号时应写整类词');

  // 选型号「鱼子」：预览必须变成限定写法，并明确说明同名还在别的类别
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'型号'})})
    .locator('button.br-opt').filter({hasText:'鱼子'}).first().click();
  const preview=await page.locator('.br-preview').innerText();
  assert.match(preview,/颗粒@鱼子/,'预览应显示类别限定写法：'+preview);
  codeTerm=await page.locator('.br-preview code').first().innerText();
  assert.equal(codeTerm,'颗粒@鱼子','跨类别同名型号写进规则的必须是限定写法');
  // 必须明确提示「同名还出现在香煮成饵」，否则用户不知道自己是不是点错了
  assert.match(preview,/同名型号还出现在/,'应提示同名型号还在其它类别：'+preview);
  assert.match(preview,/香煮成饵/,'提示里应点名另一个类别：'+preview);
  // 型号按钮上要有同名徽标（同名×N）。
  // 不按 `.br-layer` + `.k` 过滤：二级类型那一层的 `.k` 是空串，
  // hasText 过滤会同时命中/漏掉它，导致定位不稳。直接找 .badge。
  const badges=await page.locator('#modal .badge').allInnerTexts();
  if(!badges.some(x=>x.includes('同名'))){
    console.log('DEBUG layers:', (await page.locator('.br-layer .k').allInnerTexts()).join('|'));
    console.log('DEBUG model html:', (await page.locator('#modal').innerHTML()).slice(0,3000));
  }
  assert.ok(badges.some(x=>x.includes('同名')),'同名型号按钮应有徽标：'+badges.join('|'));

  // ===== 需求 3：多选 —— 勾选不关窗，可跨层累积，最后一次性加入 =====
  // 勾 1：颗粒@鱼子 这个型号
  await page.locator('button.br-opt.add').filter({hasText:'加入型号'}).click();
  // 勾完仍在选择器里（弹窗不关），底部计数为 1
  assert.ok(await page.locator('.br-pickbar').count()===1,'勾选后不应关闭选择器');
  let info=await page.locator('.br-pickbar .info').innerText();
  assert.match(info,/已勾选 1 项/,'底部应显示已勾选 1 项：'+info);

  // 勾 2：切到香煮成饵，勾它自己的「鱼子」（同名但不同类别，必须允许共存）
  await page.locator('.br-layer button.br-opt').filter({hasText:'香煮成饵'}).first().click();
  const codeTerm2=await page.locator('.br-preview code').first().innerText();
  assert.equal(codeTerm2,'香煮成饵','切到香煮成饵后未选型号，写整类词');
  await page.locator('.br-layer').filter({has:page.locator('.k',{hasText:'型号'})})
    .locator('button.br-opt').filter({hasText:'鱼子'}).first().click();
  const codeTerm3=await page.locator('.br-preview code').first().innerText();
  assert.equal(codeTerm3,'香煮成饵@鱼子','香煮成饵的鱼子应写成限定写法');
  await page.locator('button.br-opt.add').filter({hasText:'加入型号'}).click();
  info=await page.locator('.br-pickbar .info').innerText();
  assert.match(info,/已勾选 2 项/,'跨层级勾选应累积到 2 项：'+info);
  // 两个限定词不同，必须允许同时存在于待选区
  const chips=await page.locator('.br-pickbar .chips .tag').evaluateAll(
    els=>els.map(e=>((e.childNodes[0]&&e.childNodes[0].nodeValue)||'').trim()));
  assert.ok(chips.includes('颗粒@鱼子')||chips.includes('颗粒 › 鱼子'),'待选区应含颗粒限定词：'+chips.join('|'));

  // 勾 3：再把整类词「颗粒」也勾上（不同类型/层级混合多选）
  await page.locator('.br-layer button.br-opt').filter({hasText:'颗粒'}).first().click();
  await page.locator('button.br-opt.add').filter({hasText:'加入整类'}).click();
  info=await page.locator('.br-pickbar .info').innerText();
  assert.match(info,/已勾选 3 项/,'应累积到 3 项：'+info);

  // 一次性加入
  await page.locator('.br-pickbar .acts button.btn.good').click();
  await page.locator('#brCondType').waitFor();
  const tags=await tagTexts(page);
  // 关键断言：两个同名「鱼子」都能进去，靠限定词区分（这就是用户报的「加不了」）
  const joined=tags.join('|');
  // 关键断言：两个同名「鱼子」都能进去，靠限定词区分（这就是用户报的「加不了」）。
  // 展示层把 `颗粒@鱼子` 拆成「颗粒 › 鱼子」，所以标签文本里是 › 不是 @；
  // 落库值仍是 `颗粒@鱼子`，由下面的保存断言单独守。
  assert.ok(tags.some(t=>t==='颗粒›鱼子'||t==='颗粒@鱼子'),'应加入颗粒的鱼子：'+joined);
  assert.ok(tags.some(t=>t==='香煮成饵›鱼子'||t==='香煮成饵@鱼子'),'应加入香煮成饵的鱼子：'+joined);
  assert.ok(tags.includes('颗粒'),'应加入整类词 颗粒：'+joined);
  assert.equal(tags.length,3,'应恰好 3 个标签：'+joined);

  // 限定标签的显示必须拆成「限定词 › 名字」，否则两个同名标签看起来一模一样
  const scopeChips=await page.locator('#modal .br-scope-chip').count();
  assert.equal(scopeChips,2,'两个限定标签都应带 br-scope-chip 样式');
  const scopeText=await page.locator('#modal .br-scope-chip').first().innerText();
  assert.match(scopeText,/›/,'限定标签应显示「限定词 › 名字」：'+scopeText);

  // ===== 保存：限定词原样落库 =====
  await page.getByRole('button',{name:'保存'}).click();
  await page.waitForFunction(()=>!document.getElementById('modalBg').classList.contains('on'));
  const allow=(saved&&saved.allow)||[];
  assert.ok(allow.includes('颗粒@鱼子'),'保存的 allow 应含 颗粒@鱼子：'+allow.join('|'));
  assert.ok(allow.includes('香煮成饵@鱼子'),'保存的 allow 应含 香煮成饵@鱼子：'+allow.join('|'));
  assert.ok(allow.includes('颗粒'),'保存的 allow 应含整类词：'+allow.join('|'));

  assert.deepEqual(errors,[],'页面不应有 JS 报错：'+errors.join(' | '));
  await context.close();
 }finally{await browser.close();}
});