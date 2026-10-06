import test from 'node:test';
import assert from 'node:assert/strict';
import { bindChartLayout } from '../chart/chart-layout.js';

test('chart edits and disposal save indicators and drawings and release the same subscription', () => {
    const writes = [], subscriptions = new Map();
    let layout = { charts: [{ panes: [{ sources: [{ type: 'study' }, { type: 'LineToolTrendLine' }] }] }] }, captured = 0;
    const widget = {
        save(callback, options) { assert.equal(options.includeDrawings, true); callback(structuredClone(layout)); },
        subscribe: (event, callback) => subscriptions.set(event, callback),
        unsubscribe(event, callback) { assert.equal(subscriptions.get(event), callback); subscriptions.delete(event); },
    };
    const sync = bindChartLayout(widget, { set: (name, value) => writes.push({ name, value }) }, { beforeCapture: () => captured++ });
    subscriptions.get('onAutoSaveNeeded')();
    assert.equal(writes[0].value.charts[0].panes[0].sources.length, 2);
    layout = { charts: [{ panes: [] }] };
    sync.schedule();
    sync.dispose();
    assert.deepEqual(writes.at(-1), { name: 'layout', value: layout });
    assert.equal(captured, 2);
    assert.equal(subscriptions.size, 0);
    sync.capture();
    assert.equal(writes.length, 2);
});

test('late layout snapshots cannot replace a newer capture and disposal awaits its snapshot', async () => {
    const callbacks = [], writes = [], events = new Map();
    const sync = bindChartLayout({
        save: callback => { callbacks.push(callback); },
        subscribe: (name, callback) => events.set(name, callback), unsubscribe: name => events.delete(name),
    }, { set: (_name, layout) => writes.push(layout) });
    const older = sync.capture();
    const newer = sync.capture();
    callbacks[1]({ charts: ['newer'] });
    callbacks[0]({ charts: ['older'] });
    await Promise.all([older, newer]);
    assert.deepEqual(writes, [{ charts: ['newer'] }]);
    const final = sync.dispose();
    let completed = false;
    final.then(() => { completed = true; });
    await Promise.resolve();
    assert.equal(completed, false);
    callbacks[2]({ charts: ['last edit'] });
    await final;
    assert.deepEqual(writes.at(-1), { charts: ['last edit'] });
    assert.equal(events.size, 0);
});
