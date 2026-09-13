import test from 'node:test';
import assert from 'node:assert/strict';
import Datafeed from '../chart/datafeed.js';
import { isOvernightET, seriesKey } from '../chart/futu.js';

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
        futuRequest: async (path) => {
            if (String(path).startsWith('/kline')) return json([{ time: nightTime, open: 200, high: 200, low: 200, close: 200, volume: 5 }]);
            if (String(path) === '') return new Response(JSON.stringify({ enabled: true }));
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
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: async () => json([candle]),
        futuRequest: async (path) => { futuCalls.push(path); return new Response(JSON.stringify({ enabled: true })); },
    });
    t.after(() => feed.destroy());
    const { metadata } = await history(feed, { name: 'AAPL', subsession_id: 'night' }, '10');
    assert.equal(metadata.noData, true);
    assert.deepEqual(futuCalls, ['']);
});

test('24h 10-minute stays on Schwab', async (t) => {
    const futuCalls = [];
    const feed = new Datafeed({
        events: new EventTarget(), stream: { ready: false },
        request: async () => json([candle]),
        futuRequest: async (path) => { futuCalls.push(path); return new Response(JSON.stringify({ enabled: true })); },
    });
    t.after(() => feed.destroy());
    const { bars } = await history(feed, { name: 'AAPL', subsession_id: '24h' }, '10');
    assert.equal(bars.length, 1);
    assert.deepEqual(futuCalls, []);
});

test('isOvernightET uses America/New_York including DST', () => {
    assert.equal(isOvernightET(Date.parse('2026-01-15T01:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-01-15T09:00:00Z')), false);
    assert.equal(isOvernightET(Date.parse('2026-11-01T05:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-11-01T06:00:00Z')), true);
    assert.equal(isOvernightET(Date.parse('2026-03-08T08:00:00Z')), false);
});
