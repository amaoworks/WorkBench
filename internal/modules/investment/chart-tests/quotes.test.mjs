import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import {
    mergeQuoteValues,
    quoteData,
    schwabRestQuoteValues,
    schwabStreamQuoteValues,
} from '../chart/datafeed-quotes.js';

const regularTime = Date.parse('2026-01-15T15:00:00Z'); // 10:00 ET
const premarketTime = Date.parse('2026-01-15T13:00:00Z'); // 08:00 ET
const nightTime = Date.parse('2026-01-15T02:00:00Z'); // 21:00 ET
const enabledOvernight = async () => new Response(JSON.stringify({ enabled: true, providerEnabled: true }));
const waitFor = async predicate => {
    for (let attempt = 0; attempt < 100 && !predicate(); attempt++) {
        await new Promise(resolve => setTimeout(resolve, 5));
    }
    assert(predicate(), 'condition was not reached before timeout');
};
const getQuotes = (feed, symbols) => new Promise((resolve, reject) => feed.getQuotes(symbols, resolve, reject));
const restEquity = (symbol, price = 100) => ({
    assetMainType: 'EQUITY',
    quote: {
        lastPrice: price, askPrice: price + 1, bidPrice: price - 1,
        openPrice: 90, highPrice: 110, lowPrice: 80, closePrice: 85,
        totalVolume: 1234, tradeTime: premarketTime,
    },
    regular: {
        regularMarketLastPrice: price,
        regularMarketNetChange: price - 85,
        regularMarketPercentChange: (price / 85 - 1) * 100,
    },
    reference: { description: symbol, exchangeName: 'NASDAQ' },
});

test('Schwab sparse updates preserve daily OHLC and volume in the published quote', t => {
    const events = new EventTarget();
    const feed = new Datafeed({ events, stream: { ready: false }, now: () => regularTime });
    t.after(() => feed.destroy());
    const updates = [];
    feed.subscribeQuotes(['AAPL'], [], values => updates.push(...values), 'details');
    const emit = fields => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: regularTime, content: [{ key: 'AAPL', ...fields }],
    } }));

    emit({ '3': 101, '8': 1200, '10': 105, '11': 95, '12': 99, '17': 100, '29': 101, '31': 2, '43': 2.02 });
    emit({ '1': 100.5, '2': 101.5 });

    assert.deepEqual(
        Object.fromEntries(['open_price', 'high_price', 'low_price', 'volume'].map(key => [key, updates.at(-1).v[key]])),
        { open_price: 100, high_price: 105, low_price: 95, volume: 1200 },
    );
    assert.equal(updates.at(-1).v.lp, 101);
});

test('night quote uses ntc fields without replacing Schwab daily statistics or inventing a time', () => {
    const schwab = {
        lp: 100, regular_price: 100, ch: 2, chp: 2.04, prev_close_price: 98,
        open_price: 99, high_price: 103, low_price: 97, volume: 5000,
    };
    const merged = mergeQuoteValues(schwab, {
        lp: 100, ch: 0, chp: 0,
        open_price: 200, high_price: 210, low_price: 190, volume: 999,
        time: nightTime / 1000,
    }, { equity: true, overnightEnabled: true, now: nightTime });

    assert.equal(merged.cs, 'night');
    assert.deepEqual(
        Object.fromEntries(['ntc', 'nch', 'nchp', 'ntc_time'].map(key => [key, merged[key]])),
        { ntc: 100, nch: 0, nchp: 0, ntc_time: null },
    );
    assert.deepEqual(
        Object.fromEntries(['open_price', 'high_price', 'low_price', 'volume'].map(key => [key, merged[key]])),
        { open_price: 99, high_price: 103, low_price: 97, volume: 5000 },
    );
    assert.equal(merged.lp, 100, 'the overnight trade is the main price shown by the details widget');
    assert.deepEqual([merged.ch, merged.chp], [0, 0], 'a zero move from the regular price must be retained');
});

test('night quote shows the overnight price with change from the latest regular close', () => {
    const schwab = { lp: 50.52, regular_price: 50.52, ch: 2.19, chp: 4.53, prev_close_price: 48.33, volume: 36996400 };
    const mismatched = mergeQuoteValues(schwab, { lp: 51.98, ch: 9, chp: 99 }, { equity: true, overnightEnabled: true, now: nightTime });
    assert.equal(mismatched.lp, 51.98);
    assert.equal(mismatched.ntc, 51.98);
    assert.equal(mismatched.volume, 36996400);
    assert(Math.abs(mismatched.ch - 1.46) < 1e-9);
    assert(Math.abs(mismatched.chp - (51.98 / 50.52 - 1) * 100) < 1e-9);

    for (const futu of [{ lp: 51.98, ch: 9 }, { lp: 51.98, chp: 99 }, { lp: 51.98 }]) {
        const partial = mergeQuoteValues(schwab, futu, { equity: true, overnightEnabled: true, now: nightTime });
        assert(Math.abs(partial.ch - 1.46) < 1e-9);
        assert(Math.abs(partial.chp - (51.98 / 50.52 - 1) * 100) < 1e-9);
    }

    const previousClose = mergeQuoteValues({ prev_close_price: 48.33 }, { lp: 51.98, ch: 9 }, { equity: true, overnightEnabled: true, now: nightTime });
    assert(Math.abs(previousClose.ch - 3.65) < 1e-9);
    assert(Math.abs(previousClose.chp - (51.98 / 48.33 - 1) * 100) < 1e-9);

    const noBaseline = mergeQuoteValues({}, { lp: 51.98, ch: 9, chp: 99 }, { equity: true, overnightEnabled: true, now: nightTime });
    assert.deepEqual([noBaseline.lp, noBaseline.ch, noBaseline.chp, noBaseline.nch, noBaseline.nchp], [51.98, null, null, 9, 99]);

    const noFutu = mergeQuoteValues(schwab, undefined, { equity: true, overnightEnabled: true, now: nightTime });
    assert.deepEqual([noFutu.lp, noFutu.ch, noFutu.ntc], [50.52, 2.19, null]);

    const regularSession = mergeQuoteValues(schwab, { lp: 51.98, ch: 9, chp: 99 }, { equity: true, overnightEnabled: true, now: regularTime });
    assert.deepEqual([regularSession.lp, regularSession.ch, regularSession.chp, regularSession.ntc], [50.52, 2.19, 4.53, null]);
    const disabled = mergeQuoteValues(schwab, { lp: 51.98, ch: 9, chp: 99 }, { equity: true, overnightEnabled: false, now: nightTime });
    assert.deepEqual([disabled.lp, disabled.ch, disabled.chp, disabled.ntc], [50.52, 2.19, 4.53, null]);
    const nonEquity = mergeQuoteValues(schwab, { lp: 51.98, ch: 9, chp: 99 }, { equity: false, overnightEnabled: true, now: nightTime });
    assert.deepEqual([nonEquity.lp, nonEquity.ch, nonEquity.chp, nonEquity.ntc], [50.52, 2.19, 4.53, undefined]);
});

test('night change falls back to previous close when Schwab only has an extended trade', () => {
    const quotes = [
        schwabRestQuoteValues({ assetMainType: 'EQUITY', quote: { lastPrice: 105, closePrice: 100 } }),
        schwabRestQuoteValues({ assetMainType: 'EQUITY', quote: { mark: 105, closePrice: 100 } }),
        schwabStreamQuoteValues('LEVELONE_EQUITIES', { '3': 105, '12': 100 }, { lp: 105 }),
    ];
    for (const schwab of quotes) {
        assert.equal(schwab.lp, 105, 'the normal quote retains its last-price fallback');
        const merged = mergeQuoteValues(schwab, { lp: 106 }, { equity: true, overnightEnabled: true, now: nightTime });
        assert.equal(merged.lp, 106);
        assert.equal(merged.ch, 6);
        assert(Math.abs(merged.chp - 6) < 1e-9);
        assert.equal(Object.hasOwn(merged, 'regular_price'), false, 'the internal baseline is not a TV quote field');

        delete schwab.prev_close_price;
        const noBaseline = mergeQuoteValues(schwab, { lp: 106 }, { equity: true, overnightEnabled: true, now: nightTime });
        assert.deepEqual([noBaseline.lp, noBaseline.ch, noBaseline.chp], [106, null, null]);
    }
});

test('night change retains the REST regular price across sparse stream updates', async t => {
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false }, now: () => nightTime, overnightRequest: enabledOvernight,
        request: async () => Response.json({ AAPL: {
            assetMainType: 'EQUITY', quote: { lastPrice: 105, closePrice: 100 },
            regular: { regularMarketLastPrice: 104 },
        } }),
    });
    t.after(() => feed.destroy());
    await feed.ensureFutuOverlay();
    await getQuotes(feed, ['AAPL']);
    const updates = [];
    feed.subscribeQuotes(['AAPL'], [], quotes => updates.push(...quotes), 'details');
    events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: { symbol: 'AAPL', quote: { lp: 106 } } }));
    const emit = fields => events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: nightTime, content: [{ key: 'AAPL', ...fields }],
    } }));

    emit({ '3': 105, '12': 100 });
    assert.equal(updates.at(-1).v.ch, 2);
    assert(Math.abs(updates.at(-1).v.chp - (106 / 104 - 1) * 100) < 1e-9);
    assert.equal(Object.hasOwn(updates.at(-1).v, 'regular_price'), false);

    emit({ '29': 103 });
    assert.equal(updates.at(-1).v.ch, 3);
});

test('pre-market change uses the latest regular price and the provider trade time', () => {
    const receiptTime = premarketTime + 30_000;
    const values = schwabStreamQuoteValues('LEVELONE_EQUITIES', {
        '3': 105, '12': 90, '29': 100, '35': premarketTime,
    }, {
        lp: 105, open_price: 90, high_price: 105, low_price: 89, volume: 12,
        rtc_time: receiptTime / 1000,
    });

    assert.equal(values.lp, 100);
    assert.equal(values.rtc, 105);
    assert.equal(values.rch, 5, 'extended change is relative to the latest regular close, not yesterday close');
    assert(Math.abs(values.rchp - 5) < Number.EPSILON * 32);
    assert.equal(values.rtc_time, premarketTime / 1000);
    assert.notEqual(values.rtc_time, receiptTime / 1000);
});

test('missing, null and non-finite inputs stay absent and cannot form a successful quote', () => {
    const rest = schwabRestQuoteValues({
        assetMainType: 'EQUITY',
        quote: { lastPrice: 'NaN', mark: null, openPrice: '', highPrice: Infinity, totalVolume: null },
        regular: { regularMarketLastPrice: undefined, regularMarketNetChange: null },
    });
    assert.deepEqual(rest, {});

    const response = quoteData('AAPL', { lp: NaN, rtc: null, ntc: undefined, bid: null, ask: Infinity });
    assert.equal(response.s, 'error');
    assert.deepEqual(response.v, { errmsg: '暂无行情数据' });
});

test('quote listeners hold a separate Futu BASIC lease without releasing a bar lease', async t => {
    const events = new EventTarget();
    const commands = [];
    const feed = new Datafeed({
        events, stream: { ready: false }, overnightRequest: enabledOvernight,
        futuRequest: async (_path, init) => {
            commands.push(JSON.parse(init.body));
            return new Response(JSON.stringify({ success: true, applied: true }));
        },
    });
    t.after(() => feed.destroy());
    await feed.ensureFutuOverlay();
    events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: 'gateway:1', owner: 'owner:1' } }));

    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'bars');
    feed.subscribeQuotes(['AAPL'], [], () => {}, 'first-quote');
    feed.subscribeQuotes(['AAPL'], [], () => {}, 'second-quote');
    await waitFor(() => feed.futuSUB.get('AAPL\0' + '5') === 'subscribed' && feed.futuSUB.get('AAPL\0') === 'subscribed');
    assert.equal(commands.filter(command => command.command === 'ADD' && command.resolution === '').length, 1);

    feed.unsubscribeQuotes('first-quote');
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(commands.some(command => command.command === 'UNSUB' && command.resolution === ''), false);
    feed.unsubscribeQuotes('second-quote');
    await waitFor(() => commands.some(command => command.command === 'UNSUB' && command.resolution === ''));
    assert.equal(feed.futuSUB.get('AAPL\0' + '5'), 'subscribed');
    assert.equal(commands.some(command => command.command === 'UNSUB' && command.resolution === '5'), false);

    feed.unsubscribeBars('bars');
});

test('all Futu ADD commands including K-line and BASIC wait for stream readiness', async t => {
    const events = new EventTarget();
    const commands = [];
    const futuStream = { ready: false, start() {}, stop() {} };
    const feed = new Datafeed({
        events, stream: { ready: false }, futuStream, overnightRequest: enabledOvernight,
        futuRequest: async (_path, init) => {
            commands.push(JSON.parse(init.body));
            return new Response(JSON.stringify({ success: true, applied: true, generation: 'gateway:1', owner: 'owner:1' }));
        },
    });
    t.after(() => feed.destroy());
    await feed.ensureFutuOverlay();

    feed.subscribeBars({ name: 'AAPL', subsession_id: 'night' }, '5', () => {}, 'bars');
    feed.subscribeQuotes(['AAPL'], [], () => {}, 'details');
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.deepEqual(commands, []);

    events.dispatchEvent(new CustomEvent('FUTU_STREAM_READY', { detail: { generation: 'gateway:1', owner: 'owner:1' } }));
    await waitFor(() => commands.some(command => command.command === 'ADD' && command.resolution === '5') &&
        commands.some(command => command.command === 'ADD' && command.resolution === ''));
    for (const command of commands) {
        assert.equal(command.generation, 'gateway:1');
        assert.equal(command.owner, 'owner:1');
    }
});

test('Futu quotes are ignored without a matching live quote listener', async t => {
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false }, overnightRequest: enabledOvernight, now: () => nightTime,
        futuRequest: async () => new Response(JSON.stringify({ success: true, applied: true })),
    });
    t.after(() => feed.destroy());
    await feed.ensureFutuOverlay();
    const emit = price => events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
        symbol: 'AAPL', quote: { lp: price, ch: price - 100, chp: price - 100 },
    } }));

    emit(101);
    assert.equal(feed.futuOvernight.has('AAPL'), false, 'an unsolicited symbol must not populate the cache');

    feed.subscribeQuotes(['AAPL'], [], () => {}, 'details');
    emit(102);
    assert.equal(feed.futuOvernight.get('AAPL').lp, 102);
    feed.unsubscribeQuotes('details');
    emit(103);
    assert.equal(feed.futuOvernight.has('AAPL'), false, 'a push after the final listener leaves must be ignored');
});

test('a delayed Futu BASIC snapshot cannot overwrite a cached live quote', async t => {
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false }, overnightRequest: enabledOvernight, now: () => nightTime,
        futuRequest: async () => new Response(JSON.stringify({ success: true, applied: true })),
    });
    t.after(() => feed.destroy());
    await feed.ensureFutuOverlay();
    feed.subscribeQuotes(['AAPL'], [], () => {}, 'details');

    events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
        symbol: 'AAPL', quote: { lp: 105, ch: 5, chp: 5 },
    } }));
    events.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
        symbol: 'AAPL', snapshot: true, quote: { lp: 100, ch: 0, chp: 0 },
    } }));

    assert.equal(feed.futuOvernight.get('AAPL').lp, 105);
    assert.equal(feed.quote_cache.get('AAPL').ntc, 105);
});

test('getQuotes cold start batches REST snapshots and returns independent values', async t => {
    const requests = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false }, now: () => regularTime,
        request: async path => {
            const url = new URL(path, 'https://example.test');
            requests.push(url);
            const symbols = url.searchParams.get('symbols').split(',');
            return new Response(JSON.stringify(Object.fromEntries(symbols.map((symbol, index) => [symbol, restEquity(symbol, 100 + index)]))));
        },
    });
    t.after(() => feed.destroy());
    const symbols = Array.from({ length: 51 }, (_, index) => `Q${index}`);
    const quotes = await getQuotes(feed, symbols);

    assert.equal(requests.length, 2);
    assert.deepEqual(requests.map(request => request.searchParams.get('symbols').split(',').length), [50, 1]);
    assert.equal(quotes.length, 51);
    quotes[0].v.lp = -1;
    assert.notEqual(quotes[1].v.lp, -1);
    const refreshed = await getQuotes(feed, [symbols[0]]);
    assert.equal(refreshed[0].v.lp, 100, 'mutating a callback result must not mutate the quote cache');
});

test('a late REST snapshot fills daily fields but cannot replace a live quote', async t => {
    const response = Promise.withResolvers();
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: false }, now: () => regularTime,
        request: async () => response.promise,
    });
    t.after(() => feed.destroy());
    const pending = getQuotes(feed, ['AAPL']);
    await new Promise(setImmediate);
    events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: regularTime,
        content: [{ key: 'AAPL', '3': 150, '29': 150, '31': 10, '43': 7.14 }],
    } }));
    response.resolve(new Response(JSON.stringify({ AAPL: restEquity('AAPL', 100) })));
    const [quote] = await pending;

    assert.equal(quote.v.lp, 150);
    assert.equal(quote.v.ch, 10);
    assert.equal(quote.v.open_price, 90, 'REST may still fill a daily field absent from the live snapshot');
});

test('a reconnect invalidates an old REST request and publishes no stale result', async t => {
    const response = Promise.withResolvers();
    const events = new EventTarget();
    const feed = new Datafeed({
        events, stream: { ready: true }, now: () => regularTime,
        request: async () => response.promise,
    });
    t.after(() => feed.destroy());
    let callbackCalled = false;
    const pending = new Promise(resolve => feed.getQuotes(['AAPL'], () => {
        callbackCalled = true;
        resolve(null);
    }, resolve));
    await new Promise(setImmediate);
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    response.resolve(new Response(JSON.stringify({ AAPL: restEquity('AAPL', 100) })));
    const error = await pending;

    assert.match(String(error), /连接已变更/);
    assert.equal(callbackCalled, false);
    assert.equal(feed.schwabQuotes.has('AAPL'), false);
});

test('quote subscriber callback payloads cannot mutate each other or the cache', t => {
    const events = new EventTarget();
    const feed = new Datafeed({ events, stream: { ready: false }, now: () => regularTime });
    t.after(() => feed.destroy());
    let second;
    feed.subscribeQuotes(['AAPL'], [], updates => { updates[0].v.lp = -1; updates[0].v.open_price = -1; }, 'mutator');
    feed.subscribeQuotes(['AAPL'], [], updates => { second = updates[0]; }, 'observer');
    events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
        service: 'LEVELONE_EQUITIES', timestamp: regularTime,
        content: [{ key: 'AAPL', '3': 101, '12': 99, '17': 100, '29': 101 }],
    } }));

    assert.equal(second.v.lp, 101);
    assert.equal(second.v.open_price, 100);
    assert.equal(feed.quote_cache.get('AAPL').lp, 101);
});
