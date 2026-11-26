const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const {chromium}=require('playwright');

// 「鱼种档案 → 经济四档」页签：列表 → 编辑 → 试算 → 保存，POST 必须只带 (鱼种, 四档)。
test('species econ page edits xp and price per grade',async()=>{
 const html=fs.readFileSync(path.join(__dirname,'web/index.html'));
 let posted=null;
 let previewCalls=0;
 const grades=[1,2,3,4].map(g=>({grade:g,base_xp:[0.0735,0.24,1.4999,2.9998][g-1],base_price:[3.31,4.965,9.93,19.86][g-1],xp_c:null,price_per_kg:null}));
 const species={fish_key:'bream',name_zh:'欧鳊',min_g:100,trophy_g:700,trophy_line_g:4400,super_trophy_line_g:7000,grades,overridden:false,price_missing:false};
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture'),key=url.pathname.replace(/^\/admin/,'');
  const json=(value,status=200)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
  if(key==='/api/me')return json({username:'admin',item_categories:{}});
  if(key==='/api/econ/species')return json({items:[species],count:1,base:'level_009_ahtuba',maps:[
   {key:'level_009_ahtuba',name:'阿赫图巴河',pct:1},
   {key:'level_001_lake',name:'克马羚诺也湖',pct:0.831},
  ]});
  if(key==='/api/econ/preview'){
   previewCalls++;
   const grade=url.searchParams.get('grade');
   const xp=url.searchParams.get('xp_c');
   return json({xp:xp?Math.trunc(parseFloat(xp)*1000):1499,price_cents:5000,price_silver:50,
    base_xp_rate:1.4999,base_price:9.93,xp_c:xp?parseFloat(xp):0,price_per_kg:0,
    market_factor:1.0209,
    map:'level_009_ahtuba',map_pct:1,weight_g:1000,grade:parseFloat(grade),saved:false});
  }
  if(key==='/api/econ/species/save'&&req.method==='POST'){
   let body='';req.on('data',c=>body+=c);req.on('end',()=>{posted=JSON.parse(body);json({ok:true})});return;
  }
  if(key.startsWith('/api/'))return json({});
  // SPA 入口会按相对路径去拉 app/*.js 与 *.css：这些必须真的从 web/ 目录读出来，
  // 不能像旧后台那样一律回 index.html，否则模块加载拿到的是 HTML，整页白屏。
  const rel=decodeURIComponent(key.replace(/^\/+/, ''));
  const file=path.join(__dirname,'web',rel);
  if(rel && fs.existsSync(file) && fs.statSync(file).isFile()){
   const ext=path.extname(file).toLowerCase();
   const mime={'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.jpg':'image/jpeg',
     '.webp':'image/webp','.png':'image/png','.json':'application/json','.html':'text/html; charset=utf-8'}[ext]||'application/octet-stream';
   res.writeHead(200,{'Content-Type':mime});res.end(fs.readFileSync(file));return;
  }
  res.writeHead(200,{'Content-Type':'text/html; charset=utf-8'});res.end(html);
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 let browser;
 try{
  browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||path.join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
  const context=await browser.newContext({viewport:{width:1600,height:1000}});
  await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture')});
  const page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/admin/#/ecology`,{waitUntil:'networkidle'});
  // 鱼种经济已并入「鱼种档案」的第 3 个页签（生态基线 / 饵料规则 / 经济四档 / 权重档位）。
  await page.locator('#ecoTabs button[data-etab="econ"]').click();

  // 列表：中文名、四档内置值、状态「默认」。
  const table=page.locator('#econTable');
  await table.locator('text=欧鳊').waitFor();
  const text=await table.innerText();
  assert.match(text,/1\.4999/);
  assert.match(text,/9\.93/);
  assert.match(text,/默认/);
  assert.equal(await page.locator('#econStatus').innerText(),'1 个鱼种 · 0 个已覆盖');

  // 编辑：四档都有输入框，默认值为空（跟随内置）。
  await page.locator('button:has-text("编辑")').click();
  await page.locator('#eco_xp_3').waitFor();
  assert.equal(await page.locator('#eco_xp_3').inputValue(),'');
  assert.equal(await page.locator('#eco_pr_3').inputValue(),'');

  // 试算跟着输入走：2 XP/g × 1000g = 2000。
  await page.locator('#eco_xp_3').fill('2');
  await page.waitForFunction(()=>document.querySelector('#ecoPreview').textContent.includes('2,000'),null,{timeout:4000});
  assert.match(await page.locator('#ecoPreview').innerText(),/50\.00/);
  // 必须摊开当日行情系数与算式，否则「1kg 卖价 != 每公斤价」会被当成算错（2026-11-23 报障）。
  const previewText = await page.locator('#ecoPreview').innerText();
  assert.match(previewText, /当日行情/, '试算缺少当日行情说明');
  assert.match(previewText, /1\.0209/, '试算未显示行情系数数值');
  assert.match(previewText, /算式/, '试算缺少算式展开');
  assert.ok(previewCalls>0);

  await page.locator('#eco_pr_3').fill('50');
  await page.locator('button:has-text("保存")').click();
  await new Promise(r=>setTimeout(r,400));
  assert.ok(posted,'没有发出保存请求');
  assert.equal(posted.fish_key,'bream');
  assert.equal(posted.grades.length,4);
  assert.equal(posted.grades[2].grade,3);
  assert.equal(posted.grades[2].xp_c,2);
  assert.equal(posted.grades[2].price_per_kg,50);
  assert.equal(posted.grades[0].xp_c,null);
  assert.equal(posted.grades[0].price_per_kg,null);

  assert.deepEqual(errors,[]);
  await context.close();
 } finally {
  if(browser)await browser.close();
  server.close();
 }
});
