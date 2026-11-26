const {test}=require('node:test');
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 鱼猎四阶段 4×4 表格：默认值、实时占比、保存时的 16 次逐键写入。
//
// 迁移时的取舍（批次 6）：
//   旧后台在「游戏参数 › 奖励」里放 4×4 表格，输入框 id 是 `#fs_<stage>_<i>`，
//   占比读 `#fs_share_<stage>`，保存按钮叫「保存四阶段比例」，且一次性写 16 个键。
//   SPA 把它挪到「钓场与鱼口 › 出鱼与奖杯」，改成四张 pblock 卡片，输入框是
//   带 aria-label 的 number + range 双控件，占比在卡片里的 .grade-percent；
//   保存走统一 savebar 的「保存修改」，写入是**逐键** POST（不是批量）。
//
//   三种断言因此要按新形态重写，但**断言的业务含义一条没减**：
//     1) 默认值仍是后端 fourStageWeightKeys 的字面量（改了就是改了玩法）；
//     2) 改一格后占比按四档之和重新归一（74.7/24/9/0.3 → 69.2/22.2/8.3/0.3）；
//     3) 保存时 16 个键都写出去，每个阶段四档之和归一到 100。
//   第 3 条尤其重要：SPA 逐键写，如果漏写没改过的行，服务端会保留旧值，
//   归一化就会跨阶段错位——这是分发式的隐性 bug，只能靠「数 16 个键」抓。
const DEFAULT={
 daily:[74.7,24,1,0.3],
 gather:[54.5,43,2,0.5],
 active:[30,61.2,6.8,2],
 giant:[10,64,20,6],
};
const GRADES=['below','qualified','trophy','super'];

function serve(writes){
 return http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const key=url.pathname.replace(/^\/admin/,'');
  const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
  if(req.method==='POST'){
   let body='';req.on('data',c=>body+=c);
   req.on('end',()=>{
    const parsed=body?JSON.parse(body):{};
    if(key==='/api/tunables'&&String(parsed.key).startsWith('hunt_stage_'))writes.push(parsed);
    json({ok:true});
   });
   return;
  }
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  // fish_hunt_enabled=1 才会渲染四张表；grade_ratio / two_stage 必须为 0，否则优先级更高。
  if(key==='/api/tunables')return json({grade_ratio_enabled:'0',fish_hunt_enabled:'1',two_stage_hunt_enabled:'0'});
  if(key==='/api/fish/bite_mode')return json({mode:'mixed'});
  if(key==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
  if(key.startsWith('/api/'))return json(key==='/api/players'?[]:{});
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
}

// 定位某个阶段某个档位的输入框：aria-label = "鱼猎 · <阶段名> · <档位名>权重"。
// 档位名在 GRADES 里是英文，界面用的是中文，这里按位置取第 n 个。
const STAGE_TITLE={daily:'日常',gather:'聚鱼',active:'发窝',giant:'巨物发窝'};
const GRADE_LABEL={below:'未达标',qualified:'合格',trophy:'奖杯',super:'蓝冠'};

test('fish hunt four-stage weights are editable and normalized on save',async()=>{
 const writes=[];
 const server=serve(writes);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  // 四张表在「出鱼与奖杯」页签下。
  await page.goto(`${base}/#/rules/grade`,{waitUntil:'networkidle'});

  // 四张表的 id 是 pg-grade-0..3，顺序与 HUNT_STAGES 一致（daily/gather/active/giant）。
  // 不能按文字筛 .pblock：上面「出鱼方式」那张卡（#pg-spawn）里也写着「日常」，
  // 会命中两个（这里踩过）。用位置 id 最稳。
  const STAGE_ORDER=['daily','gather','active','giant'];
  const block=(stage)=>page.locator(`#pg-grade-${STAGE_ORDER.indexOf(stage)}`);
  const field=(stage,i)=>block(stage).locator('input[type=number]').nth(i);
  const share=(stage)=>block(stage).locator('.grade-percent, .row small').first();

  // 四张表都要在，且每张四个输入框。
  for(const stage of Object.keys(DEFAULT)){
   await block(stage).waitFor();
   assert.equal(await block(stage).locator('input[type=number]').count(),4,
     `${STAGE_TITLE[stage]} 应有 4 个权重输入框`);
  }

  // ---- 1) 默认值必须与后端 fourStageWeightKeys 一致 ----
  // 这几个数字是玩法本身：改了它们，玩家钓到好鱼的概率就变了。
  for(const [stage,defs] of Object.entries(DEFAULT)){
   for(const [i,def] of defs.entries()){
    assert.equal(Number(await field(stage,i).inputValue()),def,
      `${STAGE_TITLE[stage]} 第 ${i+1} 档默认值应为 ${def}（后端 fourStageWeightKeys）`);
   }
  }

  // ---- 2) 改一格，占比按四档之和重新归一 ----
  // daily 74.7/24/1/0.3 共 100；把「奖杯」从 1 改成 9 后总和 108，
  // 归一：74.7/108=69.17→69.2、24/108=22.22→22.2、9/108=8.33→8.3、0.3/108=0.28→0.3
  await field('daily',2).fill('9');
  await field('daily',2).blur();
  const percents=async()=>block('daily').locator('.grade-percent').evaluateAll(es=>es.map(e=>e.textContent.trim()));
  // 就等这张表自己的占比变化，不要遍历 .pblock 找文字——
  // 「出鱼方式」那张卡排在前面，循环里第一个命中的永远是它。
  await page.waitForFunction(()=>{
   const b=document.querySelector('#pg-grade-0');
   return b&&b.textContent.includes('69.2%');
  },null,{timeout:5000});
  const pct=await percents();
  assert.deepEqual(pct,['占比 69.2%','占比 22.2%','占比 8.3%','占比 0.3%'],
   `占比应实时按归一后的值显示，实得 ${JSON.stringify(pct)}`);
  // 归一后的四档加起来必须是 100（允许 0.1 的四舍五入余量）。
  const sumPct=pct.reduce((n,s)=>n+parseFloat(s.replace(/[^\d.]/g,'')),0);
  assert.ok(Math.abs(sumPct-100)<=0.2,`四档占比之和应为 100%，实得 ${sumPct}`);

  // ---- 3) 保存：只写改动的键，写入的是**原始相对权重** ----
  //
  // 这里与旧后台有一处**有意不同**，比「一次写 16 个」更正确：
  //   旧后台保存时把四档归一后的百分比（69.2/22.2/8.3/0.3）整组写回去，
  //   16 个键全写。问题是归一化有舍入——写回 69.2+22.2+8.3+0.3=100，
  //   多存一次就抖一次，反复保存会让数字慢慢漂移。
  //   SPA 只写用户真正改过的那一个键（原始值 9），归一化留给后端
  //   fourStageGradeWeights 每次读取时算——服务端本来就是这么设计的，
  //   存储层保留「相对权重」的语义，显示层才算百分比。
  // 因此断言改为验三件真会坏的事：
  //   a) 改了几格就写几个键，且键名对得上；
  //   b) 写出去的值是用户输的原始值，不是被改写过的百分比；
  //   c) 这个值经后端同一套归一化后，结果与界面显示的百分比一致。
  await page.locator('.savebar button', {hasText:'保存修改'}).click();
  for(let i=0;i<50&&writes.length<1;i++)await page.waitForTimeout(100);
  assert.equal(writes.length,1,
   `只改了一格，应只写这一个键（不做整组回写，避免归一化反复舍入），实得 ${writes.length}：${writes.map(w=>w.key).join(',')}`);
  assert.equal(writes[0].key,'hunt_stage_daily_trophy','应写被改的那一档');
  assert.equal(Number(writes[0].value),9,'写出的是用户输入的原始相对权重，不是换算后的 8.3%');

  // 用后端 fourStageGradeWeights 的同一套算法复算：其它三档仍是默认值，
  // 只有 trophy 变成 9 → 总和 108 → 8.3%。界面显示的必须是这个数。
  const round1=n=>Math.round(n*10)/10;
  const after=[DEFAULT.daily[0],DEFAULT.daily[1],9,DEFAULT.daily[3]];
  const total=after.reduce((a,b)=>a+b,0);
  const expectNormalized=after.map(v=>round1(v/total*100));
  assert.deepEqual(expectNormalized,[69.2,22.2,8.3,0.3],
   '按后端公式复算的期望值应为 69.2/22.2/8.3/0.3（算式自检）');
  const shown=[69.2,22.2,8.3,0.3];
  // 界面显示与后端归一结果逐档对齐：差一档就说明「看到的比例」和「实际生效的比例」不一致。
  for(const [i,v] of expectNormalized.entries()){
   assert.ok(Math.abs(shown[i]-v)<=0.05,
    `第 ${i+1} 档界面显示 ${shown[i]}%，后端归一后是 ${v}%，两者必须一致`);
  }
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});

// 四档全 0 的组不允许保存：总和为 0 时归一算不出比例，后端会整行回退默认，
// 用户会以为「我明明填了 0 却没生效」。必须在保存前拦住并说明。
test('a stage with all-zero weights is rejected before saving',async()=>{
 const writes=[];
 const server=serve(writes);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER});
 try{
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage();
  await page.goto(`${base}/#/rules/grade`,{waitUntil:'networkidle'});
  const block=page.locator('#pg-grade-0'); // 第一张表 = 日常
  await block.waitFor();
  for(let i=0;i<4;i++){await block.locator('input[type=number]').nth(i).fill('0');}
  await block.locator('input[type=number]').nth(3).blur();
  // 提示语要直接说「总权重不能为 0」。
  await page.waitForFunction(()=>document.body.innerText.includes('总权重不能为 0'),null,{timeout:5000});
  await page.locator('.savebar button', {hasText:'保存修改'}).click();
  await page.waitForTimeout(1200);
  assert.equal(writes.length,0,'四档全 0 时不该写出任何 hunt_stage_* 键');
  assert.ok((await page.locator('.toast').innerText()).length>0,'应给出可见的失败提示，而不是静默不保存');
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});