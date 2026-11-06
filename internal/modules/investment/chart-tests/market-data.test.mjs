import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { FutuStream } from '../chart/futu.js';
import { futuBarTime } from '../chart/datafeed-bars.js';
import { loadMarketData, watchMarketData } from '../chart/market-data.js';

const regular = Date.parse('2026-11-04T15:00:00Z');
const night = Date.parse('2026-11-04T02:00:00Z');
const json = value => new Response(JSON.stringify(value));
const bar = time => ({ time, open: 100, high: 102, low: 99, close: 101, volume: 100 });
const status = provider => ({ provider, schwabConnected: provider === 'schwab', reauthorizationRequired: false, futuEnabled: true, overnightEnabled: false });
const waitFor = async check => { for (let i=0;i<100;i++) { if(check()) return; await new Promise(r=>setTimeout(r,5)); } assert.fail('condition timed out'); };
function setup(t, extra = {}) {
 const events = new EventTarget(), requests = [], commands = [];
 const feed = new Datafeed({ provider:'futu', events, stream: {ready:false},
  request: async path => assert.fail('Futu must not request Schwab: '+path),
  overnightRequest: async()=>json({enabled:false,providerEnabled:true}),
  futuStream:{start(){},stop(){}},
  futuRequest:async(path,init={})=>{
   requests.push(path);
   if(init.method==='POST') { commands.push(JSON.parse(init.body)); return json({success:true,applied:true,generation:'test:1',owner:'owner'}); }
   if(path.startsWith('/symbols')) return json({items:[{symbol:'AAPL',description:'Apple',exchange:'NASDAQ',type:'stock'}]});
   if(path.startsWith('/quotes')) return json({items:[{symbol:'AAPL',description:'Apple',price:101,change:1,changePct:1,session:'regular',time:regular}]});
   if(path.startsWith('/kline')) return json({candles:[bar(night),bar(regular)]});
   throw Error('unexpected '+path);
  },...extra,
 });
 t.after(()=>feed.destroy());
 return {feed,events,requests,commands};
}
const history=(feed,resolution='5',session='regular')=>new Promise((resolve,reject)=>feed.getBars(
 {name:'AAPL',subsession_id:session},resolution,{from:regular/1000-86400,to:regular/1000+3600},resolve,reject));

test('Futu resolves exact symbols, history and watchlist quotes without Schwab or night enabled',async t=>{
 const {feed,requests}=setup(t);
 const info=await new Promise((resolve,reject)=>feed.resolveSymbol('NASDAQ:AAPL',resolve,reject));
 assert.equal(info.name,'AAPL'); assert.equal(info.subsessions.some(s=>s.id==='night'),false);
 const bars=await history(feed); assert.deepEqual(bars,[bar(regular)]);
 assert.match(requests.find(path=>path.startsWith('/kline')),/session=regular/);
 const quotes=await new Promise((resolve,reject)=>feed.getQuotes(['NASDAQ:AAPL'],resolve,reject));
 assert.equal(quotes[0].n,'NASDAQ:AAPL'); assert.equal(quotes[0].v.lp,101);
 assert.ok(requests.some(path=>path.includes('symbols=AAPL')));
});

test('Futu regular and daily/week/month bars use the full-source endpoint',async t=>{
 const {feed,requests}=setup(t);
 for(const resolution of ['1D','1W','1M']) {
  await history(feed,resolution);
  assert.ok(requests.some(path=>path.includes('resolution='+resolution)&&path.includes('session=regular')));
 }
 await assert.rejects(history(feed,'5','night'),/夜盘行情未启用/);
 await assert.rejects(history(feed,'1D','extended'),/仅支持分钟/);
});

test('Futu live subscription routes are owner-bound, session-filtered and preserve quote names',async t=>{
 const {feed,events,commands}=setup(t);
 await feed.ensureFutuOverlay();
 events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY',{detail:{generation:'test:1',owner:'owner'}}));
 const bars=[],quotes=[];
 feed.subscribeBars({name:'AAPL',subsession_id:'regular'},'5',value=>bars.push(value),'chart');
 feed.subscribeQuotes(['NASDAQ:AAPL'],[],values=>quotes.push(...values),'watch');
 await waitFor(()=>commands.some(c=>c.resolution==='')&&commands.some(c=>c.resolution==='5'));
 assert.ok(commands.every(c=>c.owner==='owner'&&c.generation==='test:1'));
 for(const time of [night,regular]) events.dispatchEvent(new CustomEvent('FUTU_KL',{detail:{symbol:'AAPL',resolution:'5',bar:bar(time)}}));
 assert.deepEqual(bars,[bar(regular)]);
 events.dispatchEvent(new CustomEvent('FUTU_QUOTE',{detail:{symbol:'AAPL',quote:{lp:102,ch:2,chp:2,time:regular,session:'regular'}}}));
 assert.equal(quotes.at(-1).n,'NASDAQ:AAPL');assert.equal(quotes.at(-1).v.lp,102);
 feed.destroy(); assert.equal(feed.latestBars.size,0);assert.equal(feed.futuQuotes.size,0);assert.equal(feed.quotesListeners.size,0);
});

test('Futu snapshots replace cached quotes only with a newer timestamp', async t => {
 const { feed, events } = setup(t);
 await feed.ensureFutuOverlay();
 const updates = [];
 feed.subscribeQuotes(['AAPL'], [], values => updates.push(...values), 'watch');
 await feed.loadQuotes(['AAPL']);
 assert.equal(feed.mergedQuote('AAPL').lp, 101);
 const emit = (price, time, snapshot = true) => events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
  symbol: 'AAPL', snapshot, quote: { lp: price, time, session: 'regular' },
 } }));
 emit(120, regular + 60_000);
 assert.equal(updates.at(-1).v.lp, 120, 'a newer snapshot must replace a cached REST quote');
 emit(100, regular);
 emit(110, regular + 60_000);
 emit(90, undefined);
 emit(80, null);
 emit(70, regular - 1, false);
 assert.equal(updates.at(-1).v.lp, 120, 'delayed, equal-time and undated snapshots cannot regress the quote');
 assert.equal(feed.futuQuoteTimes.get('AAPL'), regular + 60_000);
});

test('Futu switching away and back clears only the final listener cache and refetches quotes', async t => {
 let requests = 0;
 const { feed, events } = setup(t, { futuRequest: async path => {
  assert.ok(path.startsWith('/quotes'));
  requests++;
  return json({ items: [{ symbol: 'AAPL', price: 90 + requests * 10, time: regular + (requests - 1) * 30_000, session: 'regular' }] });
 } });
 await feed.ensureFutuOverlay();
 feed.subscribeQuotes(['AAPL'], [], () => {}, 'chart');
 feed.subscribeQuotes(['AAPL'], [], () => {}, 'details');
 await feed.loadQuotes(['AAPL']);
 assert.equal(feed.mergedQuote('AAPL').lp, 100);
 feed.unsubscribeQuotes('chart');
 assert.equal(feed.mergedQuote('AAPL').lp, 100, 'another listener still owns the quote');
 feed.unsubscribeQuotes('details');
 for (const cache of [feed.futuQuotes, feed.futuQuoteTimes, feed.quote_cache]) assert.equal(cache.has('AAPL'), false);
 events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
  symbol: 'AAPL', quote: { lp: 95, time: regular, session: 'regular' },
 } }));
 assert.equal(feed.futuQuotes.has('AAPL'), false, 'unsolicited updates cannot refill the cache');
 feed.subscribeQuotes(['MSFT'], [], () => {}, 'chart');
 feed.unsubscribeQuotes('chart');
 const updates = [];
 feed.subscribeQuotes(['AAPL'], [], values => updates.push(...values), 'chart');
 await feed.loadQuotes(['AAPL']);
 assert.equal(requests, 2, 'returning to a symbol must fetch a fresh REST snapshot');
 assert.equal(feed.mergedQuote('AAPL').lp, 110);
 const snapshot = (price, time) => events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
  symbol: 'AAPL', snapshot: true, quote: { lp: price, time, session: 'regular' },
 } }));
 snapshot(120, regular + 60_000);
 snapshot(100, regular);
 assert.equal(updates.at(-1).v.lp, 120, 'the updated subscription snapshot wins over the old delayed snapshot');
});

test('Futu REST snapshots from a released subscription cannot refill the cache after returning', async t => {
 const responses = [];
 const { feed } = setup(t, { futuRequest: path => {
  assert.ok(path.startsWith('/quotes'));
  const pending = Promise.withResolvers();
  responses.push(pending);
  return pending.promise;
 } });
 await feed.ensureFutuOverlay();
 feed.subscribeQuotes(['AAPL'], [], () => {}, 'chart');
 const first = feed.loadQuotes(['AAPL']);
 await waitFor(() => responses.length === 1);
 feed.unsubscribeQuotes('chart');
 assert.equal(feed.quoteRequests.has('AAPL'), false);
 feed.subscribeQuotes(['AAPL'], [], () => {}, 'chart');
 const second = feed.loadQuotes(['AAPL']);
 await waitFor(() => responses.length === 2);
 responses[1].resolve(json({ items: [{ symbol: 'AAPL', price: 120, session: 'regular' }] }));
 await second;
 responses[0].resolve(json({ items: [{ symbol: 'AAPL', price: 100, time: regular, session: 'regular' }] }));
 await first;
 assert.equal(feed.mergedQuote('AAPL').lp, 120, 'even an undated current quote must not be replaced by a released request');
});

test('Schwab regular quotes never subscribe Futu during regular hours',async t=>{
 const commands=[];
 const {feed}=setup(t,{provider:'schwab',now:()=>regular,overnightRequest:async()=>json({enabled:true,providerEnabled:true}),
  futuRequest:async(path)=>{commands.push(path);return json({});}});
 feed.subscribeBars({name:'AAPL',subsession_id:'regular'},'5',()=>{},'chart');
 feed.subscribeQuotes(['AAPL'],[],()=>{},'quote');
 await feed.ensureFutuOverlay();
 assert.equal(feed.desiredFutuSubscriptions().size,0);
 await new Promise(r=>setTimeout(r,120));assert.deepEqual(commands,[]);
});

test('Futu daily timestamps normalize ET dates without shifting UTC markers',()=>{
 assert.equal(futuBarTime('1D',Date.parse('2026-11-04T05:00:00Z')),Date.parse('2026-11-04T00:00:00Z'));
 assert.equal(futuBarTime('1W',Date.parse('2026-11-04T05:00:00Z')),Date.parse('2026-11-02T00:00:00Z'));
 assert.equal(futuBarTime('1M',Date.parse('2026-11-04T05:00:00Z')),Date.parse('2026-11-01T00:00:00Z'));
 assert.equal(futuBarTime('1D',Date.parse('2026-11-04T00:00:00Z')),Date.parse('2026-11-04T00:00:00Z'));
});

test('Futu main socket can start with night disabled',async t=>{
 const urls=[];
 const stream=new FutuStream({mode:'main',events:new EventTarget(),status:async()=>json(status('futu')),
  socket:url=>{urls.push(url);return {close(){}};}});
 t.after(()=>stream.close());await stream.start();await waitFor(()=>urls.length===1);
 assert.match(urls[0],/mode=main/);
});

test('market-data validates source status and rebuilds once after authorization changes',async()=>{
 assert.equal((await loadMarketData(async()=>json(status('futu')))).provider,'futu');
 await assert.rejects(loadMarketData(async()=>json({...status('schwab'),schwabConnected:false})),/状态无效/);
 const events=new EventTarget(), changed=[];
 const stop=watchMarketData(status('schwab'),next=>changed.push(next.provider),{events,document:undefined,interval:60000,request:async()=>json(status('futu'))});
 events.dispatchEvent(new Event('terminal:market-data-changed'));
 await waitFor(()=>changed.length===1);
 events.dispatchEvent(new Event('terminal:market-data-changed'));
 await new Promise(setImmediate);assert.deepEqual(changed,['futu']);stop();
});
