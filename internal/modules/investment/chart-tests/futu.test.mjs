import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { EQUITY_24H_SESSION, FutuStream, isOvernightET, isSchwabExtendedET, overnightFetch, seriesKey } from '../chart/futu.js';

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
        detail: { service: 'LEVELONE_EQUITIES', timestamp: timestamp - 60_000, content: [{ key: 'AAPL', '3': 109, '8': 1000 }] }
    })); // initial quote snapshot
    events.dispatchEvent(new CustomEvent('LEVELONE_ANY', {
        detail: { service: 'LEVELONE_EQUITIES', timestamp: timestamp + 60_000, content: [{ key: 'AAPL', '3': 110, '8': 1005 }] }
    })); // subsequent trade
    events.dispatchEvent(new CustomEvent('FUTU_KL', {
        detail: { service: 'FUTU_KL', symbol: 'AAPL', resolution: '1', bar: { time: nightTime, open: 201, high: 201, low: 201, close: 201, volume: 6 } }
    }));
    assert.equal(regularTicks.at(-1), 110);
    assert.equal(nightTicks.at(-1), 201);
    assert.equal(feed.latestBars.get(seriesKey('AAPL', 'regular')).get('1').close, 110);
    assert.equal(feed.latestBars.get(seriesKey('AAPL', 'night')).get('1').close, 201);
});

test('removed 10-minute history is noData without reading settings or kline', async (t) => {
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
    assert.deepEqual(overnightCalls, []);
    assert.deepEqual(futuCalls, []);
});

test('Night is a standalone session using only Futu for all supported minute resolutions', async t => {
    const schwabCalls = [], futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: path => { schwabCalls.push(path); return json([candle]); },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async path => { futuCalls.push(path); return json([{ time: nightTime, open: 200, high: 200, low: 200, close: 200, volume: 5 }]); },
    });
    t.after(() => feed.destroy());
    const info = await new Promise((resolve, reject) => feed.resolveSymbol('AAPL', resolve, reject, { session: 'night' }));
    assert.equal(info.subsession_id, 'night');
    assert.deepEqual(info.subsessions.map(item => item.id), ['regular', 'extended', 'premarket', 'postmarket', 'night', EQUITY_24H_SESSION]);
    for (const resolution of ['1', '5', '15', '30']) {
        const { bars } = await history(feed, info, resolution);
        assert.equal(bars.length, 1);
        assert.equal(bars[0].close, 200);
    }
    assert.deepEqual(schwabCalls, []);
    assert.equal(futuCalls.length, 4);
});

test('Night commands stay off Schwab, wait for Futu readiness, and carry generation and owner', async t => {
    const events = new EventTarget();
    const schwabCalls = [], futuCalls = [];
    const feed = new Datafeed({
        events, stream: { ready: true },
        request: async path => { schwabCalls.push(path); return new Response('{}'); },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            futuCalls.push(JSON.parse(init.body));
            return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:1', owner: 'owner:1' }));
        },
    });
    t.after(() => feed.destroy());
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.deepEqual(futuCalls, [], 'Night ADD must wait until the Futu websocket supplies generation and owner');
    events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: 'gateway:1', owner: 'owner:1' } }));
    for (let i = 0; i < 50 && feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(schwabCalls, []);
    assert.deepEqual(futuCalls[0], { command: 'ADD', symbol: 'AAPL', resolution: '5', generation: 'gateway:1', owner: 'owner:1' });
    feed.unsubscribeBars('night');
    for (let i = 0; i < 50 && futuCalls.length < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(futuCalls[1], { command: 'UNSUB', symbol: 'AAPL', resolution: '5', generation: 'gateway:1', owner: 'owner:1' });
});

test('Night discards a stale response after a Schwab reconnect', async t => {
    const events = new EventTarget(), futu = Promise.withResolvers();
    const feed = new Datafeed({
        events, stream: { ready: false }, request: async () => json([candle]),
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async () => ({ ok: true, json: () => futu.promise }),
    });
    t.after(() => feed.destroy());
    const pending = history(feed, { name: 'AAPL', subsession_id: 'night' });
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

test('Schwab and Futu source windows partition ET without DST gaps or overlaps', () => {
    const cases = [
        ['2026-01-16T08:59:59Z', false, true],  // 03:59:59 EST
        ['2026-01-16T09:00:00Z', true, false], // 04:00 EST
        ['2026-01-17T00:59:59Z', true, false], // 19:59:59 EST
        ['2026-01-17T01:00:00Z', false, true], // 20:00 EST
        ['2026-03-08T08:00:00Z', true, false], // 04:00 EDT
        ['2026-11-01T09:00:00Z', true, false], // 04:00 EST after fall-back
    ];
    for (const [value, schwab, futu] of cases) {
        const time = Date.parse(value);
        assert.equal(isSchwabExtendedET(time), schwab, value);
        assert.equal(isOvernightET(time), futu, value);
    }
    assert.equal(EQUITY_24H_SESSION, 'equity24h', 'equity full-day ID must not collide with the legacy Forex 24h ID');
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

test('Futu websocket readiness propagates the backend lease generation', async t => {
    const events = new EventTarget();
    let socket, detail;
    events.addEventListener('FUTU_STREAM_READY', event => { detail = event.detail; });
    const stream = new FutuStream({
        events,
        status: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        socket: () => (socket = { close() {} }),
    });
    t.after(() => stream.close());
    stream.wanted = true;
    await stream.connect();
    socket.onmessage({ data: JSON.stringify({ stream: { status: 'ready', generation: 'gateway-a:2', owner: 'client-1' } }) });
    assert.deepEqual(detail, { generation: 'gateway-a:2', owner: 'client-1' });
    assert.equal(stream.generation, 'gateway-a:2');
    assert.equal(stream.owner, 'client-1');
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
    assert.equal(info.subsessions.some(session => session.id === '24h'), false);
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
    assert.equal(msft.subsessions.some(session => session.id === '24h'), false);
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
