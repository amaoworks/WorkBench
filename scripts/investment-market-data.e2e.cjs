// Disposable Workbench, real terminal/datafeed, synthetic API/WS data and a minimal TV widget shim.
// This verifies source routing and delivery, not TradingView's rendering or live brokerage permissions.
const assert = require('node:assert/strict');
const { mkdtemp, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { createServer } = require('node:net');
const { spawn } = require('node:child_process');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const at = Date.parse('2026-11-04T15:00:00Z');
const night = Date.parse('2026-11-04T02:00:00Z');
const candle = (time, close=101) => ({ time, open:100, high:103, low:99, close, volume:100 });
const widgetShim = `export class widget {
 constructor(options) {
  window.fixture = { options, bars:[], ticks:[], quotes:[] };
  this.options=options; this.info={name:options.symbol,subsession_id:'regular'}; this.resolution=options.interval;
  const event={subscribe(){},unsubscribe(){}};
  this.chart={symbol:()=>options.symbol, resolution:()=>this.resolution, symbolExt:()=>this.info,
   setSymbol:async()=>{},setResolution:async r=>{this.resolution=r},onIntervalChanged:()=>event,onSymbolChanged:()=>event,resetData(){}};
  options.datafeed.now=()=>${at};
  this.ready=new Promise((resolve,reject)=>options.datafeed.resolveSymbol(options.symbol, info=>{
   this.info=info;
   options.datafeed.getBars(info,'5',{from:${at/1000-86400},to:${at/1000+3600},firstDataRequest:true},bars=>{
    window.fixture.bars=bars;
    options.datafeed.subscribeBars(info,'5',bar=>window.fixture.ticks.push(bar),'fixture-bars');
    options.datafeed.subscribeQuotes([options.symbol],[],quotes=>window.fixture.quotes.push(...quotes),'fixture-quotes');
    options.datafeed.getQuotes([options.symbol],quotes=>{window.fixture.quotes.push(...quotes);resolve()},reject);
   },reject);
  },reject));
 }
 chartReady(){return this.ready} headerReady(){return Promise.resolve()}
 activeChart(){return this.chart} chart(){return this.chart} chartsCount(){return 1}
 resetCache(){} applyOverrides(){} subscribe(){} changeTheme(){return Promise.resolve()}
 createDropdown(){return Promise.resolve({applyOptions(){},remove(){}})}
 save(callback){callback({})} remove(){window.fixture.removed=true}
 watchList(){const event={subscribe(){},unsubscribe(){}};return Promise.resolve({
  getAllLists:()=>({}),getActiveListId:()=>'',createList(){},renameList(){},setActiveList(){},deleteList(){},
  onListAdded:()=>event,onListChanged:()=>event,onListRemoved:()=>event,onActiveListChanged:()=>event,
 });}
}`;
async function freePort(){const s=createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p;}
(async()=>{
 const dir=await mkdtemp(join(tmpdir(),'workbench-market-data-'));
 const port=await freePort(),url=`http://127.0.0.1:${port}`;
 let app,browser;
 try {
  const env={...process.env};for(const key of Object.keys(env))if(/^(OPENAI_|WORKBENCH_|SCHWAB_|FUTU_)/.test(key))delete env[key];
  app=spawn(process.env.WORKBENCH_BIN || '/tmp/workbench-market-data-test',['-data',join(dir,'data.db'),'-db-driver','sqlite','-auth','local','-listen',`127.0.0.1:${port}`],{env,stdio:'ignore'});
  let spawnError;app.on('error',error=>{spawnError=error});
  for(let i=0;i<100;i++){if(spawnError)throw spawnError;if(app.exitCode!==null)throw Error('test service exited');try{if((await fetch(url+'/health/ready')).ok)break}catch{}await new Promise(r=>setTimeout(r,100));}
  browser=await chromium.launch({headless:true,executablePath:process.env.CHROMIUM_PATH || undefined,args:['--no-sandbox']});
  const context=await browser.newContext({viewport:{width:1440,height:1000}});
  let provider='futu',overnight=false,reauth=false;
  const requests=[],sockets=[];
  await context.route('**/charting_library/**',route=>route.fulfill({contentType:'application/javascript',body:widgetShim}));
  await context.routeWebSocket('**/api/modules/investment/**/ws*',ws=>{
   sockets.push(ws);const path=new URL(ws.url()).pathname;
   if(path.includes('/futu/'))ws.send(JSON.stringify({stream:{status:'ready',owner:'fixture-owner',generation:'fixture:1'}}));
   else ws.send(JSON.stringify({stream:{status:'ready'}}));
  });
  await context.route('**/api/modules/investment/**',async route=>{
   const request=route.request(),u=new URL(request.url()),path=u.pathname;
   requests.push({path,search:u.search,method:request.method()});
   const json=value=>route.fulfill({json:value});
   if(path.endsWith('/market-data'))return json({provider,schwabConnected:provider==='schwab',reauthorizationRequired:reauth,futuEnabled:provider!=='none',overnightEnabled:overnight});
   if(path.endsWith('/schwab'))return json({configured:true,appKey:'',callbackUrl:'',hasAppSecret:false,connected:provider==='schwab',reauthorizationRequired:reauth,lastError:''});
   if(path.endsWith('/overnight'))return json({enabled:overnight,provider:'futu',providerEnabled:true});
   if(path.endsWith('/futu/symbols'))return json({items:[{symbol:'AAPL',description:'Apple',exchange:'NASDAQ',type:'stock'}]});
   if(path.endsWith('/futu/quotes'))return json({items:[{symbol:'AAPL',description:'Apple',price:101,change:1,changePct:1,time:at,session:'regular'}]});
   if(path.endsWith('/futu/kline'))return json({candles:[candle(u.searchParams.get('session')==='regular'?at:night)]});
   if(path.endsWith('/futu/quote/ws/command')){
    const input=request.postDataJSON();assert.equal(input.owner,'fixture-owner');assert.equal(input.generation,'fixture:1');
    if(input.command==='ADD')for(const ws of sockets.filter(s=>s.url().includes('/futu/'))){
     const main=ws.url().includes('mode=main');
     const data=input.resolution ? {service:'FUTU_KL',symbol:input.symbol,resolution:input.resolution,bar:candle(main?at:night,102)}
      :{service:'FUTU_QUOTE',symbol:input.symbol,quote:{lp:102,ch:2,chp:2,time:at,session:main?'regular':'night'}};
     try{ws.send(JSON.stringify({data:[data]}))}catch{}
    }
    return json({success:true,applied:true,owner:'fixture-owner',generation:'fixture:1'});
   }
   if(path.endsWith('/schwab/marketdata/v1/pricehistory'))return json({candles:[{datetime:at,open:100,high:103,low:99,close:101,volume:100}]});
   if(path.endsWith('/schwab/marketdata/v1/quotes'))return json({AAPL:{quote:{lastPrice:101,closePrice:100,quoteTime:at},reference:{description:'Apple'}}});
   if(path.endsWith('/schwab/marketdata/v1/instruments'))return json({instruments:[{symbol:'AAPL',description:'Apple',exchange:'NASDAQ',assetType:'EQUITY'}]});
   if(path.endsWith('/schwab/marketdata/ws/command'))return json({success:true});
   if(path.endsWith('/preferences'))return request.method()==='GET'?json({entries:{chart:{revision:1,value:{symbol:'AAPL',resolution:'5',session:'regular'}}}}):json({revision:2,sequence:request.postDataJSON().sequence});
   if(path.endsWith('/watchlists'))return request.method()==='GET'?json({revision:1,state:{lists:[],activeId:''}}):json({revision:2,sequence:request.postDataJSON().sequence});
   return route.continue();
  });
  const page=await context.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(url+'/investment?symbol=AAPL');
  const frame=()=>page.frames().find(f=>f.url().includes('/investment/terminal'));
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentWindow.fixture?.quotes.some(q=>q.v.lp===102));
  let terminal=frame();
  assert.equal(await terminal.evaluate(()=>typeof window.fixture.options.broker_factory),'undefined');
  assert.equal(await terminal.evaluate(()=>window.fixture.options.datafeed.provider),'futu');
  assert.ok(await terminal.evaluate(()=>window.fixture.bars.length>0&&window.fixture.ticks.length>0));
  assert.equal(requests.some(r=>/\/schwab\/(marketdata|trader)/.test(r.path)),false);
  assert.ok(sockets.some(ws=>ws.url().includes('mode=main')));
  await terminal.evaluate(async()=>{
   const feed=window.fixture.options.datafeed;
   for(const resolution of ['1D','1W','1M'])await new Promise((resolve,reject)=>feed.getBars({name:'AAPL',subsession_id:'regular'},resolution,{from:1793746800,to:1793836800},resolve,reject));
   const result=await new Promise(resolve=>feed.searchSymbols('AAPL','','',resolve));if(result[0]?.symbol!=='AAPL')throw Error('symbol search failed');
  });
  const transition=requests.length;
  provider='schwab';overnight=true;
  await terminal.evaluate(()=>window.dispatchEvent(new Event('terminal:market-data-changed')));
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentWindow.fixture?.options.datafeed.provider==='schwab');
  terminal=frame();await terminal.waitForFunction(()=>window.fixture.quotes.length>0);
  assert.equal(await terminal.evaluate(()=>typeof window.fixture.options.broker_factory),'function');
  assert.equal(requests.slice(transition).some(r=>r.path.includes('/futu/kline')||r.path.includes('/futu/quotes')),false);
  await terminal.evaluate(async()=>{
   const feed=window.fixture.options.datafeed;
   await new Promise((resolve,reject)=>feed.getBars({name:'AAPL',subsession_id:'night'},'5',{from:1793718000,to:1793836800},resolve,reject));
   feed.subscribeBars({name:'AAPL',subsession_id:'night'},'5',()=>{},'fixture-night');
  });
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentWindow.futu?.ready===true);
  assert.ok(requests.slice(transition).some(r=>r.path.includes('/futu/kline')&&!r.search.includes('session=regular')));
  provider='futu';reauth=true;
  await terminal.evaluate(()=>window.dispatchEvent(new Event('terminal:market-data-changed')));
  await page.waitForFunction(()=>document.querySelector('iframe')?.contentWindow.fixture?.options.datafeed.provider==='futu');
  await page.getByText('已切换富途看盘，账户与交易已暂停。',{exact:true}).waitFor();
  assert.equal(await frame().evaluate(()=>typeof window.fixture.options.broker_factory),'undefined');
  provider='none';reauth=false;
  await frame().evaluate(()=>window.dispatchEvent(new Event('terminal:market-data-changed')));
  await page.getByRole('heading',{name:'暂无可用行情源',exact:true}).waitFor();
  assert.equal(await page.locator('iframe').count(),0);
  assert.deepEqual(errors,[]);
  console.log('PASS: real terminal/datafeed with synthetic TV/API fixtures: Futu history/quotes/live delivery without Schwab; night-off main feed; daily/weekly/monthly; source switches; Schwab uses Futu only for night; no Futu broker/trading UI or orders.');
 } finally {
  await browser?.close();if(app?.pid&&app.exitCode===null){const stopped=new Promise(r=>app.once('exit',r));app.kill('SIGTERM');await stopped;}
  await rm(dir,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1});
