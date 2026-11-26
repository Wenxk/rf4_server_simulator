const {test}=require('node:test');
const assert=require('node:assert/strict');
const http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 玩法规则开关：三个开关各自独立保存、重载后保持、在 1280/1024/360 × 1/1.25/1.5 DPI 下
// 不溢出且可点击；写入失败不改界面、读取失败给提示。
//
// 迁移时的取舍（批次 6）：
//   这个测试原本把「点击开关」当成核心，因为旧后台每次点一下立刻发一次 POST。
//   SPA 改成标准表单：改动入草稿，统一点「保存修改」再逐键提交——这是刻意的
//   改动（一次误点不该直接把线上配置改掉）。所以断言从「点一下 = 一次写入」
//   改成「点一下 = 变脏 + 出现保存按钮；点保存 = 逐键写对」，业务含义更强。
//
//   布局部分全部保留并加强：旧测只量卡片内部，SPA 的开关是 label.sw + input
//   结构，量的是同样的事——文字与开关不重叠、控件在视口内、不撑宽页面。
//
//   两条产品规则原样保留：
//     · 读取默认值绝不能顺手写回服务端（否则第一次打开就把默认值固化了）；
//     · 写入失败时界面不能假装成功（值不能变、要能重试）。
//   这两条与用哪套 UI 无关，是数据安全。
const SWITCHES=[
 {key:'bite_gate_enabled',label:'饵要对口'},
 {key:'fish_ecology_enabled',label:'鱼种习性'},
 {key:'fish_weather_enabled',label:'天气与水温'},
 {key:'fish_distribution_enabled',label:'鱼群分布'},
 {key:'fish_bait_rules_enabled',label:'鱼会挑饵'},
 {key:'happy_hour_enabled',label:'欢乐时光'},
];

function serve(state){
 return http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const key=url.pathname.replace(/^\/admin/,'');
  const json=(v,c=200)=>{res.writeHead(c,{'Content-Type':'application/json'});res.end(JSON.stringify(v));};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/fish/bite_mode')return json({mode:'mixed'});
  if(key==='/api/tunables'){
   if(req.method==='GET')return state.failRead?json({error:'读取失败'},503):json({...state.values});
   let body='';req.on('data',c=>body+=c);
   req.on('end',()=>{
    if(state.failWrite)return json({error:'保存失败'},503);
    const v=JSON.parse(body);
    // 只允许改这六个开关里的一个，且值必须是 0/1——写错键会把别的配置改坏。
    assert.ok(SWITCHES.some(x=>x.key===v.key),`不该写这个键：${v.key}`);
    assert.ok(['0','1'].includes(v.value),`开关值只能是 0/1，实得 ${v.value}`);
    state.values[v.key]=v.value;state.writes.push({key:v.key,value:v.value});
    json({ok:true});
   });
   return;
  }
  if(key.startsWith('/api/'))return json({});
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
}

// 开关行是 label.row：.lab 里是文字，.ctrl 里是 label.sw>input。
const rowOf=(page,label)=>page.locator('#view label.row').filter({has:page.locator('.lab b', {hasText:label})});
const inputOf=(page,label)=>rowOf(page,label).locator('input[type=checkbox]');

test('fishing rule switches save independently, reload and fit supported sizes and DPI',async()=>{
 const state={values:{},writes:[],failRead:false,failWrite:false};
 const server=serve(state);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 let browser;
 const metrics=[],buttonMetrics=[];
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  for(const width of [1280,1024,360])for(const dpi of [1,1.25,1.5]){
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:dpi});
   await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
   const page=await context.newPage(),errors=[];
   page.on('pageerror',e=>errors.push(e.message));

   // 每轮从「全部未配置」开始，验证默认值不落库。
   state.values={};state.writes.length=0;
   await page.goto(`${base}/#/rules/switches`,{waitUntil:'networkidle'});
   await page.locator('#view label.row').first().waitFor();

   // ---- 六个开关都在，且默认值来自代码里的 def（不读服务端就不该写） ----
   for(const s of SWITCHES){
    assert.ok(await rowOf(page,s.label).count()===1,`缺开关「${s.label}」`);
   }
   assert.deepEqual(state.writes,[],'只读取默认值绝不能写服务端，否则第一次打开就把默认值固化');

   // ---- 改一个开关：只变脏，不立刻写；保存后只写这一个键 ----
   // 选「饵要对口」（默认 0），打开它。
   await inputOf(page,'饵要对口').check();
   // 状态提示在底部 savebar（不在行内）：草稿数 + 「保存修改」按钮变为可用。
   const saveBtn=page.locator('.savebar button', {hasText:'保存修改'});
   await saveBtn.waitFor({state:'visible'});
   await page.waitForFunction(()=>{
    const bar=document.querySelector('.savebar');
    return bar&&!/没有改动/.test(bar.textContent)&&/1/.test(bar.textContent);
   },null,{timeout:5000});
   assert.deepEqual(state.writes,[],'SPA 是表单式保存，勾选本身不该立刻写服务端');
   await saveBtn.click();
   await page.waitForFunction(()=>[...document.querySelectorAll('.toast')].length>0,null,{timeout:5000});
   await page.waitForFunction(()=>document.querySelectorAll('.savebar button')[1]?.disabled,null,{timeout:5000}).catch(()=>{});
   assert.equal(state.writes.length,1,`只改了一格，应只写一个键，实得 ${JSON.stringify(state.writes)}`);
   assert.deepEqual(state.writes[0],{key:'bite_gate_enabled',value:'1'});

   // ---- 重载后保持 ----
   await page.reload({waitUntil:'networkidle'});
   await page.locator('#view label.row').first().waitFor();
   assert.equal(await inputOf(page,'饵要对口').isChecked(),true,'重载后开关应保持刚保存的状态');
   assert.equal(await inputOf(page,'鱼种习性').isChecked(),false,'没动过的开关不该被连带改动');

   // ---- 再改一次：写 0，验证是「独立保存」而不是整表回写 ----
   state.writes.length=0;
   await inputOf(page,'饵要对口').uncheck();
   await page.locator('.savebar button', {hasText:'保存修改'}).click();
   await page.waitForFunction(()=>document.querySelectorAll('.toast').length>0,null,{timeout:5000});
   assert.deepEqual(state.writes,[{key:'bite_gate_enabled',value:'0'}],'关闭时也只写这一个键，不整表回写');

   // ---- 布局：三种文字长度下都不溢出、开关都在卡片内、都点得到 ----
   // 先等页面彻底静下来：上一步保存成功会触发 render()，SPA 用
   // document.startViewTransition 做过渡，过渡期间整页在快照里，
   // elementFromPoint 会返回 <html>（这里踩过：命中栈只有 HTML.）。
   await page.waitForFunction(()=>!document.querySelector('.is-busy'),null,{timeout:8000}).catch(()=>{});
   // 还要等 toast 自己消失：它固定在右下角 28px 处，会盖住靠下的开关，
   // 命中测试会把它判成「被挡住」。这是提示条的正常行为（几秒后自动走），
   // 不是缺陷——所以等它走完，而不是放宽断言。
   await page.waitForFunction(()=>!document.querySelector('.toasts .toast'),null,{timeout:8000}).catch(()=>{});
   await page.waitForTimeout(200);
   for(const [name,text] of [['normal',null],['chinese','尚未识别的最长合理业务饵料名称与前置条件'.repeat(12)],['continuous','UnrecognizedBait'.repeat(30)]]){
    if(text){
     // 把说明文字换成超长串，看布局是否撑破。改的是 DOM 文本，不影响断言对象。
     await page.locator('#view label.row .lab small').first().evaluate((e,t)=>{e.textContent=t;},text);
    }
    const m=await page.evaluate(()=>{
     const rect=e=>{const b=e.getBoundingClientRect();return{x:b.x,y:b.y,right:b.right,bottom:b.bottom,width:b.width,height:b.height};};
     const rows=[...document.querySelectorAll('#view label.row')];
     return {viewport:innerWidth,document:document.documentElement.scrollWidth,
      work:rect(document.querySelector('#view .work')),
      rows:rows.map(e=>{
       const range=document.createRange();range.selectNodeContents(e.querySelector('.lab'));
       const sw=e.querySelector('input[type=checkbox]');
       return {box:rect(e),lab:rect(e.querySelector('.lab')),ctrl:rect(e.querySelector('.ctrl')),
        sw:rect(sw),font:getComputedStyle(sw).fontSize,
        textRects:[...range.getClientRects()].map(r=>({x:r.x,right:r.right,y:r.y,bottom:r.bottom}))};
      })};
    });
    assert.ok(m.document<=width+2,`页面横向溢出 ${JSON.stringify({width,dpi,name,document:m.document,viewport:m.viewport})}`);
    for(const [i,r] of m.rows.entries()){
     // 文字区与开关区不能重叠。注意窄屏（≤760px）下 .row 会改成上下两行堆叠，
     // 这时「lab.right <= ctrl.x」不成立也不该成立——要判的是两个矩形不相交，
     // 而不是「右边在左边左边」。旧测试只跑了宽屏，所以没遇到这一条。
     const overlaps=r.lab.right>r.ctrl.x+1&&r.lab.bottom>r.ctrl.y+1&&r.lab.x<r.ctrl.right-1&&r.lab.y<r.ctrl.bottom-1;
     assert.ok(!overlaps,`第 ${i+1} 行文字与开关重叠 ${JSON.stringify({width,dpi,name,lab:r.lab,ctrl:r.ctrl})}`);
     assert.ok(r.box.x>=m.work.x-1&&r.box.right<=m.work.right+1,`第 ${i+1} 行跑出了内容区`);
     assert.ok(r.sw.width>=16&&r.sw.height>=16,`第 ${i+1} 行的开关太小，点不准：${r.sw.width}×${r.sw.height}`);
     // 文字必须落在自己的盒子里（长串要断行，不能画到盒子外面）。
     for(const t of r.textRects){
      assert.ok(t.x>=r.lab.x-1&&t.right<=r.lab.right+1&&t.y>=r.lab.y-1&&t.bottom<=r.lab.bottom+1,
       `第 ${i+1} 行文字画出界 ${JSON.stringify({width,dpi,name,t,lab:r.lab})}`);
     }
    }
    // 每个开关都要真的能点到（没被别的元素盖住、没被挤出视口）。
    for(const s of SWITCHES){
     const box=inputOf(page,s.label);
     await box.scrollIntoViewIfNeeded();
     // 滚完再量，并且重算位置：scrollIntoView 会改变元素在视口里的坐标，
     // 拿滚动前的 rect 做命中测试，点到的可能是滚动后的另一个元素（这里踩过）。
     // 灌了超长说明文字之后行高会变，这一步尤其不能省。
     await page.waitForTimeout(80);
     // 开关是「真 checkbox 垫在 label.sw 里」：命中测试要按 label 算，
     // 直接测 input 的 rect 在某些层级下会落在 label 外。
     const b=await box.evaluate(e=>{
      const sw=e.closest('label.sw')||e;
      const r=sw.getBoundingClientRect();
      const cx=r.x+r.width/2, cy=r.y+r.height/2;
      const hit=document.elementFromPoint(cx,cy);
      return {y:r.y,bottom:r.bottom,viewport:innerHeight,width:r.width,height:r.height,
       // 括号不能省：`a || b?.c || d` 里 ?. 的优先级低于 ||，会把右半边吞掉，
       // hit 明明落在 sw 内也会被判成不可点（这里也踩过）。
       reachable:!!hit&&(sw.contains(hit)||hit===e||!!hit.closest('.sw'))};
     });
     assert.ok(b.y>=-1&&b.bottom<=b.viewport+1,`开关「${s.label}」被挤出视口 ${JSON.stringify({width,dpi,name,b})}`);
     assert.ok(b.reachable,`开关「${s.label}」被别的元素挡住了 ${JSON.stringify({width,dpi,name,b})}`);
     buttonMetrics.push({label:s.label,width:b.width,height:b.height,w:width});
    }
    metrics.push({width,dpi,name,rows:m.rows.length});
   }

   // ---- 写入失败：界面不能假装成功 ----
   state.failWrite=true;
   state.writes.length=0;
   const before={...state.values};
   await inputOf(page,'鱼种习性').check();
   await page.locator('.savebar button', {hasText:'保存修改'}).click();
   await page.waitForFunction(()=>[...document.querySelectorAll('.toast')].some(t=>/失败|错误|HTTP/.test(t.textContent)),null,{timeout:6000});
   state.failWrite=false;
   assert.deepEqual(state.values,before,'写入失败时服务端值不能变（这条由 fixture 保证）');
   // 关键：失败后草稿要还在，用户不用重新勾一遍。
   assert.equal(await inputOf(page,'鱼种习性').isChecked(),true,'保存失败后界面不能悄悄回滚勾选状态');

   // ---- 读取失败：给出可读提示而不是白屏 ----
   state.failRead=true;
   await page.reload({waitUntil:'networkidle'});
   await page.waitForFunction(()=>document.querySelector('#view')?.innerText.includes('读取失败'),null,{timeout:8000});
   const failText=await page.locator('#view').innerText();
   assert.match(failText,/页面加载失败/,'读取失败应给出统一兜底页');
   assert.match(failText,/读取失败/,'应把服务端给的原因带出来');
   state.failRead=false;

   assert.deepEqual(errors,[],'页面不应有 JS 错误：'+errors.join(' | '));
   await context.close();
  }
  console.log(JSON.stringify({renderedCases:metrics.length,actionChecks:buttonMetrics.length,
   switches:SWITCHES.map(s=>({key:s.key,minW:Math.min(...buttonMetrics.filter(b=>b.label===s.label).map(b=>b.width)),minH:Math.min(...buttonMetrics.filter(b=>b.label===s.label).map(b=>b.height))}))}));
 }finally{if(browser)await browser.close();await new Promise(r=>server.close(r));}
});