import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { SchwabStream } from '../chart/stream.js';
import { seriesKey } from '../chart/futu.js';

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) { for (let i = 0; i < 100; i++) { if (check()) return; await sleep(10); } assert.fail('condition timed out'); }

test('browser reconnect waits for backend LOGIN readiness and replays all active subscriptions', async t => {
    const events = new EventTarget();
    const sockets = [], subscriptions = [];
    const stream = new SchwabStream({ events, retryDelay: 1, socket: () => {
        const socket = { close() { this.onclose?.(); } };
        sockets.push(socket);
        return socket;
    } });
    const feed = new Datafeed({ events, stream, request: async (_path, init) => { subscriptions.push(JSON.parse(init.body)); return new Response('{}'); } });
    t.after(() => { feed.destroy(); stream.close(); });
    let resets = 0;
    feed.subscribeBars({ name: 'AAPL' }, '1', () => {}, 'opaque-id', () => resets++);
    feed.subscribeQuotes(['MSFT'], ['TSLA'], () => {}, 'quotes-id');
    assert.equal(subscriptions.length, 0);
    sockets[0].onmessage({ data: '{"stream":{"status":"ready"}}' });
    await until(() => subscriptions.length === 1);
    assert.deepEqual(subscriptions[0].keys.split(',').sort(), ['AAPL', 'MSFT', 'TSLA']);
    sockets[0].close();
    await until(() => sockets.length === 2);
    assert.equal(stream.ready, false);
    sockets[1].onmessage({ data: '{"stream":{"status":"ready"}}' });
    await until(() => subscriptions.length === 2);
    assert.deepEqual(subscriptions[1].keys.split(',').sort(), ['AAPL', 'MSFT', 'TSLA']);
    assert.equal(resets, 2);
});

test('bar subscriptions route by symbol and resolution, and stop after unsubscribe', () => {
    const events = new EventTarget();
    const feed = new Datafeed({ events, stream: { ready: false }, request: async () => new Response('{}') });
    const now = Date.now(), bars = [];
    feed.latestBars.set(seriesKey('AAPL', 'regular'), new Map([['1', { time: Math.floor(now / 60000) * 60000, open: 100, high: 100, low: 100, close: 100, volume: 1 }]]));
    feed.subscribeBars({ name: 'AAPL' }, '1', bar => bars.push(bar), 'some-random-guid');
    const update = () => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: { service: 'LEVELONE_EQUITIES', timestamp: now, content: [{ key: 'AAPL', '3': 110, '8': 100 }] } }));
    update();
    assert.equal(bars.length, 1);
    assert.equal(bars[0].close, 110);
    feed.unsubscribeBars('some-random-guid');
    update();
    assert.equal(bars.length, 1);
    feed.destroy();
});

test('late events from a closed socket cannot mark its replacement ready', async t => {
    const events = new EventTarget(), sockets = [];
    let ready = 0, data = 0;
    events.addEventListener('SCHWAB_STREAM_READY', () => ready++);
    events.addEventListener('LEVELONE_ANY', () => data++);
    const stream = new SchwabStream({ events, retryDelay: 1, socket: () => {
        const socket = { close() { this.onclose?.(); } };
        sockets.push(socket);
        return socket;
    } });
    t.after(() => stream.close());
    const message = { data: JSON.stringify({ stream: { status: 'ready' }, data: [{ service: 'LEVELONE_EQUITIES', content: [] }] }) };
    sockets[0].close();
    await until(() => sockets.length === 2);
    sockets[0].onmessage(message);
    sockets[0].onclose();
    assert.equal(stream.ready, false);
    assert.equal(ready, 0);
    assert.equal(data, 0);
    sockets[1].onmessage(message);
    assert.equal(stream.ready, true);
    assert.equal(ready, 1);
    stream.close();
    sockets[1].onmessage(message);
    assert.equal(stream.ready, false);
    assert.equal(ready, 1);
});
