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

test('quote subscribers receive the provider fields for each asset class', t => {
    const { feed, events } = setup(t);
    const cases = [
        ['LEVELONE_OPTIONS', 'AAPL 260918C00100000', { '4': 12, '3': 11, '2': 13, '8': 50 }, 12, 11, 50],
        ['LEVELONE_FUTURES', '/ES', { '3': 6000, '1': 5999, '2': 6001, '8': 80 }, 6000, 5999, 80],
        ['LEVELONE_FUTURES_OPTIONS', './ES', { '19': 20, '1': 19, '2': 21, '8': 90 }, 20, 19, 90],
        ['LEVELONE_FOREX', 'EUR/USD', { '29': 1.1, '1': 1.09, '2': 1.11, '6': 100 }, 1.1, 1.09, 100],
    ];
    for (const [service, name, fields, price, bid, volume] of cases) {
        const quotes = [];
        feed.subscribeQuotes([name], [], updates => quotes.push(...updates), name);
        events.dispatchEvent(new CustomEvent('LEVELONE_ANY', {
            detail: { service, timestamp, content: [{ key: name, ...fields }] }
        }));
        assert.equal(quotes.length, 1);
        assert.equal(quotes[0].n, name);
        assert.equal(quotes[0].v.lp, price);
        assert.equal(quotes[0].v.bid, bid);
        assert.equal(quotes[0].v.volume, volume);
    }
});

test('symbol resolution keeps night sessions exclusive to equities and preserves asset precision', async t => {
    const { feed } = setup(t);
    feed.futuRequest = async () => new Response(JSON.stringify({ enabled: true, overnightEnabled: true }));
    const resolve = name => new Promise((ok, fail) => feed.resolveSymbol(name, ok, fail, { session: 'night' }));
    const equities = await resolve(' AAPL ');
    assert.equal(equities.name, 'AAPL');
    assert.equal(equities.subsession_id, 'night');
    assert.equal(equities.session, '2000-0400');
    for (const [name, type] of [['AAPL 260918C00100000', 'OPTIONS'], ['/ES', 'FUTURES'], ['./ES', 'FUTURES_OPTIONS'], ['EUR/USD', 'FOREX']]) {
        const info = await resolve(name);
        assert.equal(info.type, type);
        assert.equal(info.subsession_id, 'regular');
        assert.equal(info.subsessions.some(session => session.id === 'night'), false);
        assert.equal(info.pricescale, type === 'FOREX' ? 100000 : 100);
    }
});

test('weekly and monthly history aligns to calendar boundaries', async t => {
    const { feed } = setup(t);
    for (const [resolution, expected] of [['1W', '2026-09-07T00:00:00Z'], ['1M', '2026-09-01T00:00:00Z']]) {
        const { bars } = await history(feed, symbol, resolution);
        assert.equal(bars[0].time, Date.parse(expected));
    }
});
