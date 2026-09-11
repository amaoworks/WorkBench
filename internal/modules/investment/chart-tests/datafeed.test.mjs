import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';

const timestamp = Date.parse('2026-09-10T14:30:00Z');
const candle = { datetime: timestamp, open: 100, high: 100, low: 100, close: 100, volume: 10 };
const symbol = { name: 'AAPL', subsession_id: 'regular' };
const period = { from: timestamp / 1000 - 3600, to: timestamp / 1000 + 60, firstDataRequest: true };
const json = candles => new Response(JSON.stringify({ candles }));
const history = (feed, info = symbol, resolution = '1', params = period) => new Promise((resolve, reject) => {
    feed.getBars(info, resolution, params, (bars, metadata) => resolve({ bars, metadata }), reject);
});
function setup(t, request = async () => json([candle]), resetCharts = () => {}) {
    const events = new EventTarget();
    const feed = new Datafeed({ events, stream: { ready: false }, request, resetCharts });
    t.after(() => feed.destroy());
    const tick = (price, volume, time = timestamp) => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', {
        detail: { service: 'LEVELONE_EQUITIES', timestamp: time, content: [{ key: 'AAPL', '3': price, '8': volume }] }
    }));
    return { feed, events, tick };
}

test('multiple subscribers share one volume update and cannot mutate each other or the cache', async t => {
    const { feed, tick } = setup(t);
    await history(feed);
    const first = [], second = [];
    feed.subscribeBars(symbol, '1', bar => { first.push({ ...bar }); bar.volume = 999; bar.high = 999; }, 'first');
    feed.subscribeBars(symbol, '1', bar => second.push(bar), 'second');
    tick(101, 1000);
    tick(102, 1005);
    assert.deepEqual(first, second);
    assert.equal(second.at(-1).volume, 15);
    assert.equal(second.at(-1).high, 102);
});

test('reconnection requests chart history and non-initial backfill restarts live bars', async t => {
    let resets = 0;
    const { feed, events, tick } = setup(t, async (_path, init) => init?.method ? new Response('{}') : json([candle]), () => resets++);
    await history(feed);
    const bars = [];
    feed.subscribeBars(symbol, '1', bar => bars.push(bar), 'chart');
    events.dispatchEvent(new Event('SCHWAB_STREAM_CLOSED'));
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    assert.equal(resets, 1);
    await history(feed, symbol, '1', { ...period, firstDataRequest: false });
    tick(105, 1000);
    assert.equal(bars.at(-1)?.close, 105);
});

test('history started before a reconnect cannot refill the new connection cache', async t => {
    let release;
    const { feed, events } = setup(t, () => new Promise(resolve => { release = () => resolve(json([candle])); }));
    const pending = history(feed);
    const rejected = assert.rejects(pending, /连接已变更/);
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    release();
    await rejected;
    assert.equal(feed.latestBars.size, 0);
});

test('daily history is sorted, deduplicated and excludes the upper boundary', async t => {
    const midnight = Date.parse('2026-09-10T00:00:00Z');
    const { feed } = setup(t, async () => json([
        { ...candle, datetime: midnight + 86400000 + 14400000 },
        { ...candle, datetime: midnight + 14400000 },
        { ...candle, datetime: midnight - 86400000 + 14400000 },
        { ...candle, datetime: midnight + 14400000, close: 101 },
    ]));
    const { bars } = await history(feed, symbol, '1D', { ...period, to: (midnight + 86400000) / 1000 });
    assert.deepEqual(bars.map(bar => bar.time), [midnight - 86400000, midnight]);
    assert.equal(bars.at(-1).close, 101);
});

test('late history and out-of-order quotes cannot replace a newer live bar', async t => {
    const { feed, tick } = setup(t);
    const { bars: initial } = await history(feed);
    initial[0].high = 999;
    const live = [];
    feed.subscribeBars(symbol, '1', bar => live.push(bar), 'chart');
    tick(105, 1000, timestamp + 60000);
    await history(feed);
    tick(90, 990, timestamp);
    tick(106, 1005, timestamp + 60001);
    assert.equal(live.length, 2);
    assert.equal(live.at(-1).time, timestamp + 60000);
    assert.equal(live.at(-1).high, 106);
    assert.equal(live.at(-1).volume, 5);
});

test('invalid history and price-less quotes do not create NaN bars', async t => {
    const { feed, events } = setup(t, async () => json([candle]));
    await history(feed);
    const bars = [];
    feed.subscribeBars(symbol, '1', bar => bars.push(bar), 'chart');
    events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: { service: 'LEVELONE_EQUITIES', timestamp, content: [{ key: 'AAPL', '1': 101 }] } }));
    assert.equal(bars.length, 0);
    feed.request = async () => json({ invalid: true });
    await assert.rejects(history(feed), /历史数据/);
});
