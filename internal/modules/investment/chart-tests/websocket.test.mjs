import test from 'node:test';
import assert from 'node:assert/strict';
import { webSocketURL } from '../chart/websocket.js';
import { SchwabStream } from '../chart/stream.js';
import { FutuStream } from '../chart/futu.js';

test('WebSocket URLs preserve the host, port and path and use the page transport security', () => {
    assert.equal(webSocketURL('/api/stream', 'https://workbench.test:8443/investment/terminal?theme=dark'), 'wss://workbench.test:8443/api/stream');
    assert.equal(webSocketURL('/api/stream', 'http://[::1]:8080/investment/terminal'), 'ws://[::1]:8080/api/stream');
    assert.equal(webSocketURL('wss://quotes.test/stream?channel=night', 'http://workbench.test/'), 'wss://quotes.test/stream?channel=night');
});

test('both providers initialize and reconnect with a browser that rejects relative WebSocket URLs', async t => {
    const locationDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'location');
    Object.defineProperty(globalThis, 'location', { configurable: true, value: { href: 'https://workbench.test:8443/investment/terminal' } });
    t.after(() => {
        if (locationDescriptor) Object.defineProperty(globalThis, 'location', locationDescriptor);
        else delete globalThis.location;
    });
    const opened = [];
    t.mock.method(globalThis, 'WebSocket', function (url) {
        // Match the legacy constructor: relative or HTTP(S) URLs throw before
        // any network event, which used to abort chart initialization.
        assert.match(url, /^wss?:\/\//);
        opened.push(url);
        return { close() { this.onclose?.(); } };
    });
    const schwab = new SchwabStream({ events: new EventTarget(), retryDelay: 1 });
    const futu = new FutuStream({
        events: new EventTarget(), retryDelay: 1,
        status: async () => new Response(JSON.stringify({ enabled: true, overnightEnabled: true })),
    });
    t.after(() => { schwab.close(); futu.close(); });
    futu.wanted = true;
    await futu.connect();
    schwab.ws.close();
    futu.ws.close();
    for (let i = 0; i < 100 && opened.length < 4; i++) await new Promise(resolve => setTimeout(resolve, 10));
    assert.deepEqual(opened, [
        'wss://workbench.test:8443/api/modules/investment/schwab/trader/ws',
        'wss://workbench.test:8443/api/modules/investment/futu/quote/ws',
        'wss://workbench.test:8443/api/modules/investment/schwab/trader/ws',
        'wss://workbench.test:8443/api/modules/investment/futu/quote/ws',
    ]);
});
