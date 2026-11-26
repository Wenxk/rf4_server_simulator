const {test}=require('node:test'),assert=require('node:assert/strict');
const http=require('node:http');
const {chromium}=require('playwright');
const {sendAsset}=require('./web_fixture.cjs');

// 商店与库存：在 1920/1024/360 三档宽度、1/1.25/1.5 三档 DPI 下，
// 商店标识从正常到极长时，页面不出现横向溢出、输入框不越界。
//
// 迁移时的取舍（批次 6）：
//   旧后台有 `#storeSummary` 一行摘要 + 每店一个 `#storeTable`；SPA 改成
//   「店铺列表表格 + 右侧「正在编辑」块」，覆盖输入框仍在表格里但换了锚点。
//   断言因此改为验两件真正会坏的事：
//     1) 长标识把页面撑宽（横向滚动条）；
//     2) 覆盖输入框跑出视口。
//   「必须有一个 id 叫 storeSummary 的元素」不属于行为，不搬。
test('store summary and controls fit supported widths, DPI and long identifiers',async()=>{
 let store='fishingstore_009';
 const server=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  const pathname=url.pathname.replace(/^\/admin\/api\//,'/api/');
  const json=value=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value));};
  if(pathname==='/api/me')return json({username:'admin',item_categories:{}});
  if(pathname==='/api/stores')return json([{store_id:store,item_count:1,override_count:0,map_key:'铜湖'}]);
  if(pathname==='/api/stores/items')return json([{store_id:store,item_id:'bait_001',name:'测试商品',category:2,base_silver:100,base_gold:1,base_stock:0,silver:100,gold:1,stock:1000,has_override:false}]);
  if(pathname.startsWith('/api/'))return json({});
  // 静态资源走真实文件：入口已是 SPA 空壳，模块要逐个读盘，回 HTML 会白屏。
  if(sendAsset(res,url.pathname))return;
  res.writeHead(404);res.end('');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,executablePath:process.env.RF4_TEST_BROWSER||require('node:path').join(process.env['ProgramFiles(x86)'],'Microsoft/Edge/Application/msedge.exe')});
 let cases=0;
 try{
  for(const width of [1920,1024,360])for(const scale of [1,1.25,1.5]){
   const context=await browser.newContext({viewport:{width,height:900},deviceScaleFactor:scale});
   await context.addInitScript(()=>{localStorage.setItem('rf4_admin_token','fixture');localStorage.setItem('rf4_local_admin_token','fixture');});
   const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
   for(const value of ['fishingstore_009','最长合理商店标识中文'.repeat(4),'Store'.repeat(32)]){
    store=value;
    // hash 直接带商店 id：SPA 用 #/shop/<id>/<tab> 路由，不用旧后台的 go('stores')。
    await page.goto(`http://127.0.0.1:${server.address().port}/admin/#/shop/${encodeURIComponent(value)}/items`,{waitUntil:'networkidle'});
    // 覆盖输入框出现 = 商品表已渲染完。
    const inputs=page.locator('.work input.input.num');
    await inputs.first().waitFor();
    const m=await page.evaluate(()=>({
      page:document.documentElement.scrollWidth,
      viewport:innerWidth,
      // 侧栏「正在编辑」块：长店名最容易把它撑破。
      aside:document.querySelector('.work aside')?.scrollWidth??0,
      asideClient:document.querySelector('.work aside')?.clientWidth??0,
    }));
    assert.ok(m.page<=m.viewport+2,`页面横向溢出 ${JSON.stringify({width,scale,value,m})}`);
    assert.ok(m.aside<=m.asideClient+2,`侧栏内容溢出 ${JSON.stringify({width,scale,value,m})}`);
    for(const input of await inputs.all()){
      await input.scrollIntoViewIfNeeded();
      const r=await input.boundingBox();
      assert.ok(r&&r.x>=0&&r.x+r.width<=width+2,JSON.stringify({width,scale,value,r}));
    }
    assert.deepEqual(errors,[],'页面不应有 JS 错误：'+errors.join(' | '));
    cases++;
   }
   await context.close();
  }
  console.log(JSON.stringify({storeRenderCases:cases,widths:[1920,1024,360],dpi:[1,1.25,1.5],textCases:3}));
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
});