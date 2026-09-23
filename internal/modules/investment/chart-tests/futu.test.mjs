import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { FutuStream, isOvernightET, overnightFetch, seriesKey } from '../chart/futu.js';

const timestamp = Date.parse('2026-09-10T14:30:00Z');
const nightTime = Date.parse('2026-01-15T02:00:00Z'); // 21:00 EST
const candle = { datetime: timestamp, open: 100, high: 100, low: 100, close: 100, volume: 10 };
const json = (candles) => new Response(JSON.stringify({ candles }));
const history = (feed, info, resolution = '1', params = { from: timestamp / 1000 - 3600, to: timestamp / 1000 + 60, firstDataRequest: true }) =>
    new Promise((resolve, reject) => {
        feed.getBars(info, resolution, params, (bars, metadata) => resolve({ bars, metadata }), reject);
    });

test('regular getBars never calls futuRequest', async (t) => {
    const futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(),
        stream: { ready: false },
        request: async () => json([candle]),
        futuRequest: async (path) => { futuCalls.push(path); return json([]); },
    });
    t.after(() => feed.destroy());
    const { bars } = await history(feed, { name: 'AAPL', subsession_id: 'regular' });
    assert.equal(bars.length, 1);
    assert.deepEqual(futuCalls, []);
});

test('night and regular live bars do not clobber each other', async (t) => {
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false },
        request: async () => json([candle]),
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (path) => {
            if (String(path).startsWith('/kline')) return json([{ time: nightTime, open: 200, high: 200, low: 200, close: 200, volume: 5 }]);
            return new Response('{}');
        },
    });
    t.after(() => feed.destroy());
    const regular = { name: 'AAPL', subsession_id: 'regular' };
    const night = { name: 'AAPL', subsession_id: 'night' };
    await history(feed, regular);
    await history(feed, night, '1', { from: nightTime / 1000 - 3600, to: nightTime / 1000 + 60, firstDataRequest: true });
    const regularTicks = [], nightTicks = [];
    feed.subscribeBars(regular, '1', (bar) => regularTicks.push(bar.close), 'r');
    feed.subscribeBars(night, '1', (bar) => nightTicks.push(bar.close), 'n');
    events.dispatchEvent(new CustomEvent('LEVELONE_ANY', {
        detail: { service: 'LEVELONE_EQUITIES', timestamp, content: [{ key: 'AAPL', '3': 110, '8': 1000 }] }
    }));
    events.dispatchEvent(new CustomEvent('FUTU_KL', {
        detail: { service: 'FUTU_KL', symbol: 'AAPL', resolution: '1', bar: { time: nightTime, open: 201, high: 201, low: 201, close: 201, volume: 6 } }
    }));
    assert.equal(regularTicks.at(-1), 110);
    assert.equal(nightTicks.at(-1), 201);
    assert.equal(feed.latestBars.get(seriesKey('AAPL', 'regular')).get('1').close, 110);
    assert.equal(feed.latestBars.get(seriesKey('AAPL', 'night')).get('1').close, 201);
});

test('night 10-minute history is noData and does not call kline', async (t) => {
    const futuCalls = [];
    const overnightCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: async () => json([candle]),
        overnightRequest: async () => { overnightCalls.push(true); return new Response(JSON.stringify({ enabled: true, providerEnabled: true })); },
        futuRequest: async (path) => { futuCalls.push(path); return json([]); },
    });
    t.after(() => feed.destroy());
    const { metadata } = await history(feed, { name: 'AAPL', subsession_id: 'night' }, '10');
    assert.equal(metadata.noData, true);
    assert.deepEqual(overnightCalls, [true]);
    assert.deepEqual(futuCalls, []);
});

test('24h 10-minute stays on Schwab', async (t) => {
    const futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: async () => json([candle]),
        futuRequest: async (path) => { futuCalls.push(path); return new Response(JSON.stringify({ enabled: true, providerEnabled: true })); },
    });
    t.after(() => feed.destroy());
    const { bars } = await history(feed, { name: 'AAPL', subsession_id: '24h' }, '10');
    assert.equal(bars.length, 1);
    assert.deepEqual(futuCalls, []);
});

test('24h history loads both providers concurrently and merges only after both finish', async t => {
    const calls = [];
    const schwab = Promise.withResolvers(), futu = Promise.withResolvers();
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: () => { calls.push('schwab'); return schwab.promise; },
        overnightRequest: () => Promise.resolve(new Response(JSON.stringify({ enabled: true, providerEnabled: true }))),
        futuRequest: path => {
            calls.push('futu');
            return futu.promise;
        },
    });
    t.after(() => feed.destroy());
    let settled = false;
    const pending = history(feed, { name: 'AAPL', subsession_id: '24h' }).then(result => { settled = true; return result; });
    await new Promise(setImmediate);
    assert.deepEqual(calls, ['schwab', 'futu'], 'a slow Schwab response must not delay the Futu request');
    futu.resolve(json([{ time: nightTime, open: 200, high: 200, low: 200, close: 200, volume: 5 }]));
    await new Promise(setImmediate);
    assert.equal(settled, false, 'partial history must not be published');
    schwab.resolve(json([candle]));
    const { bars } = await pending;
    assert.deepEqual(bars.map(bar => bar.close), [200, 100]);
});

test('24h history rejects a reconnect while the last response body is still being read', async t => {
    const events = new EventTarget(), futu = Promise.withResolvers();
    const feed = new Datafeed({
        events, stream: { ready: false }, request: async () => json([candle]),
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async () => ({ ok: true, json: () => futu.promise }),
    });
    t.after(() => feed.destroy());
    const pending = history(feed, { name: 'AAPL', subsession_id: '24h' });
    const rejected = assert.rejects(pending, /连接已变更/);
    await new Promise(setImmediate);
    events.dispatchEvent(new Event('SCHWAB_STREAM_CLOSED'));
    futu.resolve({ candles: [] });
    await rejected;
    assert.equal(feed.latestBars.size, 0);
});

test('isOvernightET uses America/New_York including DST', () => {
    assert.equal(isOvernightET(Date.parse('2026-01-15T01:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-01-15T09:00:00Z')), false);
    assert.equal(isOvernightET(Date.parse('2026-11-01T05:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-11-01T06:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-03-08T08:00:00Z')), false);
});

test('night data and stream require both provider and business switches', async (t) => {
    for (const [enabled, providerEnabled] of [[false, false], [true, false], [false, true]]) {
        const calls = [];
        const status = async () => new Response(JSON.stringify({ enabled, providerEnabled }));
        const feed = new Datafeed({ events: new EventTarget(), stream: { ready: false }, overnightRequest: async () => { calls.push(true); return status(); } });
        t.after(() => feed.destroy());
        const result = await history(feed, { name: 'AAPL', subsession_id: 'night' });
        assert.equal(result.metadata.noData, true);
        assert.deepEqual(calls, [true]);
        let sockets = 0;
        const stream = new FutuStream({ events: new EventTarget(), status, socket: () => { sockets++; return {}; } });
        stream.wanted = true;
        await stream.connect();
        assert.equal(sockets, 0);
        assert.equal(stream.wanted, false);
        stream.close();
    }
});

test('overnightFetch reads the lightweight settings endpoint with same-origin credentials', async (t) => {
    let call;
    t.mock.method(globalThis, 'fetch', async (...args) => {
        call = args;
        return new Response('{}');
    });
    const response = await overnightFetch();
    assert.equal(response.ok, true);
    assert.deepEqual(call, ['/api/modules/investment/overnight', { method: 'GET', credentials: 'same-origin' }]);
});

test('regular symbol resolution does not call the slow Futu status endpoint and keeps overnight sessions', async (t) => {
    let futuCalls = 0;
    const feed = new Datafeed({
        events: new EventTarget(),
        stream: { ready: false },
        futuRequest: () => { futuCalls++; return new Promise(() => {}); },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuStream: { start() {}, stop() {} },
    });
    t.after(() => feed.destroy());
    const info = await new Promise((resolve, reject) => feed.resolveSymbol('AAPL', resolve, reject));
    assert.equal(futuCalls, 0);
    assert.ok(info.subsessions.some(session => session.id === 'night'));
    assert.ok(info.subsessions.some(session => session.id === '24h'));
});

test('concurrent symbol resolutions share the overnight settings request', async (t) => {
    const settings = Promise.withResolvers();
    let requests = 0, starts = 0;
    const feed = new Datafeed({
        events: new EventTarget(),
        stream: { ready: false },
        overnightRequest: () => { requests++; return settings.promise; },
        futuStream: { start() { starts++; }, stop() {} },
    });
    t.after(() => feed.destroy());
    const resolve = symbol => new Promise((ok, fail) => feed.resolveSymbol(symbol, ok, fail));
    const first = resolve('AAPL'), second = resolve('MSFT');
    await new Promise(setImmediate);
    assert.equal(requests, 1);
    settings.resolve(new Response(JSON.stringify({ enabled: true, providerEnabled: true })));
    const [aapl, msft] = await Promise.all([first, second]);
    assert.ok(aapl.subsessions.some(session => session.id === 'night'));
    assert.ok(msft.subsessions.some(session => session.id === '24h'));
    assert.equal(starts, 1);
});

test('destroying while overnight settings are pending does not start Futu', async (t) => {
    const settings = Promise.withResolvers();
    let starts = 0;
    const feed = new Datafeed({
        events: new EventTarget(),
        stream: { ready: false },
        overnightRequest: () => settings.promise,
        futuStream: { start() { starts++; }, stop() {} },
    });
    const pending = feed.ensureFutuOverlay();
    feed.destroy();
    settings.resolve(new Response(JSON.stringify({ enabled: true, providerEnabled: true })));
    await pending;
    assert.equal(starts, 0);
});
