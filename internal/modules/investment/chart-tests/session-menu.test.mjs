import assert from 'node:assert/strict';
import test from 'node:test';
import { createSessionMenu } from '../chart/session-menu.js';

function deferred() {
    let resolve;
    const promise = new Promise(done => { resolve = done; });
    return { promise, resolve };
}

function setup(width = 1024, header = Promise.resolve(), create = options => Promise.resolve({ applyOptions(next) { this.options = next; }, remove() {} })) {
    const listeners = new Map();
    const host = {
        innerWidth: width,
        addEventListener(name, listener) { listeners.set(name, listener); },
        removeEventListener(name, listener) { if (listeners.get(name) === listener) listeners.delete(name); },
    };
    const widget = { headerReady: () => header, createDropdown: create };
    return { host, widget, listeners };
}

async function until(predicate) {
    for (let i = 0; i < 20 && !predicate(); i++) await new Promise(resolve => setImmediate(resolve));
    assert(predicate(), 'expected asynchronous menu action to complete');
}

test('session menu builds after headerReady and reflects only resolved sessions', async () => {
    const header = deferred(), created = deferred();
    const { host, widget } = setup(1024, header.promise, options => { created.options = options; return created.promise; });
    const menu = createSessionMenu(widget, { host });
    menu.setResolvedSession('extended', true, true);
    header.resolve();
    await until(() => Boolean(created.options));
    const handle = {
        options: created.options,
        applyOptions(options) { this.options = options; this.updated = true; },
        remove() { this.removed = true; },
    };
    created.resolve(handle);
    await until(() => handle.updated);

    assert.equal(handle.options.align, 'right');
    assert.deepEqual(handle.options.items.map(item => item.title), [
        '常规 (RTH)', '✓ 扩展 (ETH)', '夜盘 (Night)', '全天 (24H)',
    ]);
    assert.doesNotMatch(handle.options.tooltip, /周期说明|1、5、15、30/);

    menu.setResolvedSession('night', true, true);
    assert.equal(handle.options.title, '行情：夜盘');
    assert.equal(handle.options.items[2].title, '✓ 夜盘 (Night)');
    assert.equal(handle.options.items[3].title, '全天 (24H)');

    menu.setResolvedSession('extended', true, false);
    assert.deepEqual(handle.options.items.map(item => item.title), ['常规 (RTH)', '✓ 扩展 (ETH)', '夜盘 (Night)']);
    menu.setResolvedSession('night', false, false);
    assert.equal(handle.options.title, '行情：常规', 'unavailable night sessions must fall back to regular');
    assert.deepEqual(handle.options.items.map(item => item.title), ['✓ 常规 (RTH)', '扩展 (ETH)']);
    menu.dispose();
    assert.equal(handle.removed, true);
});

test('compact header labels stay short and session actions need no extra help item', async () => {
    let chosen;
    const { host, widget, listeners } = setup(390);
    widget.createDropdown = options => {
        const handle = {
            options,
            applyOptions(next) { this.options = next; this.updated = true; },
            remove() {},
        };
        widget.dropdown = handle;
        return Promise.resolve(handle);
    };
    const menu = createSessionMenu(widget, { host, onSelect: value => { chosen = value; } });
    menu.setResolvedSession('night', true, true);
    await until(() => widget.dropdown?.updated);
    const nightItem = widget.dropdown.options.items.find(item => item.title === '✓ 夜盘 (Night)');
    const fullDayItem = widget.dropdown.options.items.find(item => item.title === '全天 (24H)');
    assert(nightItem && fullDayItem);
    nightItem.onSelect();
    fullDayItem.onSelect();
    assert.equal(chosen, 'equity24h');
    assert.deepEqual(widget.dropdown.options.items.map(item => item.title), [
        '常规 (RTH)', '扩展 (ETH)', '✓ 夜盘 (Night)', '全天 (24H)',
    ]);
    host.innerWidth = 1024;
    listeners.get('resize')();
    await until(() => widget.dropdown?.updated && widget.dropdown.options.title === '行情：夜盘');
    assert.equal(widget.dropdown.options.align, 'right');
    menu.dispose();
});

test('a dropdown resolved after disposal is immediately removed', async () => {
    const created = deferred();
    const { host, widget, listeners } = setup(1024, Promise.resolve(), () => created.promise);
    const menu = createSessionMenu(widget, { host });
    await new Promise(resolve => setImmediate(resolve));
    menu.dispose();
    assert.equal(listeners.size, 0);
    const handle = { remove() { this.removed = true; }, applyOptions() {} };
    created.resolve(handle);
    await until(() => handle.removed === true);
    assert.equal(handle.removed, true);
});
