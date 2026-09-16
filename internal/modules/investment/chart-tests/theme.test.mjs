import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { bindTheme } from '../chart/theme.js';

function deferred() {
    let resolve, reject;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}

function fixture(changeTheme = async () => {}, ready = Promise.resolve()) {
    const calls = [], errors = [], properties = new Map();
    const host = Object.assign(new EventTarget(), {
        parent: {}, location: { origin: 'https://workbench.test' },
        document: { documentElement: { style: { setProperty: (key, value) => properties.set(key, value) } } },
        console: { error: (...args) => errors.push(args) },
    });
    const widget = { chartReady: () => ready, changeTheme: async (theme) => { calls.push(theme); await changeTheme(theme); } };
    const send = (theme, override = {}) => {
        const event = Object.assign(new Event('message'), {
            source: host.parent, origin: host.location.origin, data: { type: 'workbench.theme', theme }, ...override,
        });
        host.dispatchEvent(event);
    };
    return { host, widget, send, calls, errors, properties };
}

test('startup applies the latest theme received while the chart is loading', async () => {
    const ready = deferred();
    const f = fixture(undefined, ready.promise);
    const dispose = bindTheme(f.widget, 'light', f.host);
    f.send('dark'); f.send('light'); f.send('dark');
    await setImmediate();
    assert.deepEqual(f.calls, []);
    ready.resolve();
    await setImmediate();
    assert.deepEqual(f.calls, ['dark']);
    assert.equal(f.properties.get('--terminal-background'), '#131722');
    dispose();
});

test('rapid theme updates finish in order and preserve the latest requested color', async () => {
    const dark = deferred();
    let active = 0, maxActive = 0;
    const f = fixture(async (theme) => {
        maxActive = Math.max(maxActive, ++active);
        if (theme === 'dark') await dark.promise;
        active--;
    });
    const dispose = bindTheme(f.widget, 'light', f.host);
    await setImmediate();
    f.send('dark');
    await setImmediate();
    f.send('light'); f.send('dark'); f.send('light');
    await setImmediate();
    assert.deepEqual(f.calls, ['dark']);
    dark.resolve();
    await setImmediate();
    assert.deepEqual(f.calls, ['dark', 'light']);
    assert.equal(maxActive, 1);
    assert.equal(f.host.document.documentElement.style.colorScheme, 'light');
    dispose();
});

test('only valid theme messages from the same-origin parent are accepted', async () => {
    const f = fixture();
    const dispose = bindTheme(f.widget, 'light', f.host);
    await setImmediate();
    f.send('dark', { origin: 'https://unrelated.test' });
    f.send('dark', { source: {} });
    f.send('system');
    f.send('dark', { data: { type: 'unrelated', theme: 'dark' } });
    f.send('dark', { data: null });
    await setImmediate();
    assert.deepEqual(f.calls, []);
    dispose();
});

test('disposing before readiness prevents queued and later theme changes', async () => {
    const ready = deferred();
    const f = fixture(undefined, ready.promise);
    const dispose = bindTheme(f.widget, 'light', f.host);
    f.send('dark');
    dispose();
    ready.resolve();
    f.send('light');
    await setImmediate();
    assert.deepEqual(f.calls, []);
});

test('a failed theme change does not block later updates', async () => {
    let attempts = 0;
    const f = fixture(async () => { if (++attempts === 1) throw new Error('theme unavailable'); });
    const dispose = bindTheme(f.widget, 'light', f.host);
    await setImmediate();
    f.send('dark');
    await setImmediate();
    assert.equal(f.errors.length, 1);
    f.send('dark');
    await setImmediate();
    assert.deepEqual(f.calls, ['dark', 'dark']);
    assert.equal(f.host.document.documentElement.style.colorScheme, 'dark');
    dispose();
});
