const {test}=require('node:test');
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 玩家 → 技能：三档宽度 × 三档 DPI × 三种技能名长度，页面不横向溢出，
// 滑杆/百分比/保存按钮都在卡片内。
//
// 迁移时的取舍（批次 6）：
//   旧后台每张技能卡是「数字输入框 + 满分提示 + 进度条 + 分数药丸」，测试直接
//   灌 PLAYER 全局变量再调 renderPlayerDetail()。SPA 没有这两样东西：
//     · 控件改成 range 滑杆（0–100，步长 0.1）+ output 百分比；
//       「输入必须 ≤100」由滑杆的 min/max 天然保证，还多出一层「不能填 101」的
//       校验断言，但那是旧控件的产物，挪到滑杆上就没意义了；
//     · 页面靠 #/players/<uid>/skills 路由 + /api/players/detail 拿数据，
//       没有可注入的全局变量——测试只能从接口喂数据，反而更接近真实路径。
//   所以断言重心从「输入框尺寸」转到「长技能名不撑破卡片 + 保存真的发对了请求」。
//   后者是旧测试没有的：它能抓到「滑杆动了但发出去的 skill_id 是别人的」。
const TABS=['档案','背包与装备','鱼护','鱼种记录','技能','邮箱'];

const SKILLS=[
  {skill_id:11,name:'海钓',points:100,node_count:4},
  {skill_id:1,name:'浮子钓鱼',points:50,node_count:12},
  {skill_id:2,name:'纺车钓鱼',points:0,node_count:8},
];

function detail(names){
  return {uid:1,name:'技能测试',level:50,xp:0,item_count:0,keepnet_count:0,mail_count:0,
    available_points:35,skill_total:113,skill_spent:78,skill_bonus:0,
    skills:SKILLS.map((s,i)=>({...s,name:names[i]}))};
}

function serve(state){
  return http.createServer((req,res)=>{
    const url=new URL(req.url,'http://fixture');
    const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
    const json=v=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(v));};
    if(req.method==='POST'){
      let body='';req.on('data',c=>body+=c);
      req.on('end',()=>{
        state.posts.push({path:pathname,body:body?JSON.parse(body):null});
        json({ok:true,available_points:35});
      });
      return;
    }
    if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
    if(pathname==='/api/players')return json([{uid:1,name:'技能测试',level:50,online:true}]);
    if(pathname==='/api/players/detail')return json(detail(state.names));
    if(pathname==='/api/dashboard')return json({sessions:1,players:1,items:1,mails:0,tunables:1,audits:0});
    if(pathname.startsWith('/api/'))return json({});
    if(sendAsset(res,url.pathname))return;
    res.writeHead(404);res.end('');
  });
}

test('skills tab fits long skill names at every width and DPI',async()=>{
 const state={names:['海钓','浮子钓鱼','纺车钓鱼'],posts:[]};
 const server=serve(state);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 const measurements=[];
 try{
  for(const width of [1280,1024,360])for(const dpi of [1,1.25,1.5]){
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:dpi});
   await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
   const page=await context.newPage(),errors=[];
   page.on('pageerror',e=>errors.push(e.message));
   for(const [scenario,names] of [
     ['normal',['海钓','浮子钓鱼','纺车钓鱼']],
     ['chinese',['最长合理技能名称与说明'.repeat(12),'浮子钓鱼','纺车钓鱼']],
     ['continuous',['UnbrokenSkillName'.repeat(25),'浮子钓鱼','纺车钓鱼']],
   ]){
    state.names=names;
    await page.goto(`${base}/#/players/1/skills`,{waitUntil:'networkidle'});
    // 一定要 reload：只改 hash 不会重新请求 detail，拿到的还是上一轮的名字。
    if(scenario!=='normal')await page.reload({waitUntil:'networkidle'});
    const pane=page.locator('#view');
    await pane.getByText('未分配的技能点').waitFor();

    const r=await page.evaluate(()=>{
     const rect=e=>{const b=e.getBoundingClientRect();return{x:b.x,right:b.right,width:b.width,height:b.height,y:b.y,bottom:b.bottom};};
     const cards=[...document.querySelectorAll('#view .skill-card')];
     return {viewport:innerWidth,scroll:document.documentElement.scrollWidth,
      cards:cards.map(card=>{
       const range=card.querySelector('input[type=range]');
       const out=card.querySelector('output');
       const save=card.querySelector('button');
       const name=card.querySelector('.skill-name');
       return {card:rect(card),name:rect(name),range:rect(range),out:rect(out),save:rect(save),
        max:range.max,min:range.min,step:range.step,
        nameWrap:getComputedStyle(name).overflowWrap,
        nameOverflow:name.scrollWidth>name.clientWidth+2};
      })};
    });

    assert.ok(r.scroll<=width+1,`页面横向溢出 ${JSON.stringify({width,dpi,scenario,scroll:r.scroll,viewport:r.viewport})}`);
    assert.equal(r.cards.length,3,`技能卡应有 3 张，实得 ${r.cards.length}`);
    for(const [i,card] of r.cards.entries()){
     // 滑杆口径：0–100、步长 0.1。这三个值改了就是改了含义（熟练度百分比）。
     assert.equal(card.max,'100');
     assert.equal(card.min,'0');
     assert.equal(card.step,'0.1');
     // 卡片自己不能变成横向滚动容器，否则长名字会被裁掉一半看不见。
     assert.ok(card.name.right<=card.card.right+1,
       `第 ${i} 张卡的技能名跑出卡片 ${JSON.stringify({width,dpi,scenario,card})}`);
     for(const part of [card.range,card.out]){
      assert.ok(part.x>=card.card.x-1&&part.right<=card.card.right+1,
        `第 ${i} 张卡里的控件越界 ${JSON.stringify({width,dpi,scenario,part,card:card.card})}`);
     }
     assert.ok(card.range.width>=45,`滑杆被挤得太窄，拖不动：${card.range.width}px`);
     assert.ok(card.out.width>=30,`百分比读数被挤得太窄：${card.out.width}px`);
     // 长名字必须允许断行（overflow-wrap），否则它只能靠溢出卡片来显示。
     if(scenario!=='normal')assert.equal(card.nameWrap,'anywhere',`长技能名需要 overflow-wrap:anywhere，第 ${i} 张是 ${card.nameWrap}`);
     assert.equal(card.nameOverflow,false,`技能名溢出了自己的盒子：第 ${i} 张`);
    }
    assert.deepEqual(errors,[],'页面不应有 JS 错误：'+errors.join(' | '));
    measurements.push({width,dpi,scenario,cardWidth:Math.round(r.cards[0].card.width),rangeWidth:Math.round(r.cards[0].range.width)});
   }
   await context.close();
  }
 }finally{await browser.close();await new Promise(r=>server.close(r));}
 console.log(JSON.stringify({renderedCases:measurements.length}));
});

// 滑杆改了之后必须出现「保存」，并且发出去的请求要带上这张卡的 skill_id。
// 旧测试只验了布局，验不到这条——而 skill_id 串号是这类列表最容易出的错。
test('dragging a skill slider reveals save and posts the right skill_id',async()=>{
 const state={names:['海钓','浮子钓鱼','纺车钓鱼'],posts:[]};
 const server=serve(state);
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const base=`http://127.0.0.1:${server.address().port}/admin`;
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER});
 try{
  const context=await browser.newContext({viewport:{width:1280,height:900}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/#/players/1/skills`,{waitUntil:'networkidle'});
  await page.getByText('未分配的技能点').waitFor();

  const card=page.locator('#view .skill-card').nth(2); // 第三张才是纺车钓鱼（skill_id 2，0%）
  assert.match(await card.innerText(),/纺车钓鱼/);
  const save=card.locator('button', {hasText:'保存'});
  assert.equal(await save.isVisible(),false,'没动过滑杆时不该出现保存按钮');

  await card.locator('input[type=range]').fill('66.5');
  await save.waitFor({state:'visible'});
  // 百分比读数要跟着滑杆走，不能等保存后才更新。
  assert.equal(await card.locator('output').innerText(),'66.5%');
  await save.click();
  await page.waitForFunction(()=>true);

  const post=state.posts.find(p=>p.path==='/api/players/skills/update');
  assert.ok(post,'应 POST /api/players/skills/update');
  assert.equal(post.body.skill_id,2,'第二张卡对应的 skill_id 应是 2，串号会让改 A 变成改 B');
  assert.equal(post.body.uid,1);
  assert.equal(post.body.points,66.5);
  await page.waitForFunction(()=>{
    const b=[...document.querySelectorAll('#view .skill-card button')].find(x=>x.textContent==='保存');
    return !b||b.hidden;
  },null,{timeout:5000});
  assert.deepEqual(errors,[]);
  await context.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));}
});