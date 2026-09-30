import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { EQUITY_24H_SESSION, isOvernightET, isSchwabExtendedET } from '../chart/futu.js';
import { isSchwabTradeUpdate } from '../chart/datafeed-realtime.js';

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
    tick(100, 1000); // initial LEVELONE snapshot primes the cumulative volume, not a trade
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
    tick(104, 1000); // first post-reconnect snapshot
    tick(105, 1000);
    assert.equal(bars.at(-1)?.close, 105);
});

test('history started before a reconnect cannot refill the new connection cache', async t => {
    let release;
    const { feed, events } = setup(t, () => new Promise(resolve => { release = () => resolve(json([candle])); }));
    const pending = history(feed);
    const rejected = assert.rejects(pending, /连接已变更/);
    events.dispatchEvent(new Event('SCHWAB_STREAM_CLOSED'));
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    release();
    await rejected;
    assert.equal(feed.latestBars.size, 0);
});

test('the first stream-ready event does not interrupt pending history or reset charts', async t => {
    let release, resets = 0;
    const events = new EventTarget();
    const feed = new Datafeed({
        events,
        stream: { ready: false },
        request: () => new Promise(resolve => { release = () => resolve(json([candle])); }),
        resetCharts: () => { resets++; },
    });
    t.after(() => feed.destroy());
    const pending = history(feed);
    await new Promise(setImmediate);
    assert.equal(typeof release, 'function');
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    release();
    const { bars } = await pending;
    assert.equal(bars.length, 1);
    assert.equal(feed.streamEpoch, 0);
    assert.equal(resets, 0);
});

test('a ready stream followed by another ready event is treated as a reconnect', t => {
    const events = new EventTarget();
    let resets = 0;
    const feed = new Datafeed({ events, stream: { ready: true }, resetCharts: () => { resets++; } });
    t.after(() => feed.destroy());
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    assert.equal(feed.streamEpoch, 1);
    assert.equal(resets, 1);
});

test('Schwab minute requests use supported frequencies and bound large chart viewports', async t => {
    const urls = [];
    const { feed } = setup(t, async path => { urls.push(new URL(path, 'https://example.test')); return json([]); });
    const months = { from: timestamp / 1000 - 360 * 86400, to: timestamp / 1000 };
    for (const resolution of ['1', '5', '15', '30']) {
        await history(feed, { name: 'AAPL', subsession_id: 'extended' }, resolution, months);
        const url = urls.at(-1).searchParams;
        assert.equal(url.get('frequencyType'), 'minute');
        assert.equal(url.get('frequency'), resolution);
        assert.equal(url.get('periodType'), 'day');
        assert.equal(url.get('needExtendedHoursData'), 'true');
        const range = (Number(url.get('endDate')) - Number(url.get('startDate'))) / 1000;
        assert(range <= (resolution === '1' ? 46 : 240) * 86400);
    }
});

test('10-minute bars are not advertised or requested in any session', async t => {
    const calls = [];
    const { feed } = setup(t, async path => { calls.push(path); return json([]); });
    const configuration = await new Promise(resolve => feed.onReady(resolve));
    assert.deepEqual(configuration.supported_resolutions, ['1', '5', '15', '30', '1D', '1W', '1M']);
    const info = await new Promise((resolve, reject) => feed.resolveSymbol('AAPL', resolve, reject));
    assert.equal(info.supported_resolutions.includes('10'), false);
    for (const subsession_id of ['regular', 'extended', 'night', EQUITY_24H_SESSION]) {
        const { bars, metadata } = await history(feed, { name: 'AAPL', subsession_id }, '10');
        assert.deepEqual(bars, []);
        assert.equal(metadata.noData, true);
    }
    assert.deepEqual(calls, []);
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
    tick(104, 1000, timestamp);
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

test('04:00 bid/ask and unchanged-volume quote patches cannot manufacture a 24H bar', t => {
    const events = new EventTarget();
    const feed = new Datafeed({ events, stream: { ready: false } });
    t.after(() => feed.destroy());
    const info = { name: 'AAPL', subsession_id: EQUITY_24H_SESSION };
    const prior = Date.parse('2026-01-16T08:59:00Z');
    feed.seedLatest('AAPL', EQUITY_24H_SESSION, '1', [{ time: prior, open: 100, high: 100, low: 100, close: 100, volume: 8 }]);
    const bars = [], quotes = [];
    feed.subscribeBars(info, '1', bar => bars.push(bar), 'full');
    feed.subscribeQuotes(['AAPL'], [], updates => quotes.push(...updates), 'quote');
    const emit = (time, fields) => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: Date.parse(time), content: [{ key: 'AAPL', ...fields }],
    } }));
    emit('2026-01-16T08:59:00Z', { '3': 100, '8': 1000 }); // opening snapshot primes state
    emit('2026-01-16T09:00:00Z', { '1': 99.5, '2': 100.5 }); // 04:00 ET, bid/ask only
    emit('2026-01-16T09:01:00Z', { '8': 1000 }); // unchanged cumulative volume
    assert.equal(bars.length, 0, 'quote-only updates must not create a 04:00 ET bar from stale last price');
    assert.equal(quotes.at(-1).v.bid, 99.5);
    assert.equal(quotes.at(-1).v.ask, 100.5);

    emit('2026-01-16T09:02:00Z', { '3': 100, '8': 1005 }); // same-price real print, volume advanced
    assert.equal(bars.length, 1, 'a same-price trade remains a valid bar event');
    assert.equal(bars[0].time, Date.parse('2026-01-16T09:02:00Z'));
    assert.equal(bars[0].close, 100);
    assert.equal(bars[0].volume, 5);
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
    feed.overnightRequest = async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true }));
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

test('Schwab trade detection ignores quote patches but accepts same-price volume prints across assets', () => {
    const cases = [
        ['LEVELONE_EQUITIES', { '3': 100, '8': 1000 }, { '1': 99, '2': 101 }, { '3': 100, '8': 1005 }, { '3': 101 }],
        ['LEVELONE_OPTIONS', { '4': 12, '8': 50 }, { '2': 13, '3': 11 }, { '4': 12, '8': 52 }, { '4': 13 }],
        ['LEVELONE_FUTURES', { '3': 6000, '8': 80 }, { '1': 5999, '2': 6001 }, { '3': 6000, '8': 82 }, { '3': 6001 }],
        ['LEVELONE_FUTURES_OPTIONS', { '3': 20, '8': 90 }, { '1': 19, '2': 21 }, { '3': 20, '8': 91 }, { '3': 21 }],
        ['LEVELONE_FOREX', { '3': 1.1, '6': 100 }, { '1': 1.09, '2': 1.11 }, { '3': 1.1, '6': 101 }, { '3': 1.2 }],
    ];
    for (const [service, previous, quotePatch, samePriceTrade, changedPriceTrade] of cases) {
        assert.equal(isSchwabTradeUpdate(service, quotePatch, previous), false, service);
        assert.equal(isSchwabTradeUpdate(service, samePriceTrade, previous), true, `${service} same-price volume increment`);
        assert.equal(isSchwabTradeUpdate(service, changedPriceTrade, previous), true, `${service} last price change`);
    }
    assert.equal(isSchwabTradeUpdate('LEVELONE_EQUITIES', { '8': 1 }, { '8': 1000 }), false, 'daily volume reset is not a trade event');
});

test('Schwab sparse patches compare the merged plotted price using each asset priority', () => {
    const previousEquity = { '3': 100, '33': 98, '8': 1000 };
    assert.equal(isSchwabTradeUpdate('LEVELONE_EQUITIES', { '33': 98 }, previousEquity), false,
        'the exact merged rtc=100 regression must not become a false trade');
    assert.equal(isSchwabTradeUpdate('LEVELONE_EQUITIES', { '33': 99 }, previousEquity), false,
        'changing a lower-priority fallback must not advance the plotted price');
    assert.equal(isSchwabTradeUpdate('LEVELONE_EQUITIES', { '33': 99, '8': 1005 }, previousEquity), true,
        'same plotted price still counts when cumulative volume increases');

    const priorities = [
        ['LEVELONE_EQUITIES', { '3': 100, '33': 98 }, { '33': 99 }],
        ['LEVELONE_OPTIONS', { '4': 12, '37': 11 }, { '37': 10 }],
        ['LEVELONE_FUTURES', { '3': 6000, '24': 5990 }, { '24': 5980 }],
        ['LEVELONE_FUTURES_OPTIONS', { '3': 20, '19': 19 }, { '19': 18 }],
        ['LEVELONE_FOREX', { '3': 1.1, '29': 1.09 }, { '29': 1.08 }],
    ];
    for (const [service, previous, lowerPriorityPatch] of priorities) {
        assert.equal(isSchwabTradeUpdate(service, lowerPriorityPatch, previous), false, `${service} plotted price priority`);
    }
});

test('ET source windows are DST-aware and exclude exact 04:00/20:00 boundaries', () => {
    for (const [time, schwab, futu] of [
        ['2026-01-16T09:00:00Z', true, false],  // 04:00 EST
        ['2026-01-17T01:00:00Z', false, true], // 20:00 EST
        ['2026-03-08T08:00:00Z', true, false], // 04:00 EDT after DST begins
        ['2026-11-01T05:00:00Z', false, true], // 01:00 EDT, repeated hour
        ['2026-11-01T06:00:00Z', false, true], // 01:00 EST, repeated hour
        ['2026-11-01T09:00:00Z', true, false], // 04:00 EST after DST ends
    ]) {
        const timestamp = Date.parse(time);
        assert.equal(isSchwabExtendedET(timestamp), schwab, time);
        assert.equal(isOvernightET(timestamp), futu, time);
    }
});

test('24H history concurrently merges filtered Schwab/Futu bars in ET order with deduplication', async t => {
    const schwabCalls = [], futuCalls = [];
    const at = value => Date.parse(value);
    const candleAt = (datetime, close) => ({ datetime: at(datetime), open: close, high: close, low: close, close, volume: 1 });
    const futuAt = (time, close) => ({ time: at(time), open: close, high: close, low: close, close, volume: 2 });
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        request: async path => {
            schwabCalls.push(path);
            return json([
                candleAt('2026-01-17T00:59:00Z', 119), // 19:59 ET: Schwab
                candleAt('2026-01-17T01:00:00Z', 120), // 20:00 ET: Futu boundary
                candleAt('2026-01-17T09:00:00Z', 104), // 04:00 ET: Schwab boundary
                candleAt('2026-01-17T09:59:00Z', 105),
                candleAt('2026-01-17T10:00:00Z', 106), // exclusive upper bound
                candleAt('2026-01-17T00:59:00Z', 119.5), // duplicate timestamp: last provider candle wins
            ]);
        },
        futuRequest: async path => {
            futuCalls.push(path);
            return json([
                futuAt('2026-01-17T00:59:00Z', 999), // daytime, never take from Futu
                futuAt('2026-01-17T01:00:00Z', 200), // 20:00 ET inclusive
                futuAt('2026-01-17T05:00:00Z', 201), // midnight ET, across date boundary
                futuAt('2026-01-17T05:00:00Z', 202), // duplicate timestamp
                futuAt('2026-01-17T08:59:00Z', 203), // 03:59 ET inclusive
                futuAt('2026-01-17T09:00:00Z', 204), // 04:00 ET exclusive
            ]);
        },
    });
    t.after(() => feed.destroy());
    feed.overnightCache = undefined;
    const info = await new Promise((resolve, reject) => feed.resolveSymbol('AAPL', resolve, reject, { session: EQUITY_24H_SESSION }));
    assert.equal(info.subsession_id, EQUITY_24H_SESSION);
    assert.equal(info.session, '0400-0400');
    assert.deepEqual(info.subsessions.slice(-2).map(item => item.id), ['night', EQUITY_24H_SESSION]);
    const from = at('2026-01-17T00:00:00Z') / 1000;
    const to = at('2026-01-17T10:00:00Z') / 1000;
    const { bars, metadata } = await history(feed, info, '1', { from, to, firstDataRequest: true });
    assert.equal(metadata.noData, false);
    assert.deepEqual(bars.map(bar => bar.time), [
        at('2026-01-17T00:59:00Z'), at('2026-01-17T01:00:00Z'), at('2026-01-17T05:00:00Z'),
        at('2026-01-17T08:59:00Z'), at('2026-01-17T09:00:00Z'), at('2026-01-17T09:59:00Z'),
    ]);
    assert.deepEqual(bars.map(bar => bar.close), [119.5, 200, 202, 203, 104, 105]);
    assert.equal(schwabCalls.length, 1);
    assert.match(schwabCalls[0], /needExtendedHoursData=true/);
    assert.equal(futuCalls.length, 1);
    const futuURL = new URL(futuCalls[0], 'https://example.test');
    assert.equal(futuURL.searchParams.get('requireHistory'), 'true');
    assert.equal(futuURL.searchParams.get('resolution'), '1');
    assert.equal(futuURL.searchParams.get('from'), String(from - Math.floor((to - from) * 0.5)));
    assert.equal(futuURL.searchParams.get('to'), String(to));
});

test('24H aligns Futu strict history with Schwab capped ranges and preserves older backfill chunks', async t => {
    const schwabCalls = [], futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        request: async path => { schwabCalls.push(new URL(path, 'https://example.test')); return json([]); },
        futuRequest: async path => { if (path.startsWith('/kline')) futuCalls.push(new URL(path, 'https://example.test')); return json([]); },
    });
    t.after(() => feed.destroy());
    const info = { name: 'AAPL', subsession_id: EQUITY_24H_SESSION };
    const year = { from: timestamp / 1000 - 365 * 86400, to: timestamp / 1000, firstDataRequest: true };
    await history(feed, info, '1', year);
    const schwabStart = Number(schwabCalls.at(-1).searchParams.get('startDate')) / 1000;
    const futuStart = Number(futuCalls.at(-1).searchParams.get('from'));
    assert.equal(schwabStart, futuStart, 'both 24H sources must start at Schwab’s 46-day cap');
    assert.equal((year.to - futuStart) / 86400, 46);
    assert.equal(Number(futuCalls.at(-1).searchParams.get('to')), year.to);

    const backfillTo = timestamp / 1000 - 300 * 86400;
    const backfill = { from: backfillTo - 86400, to: backfillTo, firstDataRequest: false };
    await history(feed, info, '1', backfill);
    const oldSchwabStart = Number(schwabCalls.at(-1).searchParams.get('startDate')) / 1000;
    const oldFutuStart = Number(futuCalls.at(-1).searchParams.get('from'));
    assert.equal(oldSchwabStart, oldFutuStart, 'older TradingView backfill chunks must retain their own shared start');
    assert(oldFutuStart < timestamp / 1000 - 46 * 86400, 'backfill must not be clamped to the latest 46 days');
    assert.equal(Number(futuCalls.at(-1).searchParams.get('to')), backfillTo);
});

test('24H history fails instead of returning one provider when either source fails', async t => {
    for (const failedSource of ['schwab', 'futu']) {
        const calls = [];
        const feed = new Datafeed({
            events: new EventTarget(), stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            request: async () => {
                calls.push('schwab');
                return failedSource === 'schwab' ? new Response('{}', { status: 503 }) : json([candle]);
            },
            futuRequest: async path => {
                if (path.startsWith('/kline')) {
                    calls.push('futu');
                    return failedSource === 'futu' ? new Response('{}', { status: 503 }) : json([{ time: timestamp, ...candle }]);
                }
                return new Response('{}');
            },
        });
        t.after(() => feed.destroy());
        const info = { name: 'AAPL', subsession_id: EQUITY_24H_SESSION };
        await assert.rejects(history(feed, info, '5'), failedSource === 'schwab' ? /获取历史数据失败/ : /获取夜盘数据失败/);
        assert.deepEqual(calls.sort(), ['futu', 'schwab']);
    }
});

test('24H option is gated to enabled US equities; Forex 24h stays a separate session', async t => {
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
    });
    t.after(() => feed.destroy());
    const resolve = (name, session = EQUITY_24H_SESSION) => new Promise((ok, fail) => feed.resolveSymbol(name, ok, fail, { session }));
    const equity = await resolve('AAPL');
    assert.equal(equity.subsession_id, EQUITY_24H_SESSION);
    assert.equal(equity.subsessions.some(item => item.id === EQUITY_24H_SESSION), true);
    const option = await resolve('AAPL 260918C00100000');
    assert.equal(option.subsession_id, 'regular');
    assert.equal(option.subsessions.some(item => item.id === EQUITY_24H_SESSION), false);
    const forex = await resolve('EUR/USD', '24h');
    assert.equal(forex.subsession_id, '24h');
    assert.equal(forex.subsessions.some(item => item.id === EQUITY_24H_SESSION), false);
});

test('24H supports exactly 1/5/15/30 minute history and remains hidden when coverage is disabled', async t => {
    const schwabCalls = [], futuCalls = [];
    let enabled = true;
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled, providerEnabled: enabled })),
        request: async path => { schwabCalls.push(path); return json([]); },
        futuRequest: async path => {
            if (path.startsWith('/kline')) futuCalls.push(path);
            return json([]);
        },
    });
    t.after(() => feed.destroy());
    const info = { name: 'AAPL', subsession_id: EQUITY_24H_SESSION };
    for (const resolution of ['1', '5', '15', '30']) {
        const { metadata, bars } = await history(feed, info, resolution);
        assert.deepEqual(bars, []);
        assert.equal(metadata.noData, true);
    }
    assert.equal(schwabCalls.length, 4);
    assert.deepEqual(futuCalls.map(path => new URL(path, 'https://example.test').searchParams.get('resolution')), ['1', '5', '15', '30']);
    assert(futuCalls.every(path => new URL(path, 'https://example.test').searchParams.get('requireHistory') === 'true'));

    enabled = false;
    feed.overlayCachedAt = 0;
    const unavailable = await new Promise((resolve, reject) => feed.resolveSymbol('AAPL', resolve, reject, { session: EQUITY_24H_SESSION }));
    assert.equal(unavailable.subsession_id, 'regular');
    assert.equal(unavailable.subsessions.some(item => item.id === EQUITY_24H_SESSION), false);
    const priorCalls = [schwabCalls.length, futuCalls.length];
    const result = await history(feed, info, '1');
    assert.deepEqual(result.bars, []);
    assert.deepEqual([schwabCalls.length, futuCalls.length], priorCalls, 'disabled full-day coverage cannot masquerade as Schwab-only 24H');
});

test('24H unsupported resolutions return noData without querying either source', async t => {
    const calls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        request: async () => { calls.push('schwab'); return json([]); },
        futuRequest: async path => { if (path.startsWith('/kline')) calls.push('futu'); return json([]); },
    });
    t.after(() => feed.destroy());
    const { metadata, bars } = await history(feed, { name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '10');
    assert.deepEqual(bars, []);
    assert.deepEqual(metadata, { noData: true });
    assert.deepEqual(calls, []);
});

test('24H realtime routes only ET daytime to Schwab and night to Futu, isolated from Night cache', t => {
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async () => new Response('{}'),
    });
    t.after(() => feed.destroy());
    const fullDay = { name: 'AAPL', subsession_id: EQUITY_24H_SESSION };
    const night = { name: 'AAPL', subsession_id: 'night' };
    const beforeOpen = Date.parse('2026-01-17T08:59:00Z'); // 03:59 ET
    feed.seedLatest('AAPL', EQUITY_24H_SESSION, '1', [{ time: beforeOpen - 60_000, open: 100, high: 100, low: 100, close: 100, volume: 5 }]);
    feed.seedLatest('AAPL', 'night', '1', [{ time: beforeOpen - 60_000, open: 200, high: 200, low: 200, close: 200, volume: 9 }]);
    const dayTicks = [], nightTicks = [];
    feed.subscribeBars(fullDay, '1', bar => dayTicks.push(bar), '24h');
    feed.subscribeBars(night, '1', bar => nightTicks.push(bar), 'night');
    const futu = (time, close, volume = 7) => events.dispatchEvent(new CustomEvent('FUTU_KL', { detail: {
        symbol: 'AAPL', resolution: '1', bar: { time: Date.parse(time), open: close, high: close, low: close, close, volume },
    } }));
    const schwab = (time, price, volume) => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: Date.parse(time), content: [{ key: 'AAPL', '3': price, '8': volume }],
    } }));

    futu('2026-01-17T08:59:00Z', 101);
    assert.equal(dayTicks.at(-1).close, 101);
    assert.equal(nightTicks.at(-1).close, 101);
    futu('2026-01-17T09:00:00Z', 102); // 04:00 ET belongs to Schwab, not Futu
    assert.equal(dayTicks.length, 1);
    assert.equal(nightTicks.at(-1).close, 102, 'standalone Night preserves its existing provider semantics');
    schwab('2026-01-17T09:00:00Z', 103, 100);
    schwab('2026-01-17T09:01:00Z', 104, 110);
    assert.equal(dayTicks.at(-1).close, 104);
    assert.equal(dayTicks.at(-1).volume, 10, 'Schwab daily cumulative volume starts a new 04:00 ET trading date');
    const fullDayCache = feed.latestBars.get(`${'AAPL'}\0${EQUITY_24H_SESSION}`).get('1');
    const nightCache = feed.latestBars.get('AAPL\0night').get('1');
    assert.equal(fullDayCache.close, 104);
    assert.equal(nightCache.close, 102);
    const fullDayCount = dayTicks.length;
    schwab('2026-01-18T01:00:00Z', 105, 500); // 20:00 ET belongs to Futu
    assert.equal(dayTicks.length, fullDayCount);
    futu('2026-01-18T01:00:00Z', 106, 3);
    assert.equal(dayTicks.at(-1).close, 106);
    assert.equal(nightTicks.at(-1).close, 106);
    assert.equal(feed.latestBars.get(`AAPL\0${EQUITY_24H_SESSION}`).get('1').close, 106);
    assert.equal(feed.latestBars.get('AAPL\0night').get('1').close, 106);
});

test('24H realtime subscribes both providers once and releases Futu only after the last night-series user', async t => {
    const schwabCalls = [], futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: true },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        request: async (_path, init) => { schwabCalls.push(JSON.parse(init.body)); return new Response('{}'); },
        futuRequest: async (_path, init) => { futuCalls.push(JSON.parse(init.body)); return new Response('{}'); },
    });
    t.after(() => feed.destroy());
    feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '5', () => {}, 'full');
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(schwabCalls.filter(call => call.command === 'ADD').length, 1);
    assert.deepEqual(futuCalls.map(call => call.command), ['ADD']);
    feed.unsubscribeBars('night');
    assert.deepEqual(futuCalls.map(call => call.command), ['ADD'], 'Night unsubscribe must not break the still-active 24H stream');
    feed.unsubscribeBars('full');
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.deepEqual(futuCalls.map(call => call.command), ['ADD', 'UNSUB']);
});

test('first Futu READY before or after ADD and same-generation WS reconnect never duplicate leases', async t => {
    for (const order of ['ready-first', 'add-first']) {
        const events = new EventTarget(), commands = [];
        let generation = 'gateway:1';
        const feed = new Datafeed({
            events, stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            futuRequest: async (_path, init) => {
                const command = JSON.parse(init.body).command;
                commands.push(command);
                return new Response(JSON.stringify({ success: true, applied: true, generation }));
            },
        });
        t.after(() => feed.destroy());
        const ready = value => events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: value } }));
        if (order === 'ready-first') ready(generation);
        feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '5', () => {}, 'lease');
        for (let i = 0; i < 50 && commands.filter(command => command === 'ADD').length < 1; i++) await new Promise(resolve => setTimeout(resolve, 5));
        assert.deepEqual(commands, ['ADD'], `${order}: initial subscription should ADD once`);
        if (order === 'add-first') ready(generation);
        await new Promise(resolve => setTimeout(resolve, 10));
        assert.deepEqual(commands, ['ADD'], `${order}: first READY must not replay an existing lease`);
        events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
        ready(generation);
        await new Promise(resolve => setTimeout(resolve, 15));
        assert.deepEqual(commands, ['ADD'], 'same-generation websocket reconnect must preserve the OpenD lease');
        generation = 'gateway:2';
        events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
        ready(generation);
        for (let i = 0; i < 50 && commands.filter(command => command === 'ADD').length < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
        assert.deepEqual(commands, ['ADD', 'ADD'], 'new backend generation must replay active listeners');
        feed.unsubscribeBars('lease');
        for (let i = 0; i < 50 && commands.at(-1) !== 'UNSUB'; i++) await new Promise(resolve => setTimeout(resolve, 5));
        assert.deepEqual(commands, ['ADD', 'ADD', 'UNSUB']);
    }
});

test('Futu command response from a reset backend invalidates an in-flight old-generation ADD', async t => {
    const events = new EventTarget(), calls = [];
    const firstAdd = Promise.withResolvers(), releaseOldAdd = Promise.withResolvers();
    let addCount = 0, generation = 'gateway:1';
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body).command;
            calls.push(command);
            if (command === 'ADD' && ++addCount === 1) {
                firstAdd.resolve();
                await releaseOldAdd.promise;
                return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:1' }));
            }
            return new Response(JSON.stringify({ success: true, applied: true, generation }));
        },
    });
    t.after(() => feed.destroy());
    const ready = value => events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: value } }));
    ready(generation);
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await firstAdd.promise;
    events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    generation = 'gateway:2';
    ready(generation);
    releaseOldAdd.resolve();
    for (let i = 0; i < 100 && addCount < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(addCount, 2, 'an old ADD response must not mark the new backend generation subscribed');
    assert.deepEqual(calls, ['ADD', 'ADD']);
    feed.unsubscribeBars('night');
});

test('stale Futu READY cannot roll back an ADD-observed generation or duplicate its lease', async t => {
    for (const seedOldReady of [true, false]) {
        const events = new EventTarget(), calls = [];
        let references = 0;
        const feed = new Datafeed({
            events, stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            futuRequest: async (_path, init) => {
                calls.push(JSON.parse(init.body).command);
                references++;
                return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:2' }));
            },
        });
        t.after(() => feed.destroy());
        const ready = generation => events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation } }));
        if (seedOldReady) ready('gateway:1');
        feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
        for (let i = 0; i < 50 && feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'; i++) await new Promise(resolve => setTimeout(resolve, 5));
        assert.equal(feed.futuBackendGeneration, 'gateway:2');
        assert.equal(references, 1);

        // In one case the old READY was already observed before ADD returned;
        // in the other it is the first READY seen, queued behind the HTTP reply.
        ready('gateway:1');
        await new Promise(resolve => setTimeout(resolve, 30));
        assert.equal(feed.futuBackendGeneration, 'gateway:2', 'retired or numerically older READY cannot roll back the gateway');
        assert.deepEqual(calls, ['ADD'], 'stale READY must not replay and inflate the backend reference');
        assert.equal(references, 1);
    }
});

test('Futu commands carry their captured token and retry explicit unapplied responses', async t => {
    const events = new EventTarget(), commands = [];
    let adds = 0;
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body);
            commands.push(command);
            if (command.command === 'ADD' && ++adds === 1) {
                return new Response(JSON.stringify({ success: true, applied: false, generation: 'gateway:2' }));
            }
            return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:2' }));
        },
    });
    t.after(() => feed.destroy());
    events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: 'gateway:1' } }));
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    for (let i = 0; i < 50 && (adds < 2 || feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'); i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(commands.slice(0, 2).map(command => [command.command, command.generation]), [
        ['ADD', 'gateway:1'], ['ADD', 'gateway:2'],
    ]);
    assert.equal(feed.futuSUB.get('AAPL\0' + '5'), 'subscribed');

    feed.unsubscribeBars('night');
    for (let i = 0; i < 50 && !commands.some(command => command.command === 'UNSUB'); i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(commands.at(-1), { command: 'UNSUB', symbol: 'AAPL', resolution: '5', generation: 'gateway:2' });
});

test('bootstrap Futu ADD may omit an unknown token, but later UNSUB is versioned', async t => {
    const events = new EventTarget(), commands = [];
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            commands.push(JSON.parse(init.body));
            return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:2' }));
        },
    });
    t.after(() => feed.destroy());
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    for (let i = 0; i < 50 && feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(commands[0], { command: 'ADD', symbol: 'AAPL', resolution: '5' }, 'the first ADD bootstraps without a generation');
    feed.unsubscribeBars('night');
    for (let i = 0; i < 50 && commands.length < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(commands[1], { command: 'UNSUB', symbol: 'AAPL', resolution: '5', generation: 'gateway:2' });
});

test('legacy unknown-token UNSUB cannot release another tab after gateway reset', async t => {
    let generation = 'gateway:1', references = 0;
    const commands = [];
    const makeTab = name => {
        const events = new EventTarget();
        const feed = new Datafeed({
            events, stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            futuRequest: async (_path, init) => {
                const command = JSON.parse(init.body);
                commands.push([name, command]);
                if (command.command === 'ADD') {
                    references++;
                    // Simulate the old ADD response that did not expose its lease generation.
                    return new Response(JSON.stringify(command.generation
                        ? { success: true, applied: true, generation }
                        : { success: true, applied: true }));
                }
                if (!command.generation || command.generation !== generation) {
                    return new Response(JSON.stringify({ success: true, applied: false, generation }));
                }
                references--;
                return new Response(JSON.stringify({ success: true, applied: true, generation }));
            },
        });
        t.after(() => feed.destroy());
        return { events, feed };
    };
    const tabA = makeTab('A');
    tabA.feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'A');
    for (let i = 0; i < 50 && tabA.feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 1);
    assert.equal(tabA.feed.futuBackendGeneration, undefined, 'the old ADD response left tab A without a known token');
    assert.deepEqual(commands[0], ['A', { command: 'ADD', symbol: 'AAPL', resolution: '5' }]);

    generation = 'gateway:2';
    references = 0; // Gateway reset clears generation-1 leases.
    const tabB = makeTab('B');
    tabB.events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation } }));
    tabB.feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'B');
    for (let i = 0; i < 50 && tabB.feed.futuSUB.get('AAPL\0' + '5') !== 'subscribed'; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 1);
    assert.deepEqual(commands[1], ['B', { command: 'ADD', symbol: 'AAPL', resolution: '5', generation }]);

    tabA.feed.unsubscribeBars('A');
    for (let i = 0; i < 50 && commands.length < 3; i++) await new Promise(resolve => setTimeout(resolve, 5));
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.deepEqual(commands[2], ['A', { command: 'UNSUB', symbol: 'AAPL', resolution: '5' }]);
    assert.equal(commands.length, 3, 'tab A must not retry using the generation learned from the unapplied response');
    assert.equal(references, 1, 'unknown-token UNSUB must not decrement tab B generation-2 reference');
    assert.equal(tabB.feed.futuSUB.get('AAPL\0' + '5'), 'subscribed');
});

test('generation-less Futu ADD response is retried if its request crossed a backend reset', async t => {
    const events = new EventTarget(), firstAdd = Promise.withResolvers(), releaseOldAdd = Promise.withResolvers();
    let adds = 0;
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body).command;
            if (command !== 'ADD') return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:2' }));
            if (++adds === 1) {
                firstAdd.resolve();
                await releaseOldAdd.promise;
                return new Response(JSON.stringify({ success: true, applied: true }));
            }
            return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:2' }));
        },
    });
    t.after(() => feed.destroy());
    const ready = generation => events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation } }));
    ready('gateway:1');
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await firstAdd.promise;
    ready('gateway:2');
    releaseOldAdd.resolve();
    for (let i = 0; i < 50 && adds < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(adds, 2, 'unknown-generation stale response must not mark the new backend subscribed');
    assert.equal(feed.futuBackendGeneration, 'gateway:2');
    assert.equal(feed.futuSUB.get('AAPL\0' + '5'), 'subscribed');
    feed.unsubscribeBars('night');
});

test('separate tabs retain independent Futu leases across same-generation reconnect and replay after reset', async t => {
    let generation = 'gateway:1', references = 0;
    const calls = [];
    const makeTab = name => {
        const events = new EventTarget();
        const feed = new Datafeed({
            events, stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            futuRequest: async (_path, init) => {
                const command = JSON.parse(init.body).command;
                calls.push([name, command]);
                references += command === 'ADD' ? 1 : -1;
                return new Response(JSON.stringify({ success: true, applied: true, generation }));
            },
        });
        return { events, feed };
    };
    const tabA = makeTab('A'), tabB = makeTab('B');
    t.after(() => { tabA.feed.destroy(); tabB.feed.destroy(); });
    const ready = (tab, value) => tab.events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: value } }));
    ready(tabA, generation); ready(tabB, generation);
    tabA.feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '5', () => {}, 'A');
    tabB.feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'B');
    for (let i = 0; i < 50 && references < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 2, 'each tab must hold one independent backend reference');

    tabA.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    tabB.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    ready(tabA, generation); ready(tabB, generation);
    await new Promise(resolve => setTimeout(resolve, 15));
    assert.equal(references, 2, 'same-generation WS reconnect must not increment references');
    assert.equal(calls.filter(([, command]) => command === 'ADD').length, 2);

    generation = 'gateway:2';
    references = 0; // backend reset clears all gateway leases
    tabA.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    tabB.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    ready(tabA, generation); ready(tabB, generation);
    for (let i = 0; i < 50 && calls.filter(([, command]) => command === 'ADD').length < 4; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 2, 'both tabs must restore only their own lease after backend reset');
    tabA.feed.unsubscribeBars('A');
    for (let i = 0; i < 50 && references !== 1; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 1, 'one tab leaving must not release the other tab lease');
    tabB.feed.unsubscribeBars('B');
    for (let i = 0; i < 50 && references !== 0; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 0);
});

test('new gateway generation replays every tab lease without clearing another tab reference', async t => {
    let generation = 'gateway:1', references = 0;
    const commands = [];
    const makeTab = name => {
        const events = new EventTarget();
        const feed = new Datafeed({
            events, stream: { ready: false },
            overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
            futuRequest: async (_path, init) => {
                const command = JSON.parse(init.body).command;
                commands.push([name, command]);
                references += command === 'ADD' ? 1 : -1;
                return new Response(JSON.stringify({ success: true, applied: true, generation }));
            },
        });
        return { events, feed };
    };
    const a = makeTab('a'), b = makeTab('b');
    t.after(() => { a.feed.destroy(); b.feed.destroy(); });
    const ready = (tab, value) => tab.events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: value } }));
    ready(a, generation); ready(b, generation);
    a.feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '5', () => {}, 'a');
    b.feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'b');
    for (let i = 0; i < 50 && references !== 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 2);

    a.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    b.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    ready(a, generation); ready(b, generation);
    await new Promise(resolve => setTimeout(resolve, 15));
    assert.equal(commands.filter(([, command]) => command === 'ADD').length, 2, 'same-generation reconnect must not inflate refcounts');
    assert.equal(references, 2);

    generation = 'gateway:2';
    references = 0; // The backend reset itself clears all gateway leases.
    a.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    b.events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    ready(a, generation); ready(b, generation);
    for (let i = 0; i < 50 && commands.filter(([, command]) => command === 'ADD').length < 4; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 2, 'each tab must restore exactly its own desired lease');
    a.feed.unsubscribeBars('a');
    for (let i = 0; i < 50 && references !== 1; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 1, 'one tab cannot release the other tab lease');
    b.feed.unsubscribeBars('b');
    for (let i = 0; i < 50 && references !== 0; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(references, 0);
});

test('pending old-generation ADD is replayed, not mistaken for a current backend lease', async t => {
    const events = new EventTarget(), calls = [];
    const addStarted = Promise.withResolvers(), releaseOldAdd = Promise.withResolvers();
    let generation = 'gateway:1', adds = 0;
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body).command;
            calls.push(command);
            if (command === 'ADD' && ++adds === 1) {
                addStarted.resolve();
                await releaseOldAdd.promise;
                return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:1' }));
            }
            return new Response(JSON.stringify({ success: true, applied: true, generation }));
        },
    });
    t.after(() => feed.destroy());
    const ready = value => events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: value } }));
    ready(generation);
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await addStarted.promise;
    events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    generation = 'gateway:2';
    ready(generation);
    releaseOldAdd.resolve();
    for (let i = 0; i < 50 && adds < 2; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.equal(adds, 2, 'old generation ADD response must be discarded and resubscribed on the reset backend');
    assert.deepEqual(calls, ['ADD', 'ADD']);
    feed.unsubscribeBars('night');
    for (let i = 0; i < 50 && calls.at(-1) !== 'UNSUB'; i++) await new Promise(resolve => setTimeout(resolve, 5));
    assert.deepEqual(calls, ['ADD', 'ADD', 'UNSUB']);
});

test('leaving 24H while Futu ADD is pending sends serialized UNSUB after ADD succeeds', async t => {
    const started = Promise.withResolvers(), releaseAdd = Promise.withResolvers(), unsubscribed = Promise.withResolvers();
    const calls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body).command;
            calls.push(command);
            if (command === 'ADD') {
                started.resolve();
                await releaseAdd.promise;
            } else if (command === 'UNSUB') unsubscribed.resolve();
            return new Response('{}');
        },
    });
    t.after(() => feed.destroy());
    feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '5', () => {}, 'full');
    await started.promise;
    feed.unsubscribeBars('full');
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls, ['ADD'], 'UNSUB must not overtake a pending ADD');
    releaseAdd.resolve();
    await unsubscribed.promise;
    assert.deepEqual(calls, ['ADD', 'UNSUB'], 'successful orphan ADD must be compensated exactly once');
});

test('leaving Night while Futu ADD is pending is also compensated in order', async t => {
    const started = Promise.withResolvers(), releaseAdd = Promise.withResolvers(), unsubscribed = Promise.withResolvers();
    const calls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        futuRequest: async (_path, init) => {
            const command = JSON.parse(init.body).command;
            calls.push(command);
            if (command === 'ADD') { started.resolve(); await releaseAdd.promise; }
            else if (command === 'UNSUB') unsubscribed.resolve();
            return new Response('{}');
        },
    });
    t.after(() => feed.destroy());
    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'night');
    await started.promise;
    feed.unsubscribeBars('night');
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(calls, ['ADD'], 'Night UNSUB must wait for the in-flight ADD');
    releaseAdd.resolve();
    await unsubscribed.promise;
    assert.deepEqual(calls, ['ADD', 'UNSUB']);
});

test('24H history started before Futu disconnect is discarded and reconnect resets its series', async t => {
    const events = new EventTarget(), pending = Promise.withResolvers();
    let resets = 0;
    const feed = new Datafeed({
        events, stream: { ready: false },
        overnightRequest: async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true })),
        request: async () => json([candle]),
        futuRequest: async path => path.startsWith('/kline')
            ? { ok: true, json: () => pending.promise }
            : new Response('{}'),
    });
    t.after(() => feed.destroy());
    const waiting = history(feed, { name: 'AAPL', subsession_id: EQUITY_24H_SESSION });
    const rejected = assert.rejects(waiting, /连接已变更/);
    await new Promise(setImmediate);
    events.dispatchEvent(new Event('FUTU_STREAM_CLOSED'));
    pending.resolve({ candles: [] });
    await rejected;
    assert.equal(feed.latestBars.size, 0);
    feed.seedLatest('AAPL', EQUITY_24H_SESSION, '1', [candle]);
    feed.subscribeBars({ name: 'AAPL', subsession_id: EQUITY_24H_SESSION }, '1', () => {}, 'full', () => resets++);
    events.dispatchEvent(new Event('FUTU_STREAM_READY'));
    assert.equal(resets, 1);
    assert.equal(feed.latestBars.has(`AAPL\0${EQUITY_24H_SESSION}`), false);
});
