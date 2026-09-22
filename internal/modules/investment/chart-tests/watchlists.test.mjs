import test from 'node:test';
import assert from 'node:assert/strict';
import { bindWatchlists, loadWatchlists } from '../chart/watchlists.js';

const local = { lists: [{ id: 'local', title: '本地自选', symbols: ['AAPL'] }], activeId: 'local' };
const saved = { lists: [{ id: 'server', title: '长期关注', symbols: ['###科技', 'MSFT', 'AAPL'] }, { id: 'empty', title: '空表', symbols: [] }], activeId: 'empty' };
function fixture(state = local) {
    let current = structuredClone(state);
    const events = {};
    const emit = name => { for (const callback of events[name]) callback(); };
    const api = {
        getAllLists: () => Object.fromEntries(current.lists.map(list => [list.id, list])),
        getActiveListId: () => current.activeId,
        createList(title, symbols, id) {
            const existing = current.lists.find(list => list.id === id);
            emit('onListAdded');
            queueMicrotask(() => {
                if (existing) existing.symbols = symbols;
                else current.lists.push({ id, title, symbols });
            });
        },
        renameList(id, title) { current.lists.find(list => list.id === id).title = title; emit('onListRenamed'); },
        updateList(id, symbols) { current.lists.find(list => list.id === id).symbols = symbols; emit('onListChanged'); },
        deleteList(id) { emit('onListRemoved'); queueMicrotask(() => { current.lists = current.lists.filter(list => list.id !== id); }); },
        setActiveList(id) { current.activeId = id; emit('onActiveListChanged'); },
    };
    for (const name of ['onListAdded', 'onListChanged', 'onListRemoved', 'onListRenamed', 'onActiveListChanged']) {
        events[name] = new Set();
        api[name] = () => ({ subscribe: (_, callback) => events[name].add(callback), unsubscribe: (_, callback) => events[name].delete(callback) });
    }
    return { api, widget: { watchList: async () => api }, state: () => structuredClone(current), events };
}

test('workspace lists replace local defaults without saving restoration events or reviving an empty list', async t => {
    const chart = fixture(), writes = [];
    const sync = await bindWatchlists(chart.widget, { initial: { revision: 3, state: saved }, save: async (...args) => { writes.push(args); return 4; } });
    t.after(() => sync.dispose());
    assert.deepEqual(chart.state(), saved);
    await sync.flush();
    assert.deepEqual(writes, []);
    chart.api.updateList('server', []);
    await sync.flush();
    assert.equal(writes[0][0], 3);
    assert.deepEqual(writes[0][1].lists.map(list => list.symbols), [[], []]);
});

test('first use imports existing browser lists, including order and sections', async t => {
    const chart = fixture(saved), writes = [];
    const sync = await bindWatchlists(chart.widget, { initial: { revision: 0, state: null }, save: async (revision, state) => { writes.push({ revision, state }); return 1; } });
    t.after(() => sync.dispose());
    await sync.flush();
    assert.deepEqual(writes, [{ revision: 0, state: saved }]);
});

test('saves are serialized and edits made during a slow save keep the latest state', async t => {
    const chart = fixture(), first = Promise.withResolvers(), writes = [];
    const sync = await bindWatchlists(chart.widget, {
        initial: { revision: 1, state: local },
        save: (revision, state) => { writes.push({ revision, state }); return writes.length === 1 ? first.promise : Promise.resolve(revision + 1); },
    });
    t.after(() => sync.dispose());
    chart.api.updateList('local', ['MSFT']);
    const flushing = sync.flush();
    await new Promise(resolve => setTimeout(resolve, 0));
    chart.api.updateList('local', ['AAPL']);
    chart.api.renameList('local', '改名');
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(writes.length, 1);
    first.resolve(2);
    await flushing;
    assert.equal(writes.length, 2);
    assert.equal(writes[1].revision, 2);
    assert.deepEqual(writes[1].state.lists, [{ id: 'local', title: '改名', symbols: ['AAPL'] }]);
});

test('failed saves retain subsequent edits for retry and report recovery', async t => {
    const chart = fixture(), reports = [], writes = [];
    let offline = true;
    const sync = await bindWatchlists(chart.widget, {
        initial: { revision: 5, state: local },
        save: async (revision, state) => {
            writes.push({ revision, state });
            if (offline) throw new Error('offline');
            return revision + 1;
        },
    }, error => reports.push(error?.message || 'saved'));
    t.after(() => sync.dispose());
    chart.api.updateList('local', ['MSFT']);
    await sync.flush();
    chart.api.updateList('local', []);
    await sync.flush();
    assert.equal(writes.length, 1);
    offline = false;
    await sync.retry();
    assert.deepEqual(writes[1], { revision: 5, state: { lists: [{ id: 'local', title: '本地自选', symbols: [] }], activeId: 'local' } });
    assert.deepEqual(reports, ['offline', 'saved']);
});

test('a version conflict cannot be retried into overwriting another device', async () => {
    const chart = fixture();
    let writes = 0;
    const sync = await bindWatchlists(chart.widget, {
        initial: { revision: 1, state: local },
        save: async () => { writes++; throw Object.assign(new Error('conflict'), { conflict: true }); },
    });
    chart.api.updateList('local', []);
    await sync.flush();
    await sync.retry();
    sync.dispose();
    chart.api.renameList('local', 'after dispose');
    await sync.flush();
    assert.equal(writes, 1);
    assert(Object.values(chart.events).every(listeners => listeners.size === 0));
});

test('watchlist requests refresh expired CSRF and preserve the same versioned payload', async () => {
    const calls = [];
    let tokenCalls = 0, writes = 0;
    const store = await loadWatchlists(async (url, init) => {
        calls.push({ url, init });
        if (url.endsWith('/csrf')) return Response.json({ token: ++tokenCalls === 1 ? 'old' : 'new' });
        if (init.method !== 'PUT') return Response.json({ revision: 2, state: saved });
        return ++writes === 1 ? Response.json({ code: 'csrf_failed' }, { status: 400 }) : Response.json({ revision: 3, sequence: 1 });
    });
    assert.equal(await store.save(2, saved), 3);
    const sent = calls.filter(call => call.init.method === 'PUT');
    assert.equal(sent[0].init.body, sent[1].init.body);
    assert.equal(sent[1].init.headers['X-CSRF-Token'], 'new');
    assert.equal(sent[1].init.keepalive, true);
});

test('failed initial reads do not become empty saved state', async () => {
    await assert.rejects(loadWatchlists(async () => new Response('', { status: 503 })), /无法读取/);
});

test('removing an inactive list saves the state updated after the library event', async t => {
    const chart = fixture(saved), writes = [];
    const sync = await bindWatchlists(chart.widget, { initial: { revision: 1, state: saved }, save: async (_, state) => { writes.push(state); return 2; } });
    t.after(() => sync.dispose());
    chart.api.deleteList('server');
    await sync.flush();
    assert.deepEqual(writes[0], { lists: [saved.lists[1]], activeId: 'empty' });
});

test('leaving during a save submits the newest state without waiting for the old response', async () => {
    const chart = fixture(), first = Promise.withResolvers(), writes = [];
    const sync = await bindWatchlists(chart.widget, {
        initial: { revision: 1, state: local },
        save: (revision, state) => { writes.push(state); return writes.length === 1 ? first.promise : Promise.resolve(revision + 1); },
    });
    chart.api.updateList('local', ['GOOG']);
    const flushing = sync.flush();
    await new Promise(resolve => setTimeout(resolve, 0));
    chart.api.updateList('local', ['NVDA']);
    sync.dispose();
    assert.deepEqual(writes.map(state => state.lists[0].symbols), [['GOOG'], ['NVDA']]);
    first.resolve(2);
    await flushing;
});

test('refresh replays the latest staged draft before restoring, and old responses cannot erase it', async () => {
    const values = new Map();
    const storage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
    const old = Promise.withResolvers();
    let state = local, puts = 0;
    const request = async (url, init) => {
        if (url.endsWith('/csrf')) return Response.json({ token: 'csrf' });
        if (init.method !== 'PUT') return Response.json({ revision: puts, state });
        const input = JSON.parse(init.body);
        state = input.state;
        if (++puts === 1) return old.promise;
        return Response.json({ revision: puts, sequence: input.sequence });
    };
    const store = await loadWatchlists(request, storage);
    const saving = store.save(0, local);
    store.stage(saved);
    old.resolve(Response.json({ revision: 1, sequence: 1 }));
    await saving;
    assert.equal(values.size, 1, 'the old response must retain the newer pending snapshot');
    const reloaded = await loadWatchlists(request, storage);
    assert.deepEqual(reloaded.initial.state, saved);
    assert.equal(puts, 2, 'the draft must be submitted before the chart is restored');
    assert.equal(values.size, 0);
});

test('a rejected draft can be edited after refresh and a conflict never acquires the newer remote base', async t => {
    for (const status of [400, 409, 503]) {
        let draft = JSON.stringify({ revision: 0, writer: 'old-tab', sequence: 2, state: saved });
        const storage = { getItem: () => draft, setItem: (_, value) => { draft = value; }, removeItem: () => { draft = null; } };
        const store = await loadWatchlists(async (url, init) => {
            if (url.endsWith('/csrf')) return Response.json({ token: 'csrf' });
            if (init.method !== 'PUT') return Response.json({ revision: 5, state: local });
            return Response.json({ message: 'save rejected' }, { status });
        }, storage);
        assert.deepEqual(store.initial.state, saved);
        const chart = fixture();
        const sync = await bindWatchlists(chart.widget, store);
        t.after(() => sync.dispose());
        chart.api.updateList('empty', ['NVDA']);
        await sync.flush();
        const pending = JSON.parse(draft);
        assert.equal(pending.revision, 0, 'editing a rejected draft must not rebase onto the remote version');
        assert.deepEqual(pending.state.lists[1].symbols, ['NVDA']);
        sync.dispose({ discard: true });
        store.discardDraft();
        sync.dispose();
        assert.equal(draft, null, 'choosing remote state must not recreate the discarded draft during pagehide');
    }
});
