import test from 'node:test';
import assert from 'node:assert/strict';
import { loadPreferences } from '../chart/preferences.js';

function fixture(initial = {}) {
    const entries = new Map(Object.entries(initial)), calls = [], reports = [];
    let reject, hold;
    const request = async (url, init = {}) => {
        if (url.endsWith('/csrf')) return Response.json({ token: 'token' });
        if (init.method !== 'PUT') {
            const filtered = new URL(url, 'http://fixture').searchParams.get('includeLayout') === 'false';
            return Response.json({ entries: Object.fromEntries([...entries].filter(([name]) => !filtered || name !== 'layout')) });
        }
        const draft = JSON.parse(init.body);
        calls.push(draft);
        if (reject) return Response.json({ message: 'save failed' }, { status: reject });
        if (hold) { const waiting = hold; hold = null; await waiting; }
        const old = entries.get(draft.name);
        if (old && old.revision !== draft.revision && !(old.writer === draft.writer && old.base === draft.revision)) return Response.json({ message: 'conflict' }, { status: 409 });
        if (!old || old.sequence < draft.sequence || old.writer !== draft.writer) entries.set(draft.name, {
            revision: (old?.revision || 0) + 1, value: draft.value, writer: draft.writer, base: draft.revision, sequence: draft.sequence,
        });
        const current = entries.get(draft.name);
        return Response.json({ revision: current.revision, sequence: current.sequence });
    };
    return { request, entries, calls, reports, options: { request, report: error => reports.push(error) },
        reject: status => { reject = status; }, hold: promise => { hold = promise; },
    };
}

test('chart and TradingView settings save to the workspace and restore in another browser', async () => {
    const chart = { symbol: 'MSFT', resolution: '15', session: 'extended' };
    const f = fixture();
    const store = await loadPreferences(f.options);
    assert.equal(f.calls.length, 0, 'loading settings must not write defaults');
    store.set('chart', chart);
    store.settingsAdapter().setValue('chart.favoriteDrawings', '["LineToolTrendLine"]');
    assert.deepEqual(store.get('chart'), chart);
    assert.equal(store.settingsAdapter().initialSettings['chart.favoriteDrawings'], '["LineToolTrendLine"]');
    store.settingsAdapter().setValue('dateFormat', 'yyyy-MM-dd');
    store.set('account', 'B');
    await store.flush();
    const other = await loadPreferences({ request: f.request });
    assert.deepEqual(other.get('chart'), chart);
    assert.equal(other.get('account'), 'B');
    assert.equal(other.settingsAdapter().initialSettings.dateFormat, 'yyyy-MM-dd');
});

test('removed TradingView settings stay removed when a new browser loads them', async () => {
    const f = fixture({ 'tv:dateFormat': { revision: 2, value: 'yyyy-MM-dd' }, chart: { revision: 3, value: { symbol: 'AAPL', resolution: '1D', session: 'regular' } } });
    const store = await loadPreferences(f.options);
    store.settingsAdapter().removeValue('dateFormat');
    await store.flush();
    const other = await loadPreferences(f.options);
    assert.equal(other.settingsAdapter().initialSettings.dateFormat, undefined);
    assert.equal(f.entries.get('tv:dateFormat').value, null);
    assert.equal(store.get('chart').symbol, 'AAPL');
});

test('loading settings excludes the legacy layout without changing stored data', async () => {
    const layout = { revision: 7, value: { charts: [{}], opaque: 'x'.repeat(100000) } };
    const f = fixture({ layout, 'tv:dateFormat': { revision: 2, value: 'yyyy-MM-dd' } });
    const requests = [];
    const store = await loadPreferences({ request: (url, init) => {
        requests.push(url);
        return f.request(url, init);
    } });
    assert(requests.includes('/api/modules/investment/preferences?includeLayout=false'));
    assert.equal(store.has('layout'), false);
    assert.equal(store.settingsAdapter().initialSettings.dateFormat, 'yyyy-MM-dd');
    assert.deepEqual(f.entries.get('layout'), layout);
    assert.equal(f.calls.length, 0);
});

test('repeated leave shares an in-flight save and does not replay an acknowledged draft', async () => {
    const f = fixture();
    const store = await loadPreferences(f.options);
    const held = Promise.withResolvers();
    f.hold(held.promise);
    store.set('account', 'A');
    const flushing = store.flush();
    store.leave();
    store.leave();
    assert.equal(f.calls.length, 1);
    held.resolve();
    await flushing;
    store.leave();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(f.calls.length, 1);
    assert.equal(f.entries.get('account').value, 'A');
});

test('leaving sends a newer selection without waiting for an old save response', async () => {
    const f = fixture();
    const store = await loadPreferences(f.options);
    const held = Promise.withResolvers();
    f.hold(held.promise);
    store.set('account', 'A');
    const flushing = store.flush();
    store.set('account', 'B');
    store.leave();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(f.entries.get('account').value, 'B');
    held.resolve();
    await flushing;
    assert.equal(f.entries.get('account').value, 'B');
    assert.deepEqual(f.calls.slice(0, 2).map(call => call.sequence), [1, 2]);
});

test('save errors retain memory edits for retry and stale windows cannot replace server settings', async () => {
    const f = fixture();
    const first = await loadPreferences(f.options);
    const second = await loadPreferences(f.options);
    first.set('account', 'A');
    await first.flush();
    second.set('account', 'B');
    await second.flush();
    assert.equal(f.entries.get('account').value, 'A');
    assert.equal(f.reports.at(-1).conflict, true);
    f.reject(503);
    first.settingsAdapter().setValue('dateFormat', 'yyyy-MM-dd');
    await first.flush();
    assert.equal(first.settingsAdapter().initialSettings.dateFormat, 'yyyy-MM-dd');
    assert.equal(f.reports.at(-1).message, 'save failed');
    f.reject(null);
    await first.retry();
    assert.equal(f.entries.get('tv:dateFormat').value, 'yyyy-MM-dd');
    assert.equal(f.reports.at(-1), null);
});

test('a new edit retries after a transient failure and still respects a conflict', async () => {
    const f = fixture();
    const store = await loadPreferences(f.options);
    f.reject(502);
    store.set('account', 'A');
    await store.flush();
    assert(f.reports.at(-1));
    f.reject(null);
    store.set('account', 'B');
    store.leave();
    await store.flush();
    assert.equal(f.entries.get('account').value, 'B');
    assert.equal(f.reports.at(-1), null);

    f.reject(409);
    store.set('account', 'C');
    await store.flush();
    const failedCalls = f.calls.length;
    store.set('account', 'D');
    store.leave();
    await store.flush();
    assert.equal(f.calls.length, failedCalls);
    assert.equal(f.reports.at(-1).conflict, true);
    assert.equal(f.entries.get('account').value, 'B');
});

test('malformed server responses never start a chart with defaults', async () => {
    for (const body of [{}, { entries: { chart: { revision: 1, value: { symbol: 'MSFT', resolution: '10', session: 'regular' } } } }, { entries: { layout: { revision: 1, value: {} } } }]) {
        await assert.rejects(loadPreferences({ request: async url => Response.json(url.endsWith('/csrf') ? { token: 'token' } : body) }), /响应无效/);
    }
});

test('a non-JSON proxy error page keeps its HTTP status in the save error', async () => {
    const f = fixture();
    const request = async (url, init = {}) => init.method === 'PUT'
        ? new Response('<html><body><h1>500 Internal Server Error</h1></body></html>', { status: 500, headers: { 'Content-Type': 'text/html' } })
        : f.request(url, init);
    const preferences = await loadPreferences({ ...f.options, request });
    preferences.set('chart', { symbol: 'DRAM', resolution: '15', session: 'regular' });
    await preferences.flush();
    assert.equal(f.reports.at(-1).message, '无法保存投资设置（HTTP 500），请重试。');
});
