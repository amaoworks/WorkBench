import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../chart/identity.js', import.meta.url), 'utf8');

function terminal() {
    const window = new EventTarget();
    const document = new EventTarget();
    document.visibilityState = 'visible';
    const channels = [], requests = [], sockets = [];
    let currentUser = 'alice', poll, delay;
    window.fetch = async (url, init = {}) => {
        if (url === '/api/auth/status') return Response.json({ authenticated: !!currentUser, user: currentUser ? { id: currentUser } : undefined });
        requests.push({ url, init });
        if (delay) return delay.promise;
        if (init.headers.get('X-Workbench-User') !== currentUser) return Response.json({ code: 'account_changed' }, { status: 409 });
        return Response.json({ saved: true });
    };
    window.WebSocket = class extends EventTarget {
        constructor(url) { super(); this.url = url; sockets.push(this); }
        close() { this.closed = true; this.dispatchEvent(new Event('close')); }
    };
    class Channel {
        constructor(name) { this.name = name; channels.push(this); }
        close() { this.closed = true; }
    }
    vm.runInNewContext(source, {
        window, document, location: { href: 'https://workbench.test/investment/terminal', origin: 'https://workbench.test', host: 'workbench.test' },
        Headers, Request, Response, URL, AbortController, AbortSignal, Event,
        BroadcastChannel: Channel, setInterval: fn => { poll = fn; return 1; }, clearInterval() {},
    });
    return {
        window, channels, requests, sockets,
        changeUser: id => { currentUser = id; },
        deferRequest: () => { delay = Promise.withResolvers(); return delay; },
        poll: () => poll(),
    };
}

test('terminal HTTP, keepalive and WebSocket requests pin their initial user', async () => {
    const t = terminal();
    assert.equal(await t.window.terminalIdentity.ready, 'alice');
    await t.window.fetch('/api/modules/investment/preferences', { method: 'PUT', keepalive: true, body: '{}' });
    assert.equal(t.requests[0].init.headers.get('X-Workbench-User'), 'alice');
    assert.equal(t.requests[0].init.keepalive, true);
    const socket = new t.window.WebSocket('wss://workbench.test/api/modules/investment/schwab/trader/ws');
    assert.equal(new URL(socket.url).searchParams.get('workbenchUser'), 'alice');
    assert.equal(t.channels[0].name, 'workbench-auth');
    t.channels[0].onmessage({ data: { type: 'identity', userId: 'alice' } });
    assert.equal(t.window.terminalIdentity.active, true);
    t.channels[0].onmessage({ data: { type: 'identity', userId: 'bob' } });
    assert.equal(socket.closed, true);
    assert.equal(t.window.terminalIdentity.active, false);
    await assert.rejects(t.window.fetch('/api/modules/investment/preferences', { method: 'PUT', keepalive: true }), /登录用户已变更/);
    assert.throws(() => new t.window.WebSocket('wss://workbench.test/api/stream'), /登录用户已变更/);
    assert.equal(t.requests.length, 1);
});

test('a missed broadcast cannot let an old terminal save into the new cookie identity', async () => {
    const t = terminal();
    await t.window.terminalIdentity.ready;
    t.changeUser('bob');
    await assert.rejects(t.window.fetch('/api/modules/investment/watchlists', { method: 'PUT', keepalive: true, body: '{}' }), /登录用户已变更/);
    assert.equal(t.requests[0].init.headers.get('X-Workbench-User'), 'alice');
    assert.equal(t.window.terminalIdentity.active, false);
    await assert.rejects(t.window.fetch('/api/auth/csrf'), /登录用户已变更/);
    assert.equal(t.requests.length, 1, 'must not fetch a new CSRF token or retry as Bob');
});

test('identity loss aborts pending responses and discards their late values', async () => {
    const t = terminal();
    await t.window.terminalIdentity.ready;
    const pending = t.deferRequest();
    const read = t.window.fetch('/api/modules/investment/positions');
    await Promise.resolve();
    const rejected = assert.rejects(read, /登录用户已变更/);
    t.channels[0].onmessage({ data: { type: 'identity', userId: null } });
    assert.equal(t.requests[0].init.signal.aborted, true);
    pending.resolve(Response.json({ private: 'old response' }));
    await rejected;
});

test('periodic status verification also invalidates an old standalone terminal', async () => {
    const t = terminal();
    await t.window.terminalIdentity.ready;
    t.changeUser('bob');
    t.poll();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(t.window.terminalIdentity.active, false);
});
