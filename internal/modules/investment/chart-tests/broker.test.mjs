import test from 'node:test';
import assert from 'node:assert/strict';
import Broker from '../chart/broker.js';
import { assetTypeLabel, buildOrder, mapOrder, mapPositions } from '../chart/broker-models.js';

const order = { symbol: 'AAPL', side: 1, type: 1, qty: 10, limitPrice: 100 };
const rawOrder = { orderId: 42, orderType: 'LIMIT', orderStrategyType: 'SINGLE', session: 'NORMAL', duration: 'GOOD_TILL_CANCEL', quantity: 10, price: 100, status: 'WORKING', enteredTime: '2026-01-01T15:00:00Z', orderLegCollection: [{ instruction: 'BUY', quantity: 10, instrument: { symbol: 'AAPL', assetType: 'EQUITY' } }] };
const json = (data, status = 200, headers) => new Response(JSON.stringify(data), { status, headers });
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) { for (let i = 0; i < 100; i++) { if (check()) return; await sleep(10); } assert.fail('condition timed out'); }

async function setup(t, handler = () => undefined, options = {}) {
    const calls = [], updates = [], errors = [];
    let accountUpdates = 0;
    const events = new EventTarget();
    const broker = new Broker({
        connectionStatusUpdate() {}, currentAccountUpdate() { accountUpdates++; },
        ordersFullUpdate() {}, positionsFullUpdate() {}, orderUpdate(value) { updates.push(value); }, positionUpdate() {},
        showNotification(_title, message) { errors.push(message); },
    }, {
        events, pollInterval: 0, ...options,
        request: async (path, init = {}) => {
            calls.push({ path, init });
            const result = await handler(path, init);
            if (result !== undefined) return result;
            if (path.endsWith('/accountNumbers')) return json([{ hashValue: 'A', accountNumber: '111' }, { hashValue: 'B', accountNumber: '222' }]);
            if (path.includes('?fields=positions')) return json({ securitiesAccount: { positions: [] } });
            if (path.includes('/orders?')) return json([rawOrder]);
            if (path.endsWith('/orders/42')) return json(rawOrder);
            throw new Error('unexpected request: ' + path);
        }
    });
    t.after(() => broker.destroy());
    await broker._ready;
    clearTimeout(broker._refreshTimer); broker._refreshTimer = undefined;
    return { broker, calls, updates, errors, events, accountUpdates: () => accountUpdates };
}

test('saved trading accounts restore and unavailable accounts fall back and update the workspace', async t => {
    const accounts = [];
    const { broker } = await setup(t, undefined, { preferredAccount: 'B', onAccountChanged: id => accounts.push(id) });
    assert.equal(broker.currentAccount(), 'B');
    broker.setCurrentAccount('A');
    assert.deepEqual(accounts, ['B', 'A']);
    const fallback = await setup(t, undefined, { preferredAccount: 'removed', onAccountChanged: id => accounts.push(id) });
    assert.equal(fallback.broker.currentAccount(), 'A');
    assert.equal(accounts.at(-1), 'A');
});

test('temporary account discovery failure and overlapping reconnects preserve the preferred account', async t => {
    let fail = false, held;
    const selected = [];
    const { broker, events } = await setup(t, path => {
        if (!path.endsWith('/accountNumbers')) return;
        if (fail) { fail = false; return json({ message: 'temporary' }, 503); }
        if (held) { const waiting = held; held = null; return waiting.promise; }
    }, { preferredAccount: 'B', onAccountChanged: id => selected.push(id) });
    fail = true;
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    await broker._ready;
    assert.equal(broker.currentAccount(), '');
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    await broker._ready;
    assert.equal(broker.currentAccount(), 'B');
    const old = Promise.withResolvers();
    held = old;
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    const previousInit = broker._ready;
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    await broker._ready;
    old.resolve(json([{ hashValue: 'A', accountNumber: '111' }, { hashValue: 'B', accountNumber: '222' }]));
    await previousInit;
    assert.equal(broker.currentAccount(), 'B');
    assert.ok(selected.every(id => id === 'B'));
});

test('new orders default to DAY/NORMAL and preserve explicit duration', () => {
    assert.equal(buildOrder(order).duration, 'DAY');
    assert.equal(buildOrder(order).session, 'NORMAL');
    for (const [type, expected] of [['DAY', 'DAY'], ['GTC', 'GOOD_TILL_CANCEL'], ['FOK', 'FILL_OR_KILL'], ['IOC', 'IMMEDIATE_OR_CANCEL']]) {
        assert.equal(buildOrder({ ...order, duration: { type } }).duration, expected);
    }
    assert.equal(buildOrder({ ...order, session: 'AM' }).session, 'AM');
    assert.equal(buildOrder({ ...order, customFields: { session: 'SEAMLESS' } }).session, 'SEAMLESS');
    assert.throws(() => buildOrder({ ...order, customFields: { session: 'EXTO' } }), /不支持的交易时段/);
    assert.throws(() => buildOrder({ ...order, qty: 0 }), /数量/);
    assert.throws(() => buildOrder({ ...order, duration: { type: 'UNSUPPORTED' } }), /有效期/);
    assert.throws(() => buildOrder({ ...order, type: 2, session: 'AM' }), /股票限价单/);
});

test('editing an order round-trips its session and duration into the ticket', () => {
    for (const [session, duration] of [['NORMAL', 'DAY'], ['AM', 'DAY'], ['PM', 'DAY'], ['SEAMLESS', 'GOOD_TILL_CANCEL'], ['NORMAL', 'FILL_OR_KILL'], ['NORMAL', 'IMMEDIATE_OR_CANCEL']]) {
        const original = { ...rawOrder, session, duration };
        const mapped = mapOrder(original);
        assert.equal(mapped.customFields.session, session);
        const payload = buildOrder({ ...mapped, limitPrice: 102 }, original);
        assert.equal(payload.session, session);
        assert.equal(payload.duration, duration);
        assert.equal(payload.price, 102);
    }
});

test('unsupported sessions and deferred durations never submit or replace an order', async t => {
    const original = { ...rawOrder, session: 'EXTO' };
    const { broker, calls } = await setup(t, path => path.endsWith('/orders/42') ? json(original) : undefined);
    for (const fields of [{ session: 'EXTO' }, { customFields: { session: 'EXTO' } }]) {
        await assert.rejects(broker.placeOrder({ ...order, ...fields }), /交易时段/);
    }
    for (const type of ['END_OF_WEEK', 'END_OF_MONTH', 'NEXT_END_OF_MONTH', 'UNKNOWN']) {
        await assert.rejects(broker.placeOrder({ ...order, duration: { type } }), /有效期/);
    }
    // A removed dropdown value must not silently turn an existing EXTO order
    // into a regular-session order when the UI falls back to its default.
    await assert.rejects(broker.modifyOrder({ ...mapOrder(original), customFields: { session: 'NORMAL' } }), /交易时段/);
    assert.equal(calls.filter(call => ['POST', 'PUT'].includes(call.init.method)).length, 0);
});

test('modifying stop-limit orders preserves both prices, session, duration and instruction', () => {
    const original = { ...rawOrder, orderType: 'STOP_LIMIT', price: 99, stopPrice: 100, orderLegCollection: [{ ...rawOrder.orderLegCollection[0], instruction: 'SELL' }] };
    const mapped = mapOrder(original);
    const payload = buildOrder({ ...mapped, qty: 8 }, original);
    assert.equal(payload.price, 99);
    assert.equal(payload.stopPrice, 100);
    assert.equal(payload.duration, 'GOOD_TILL_CANCEL');
    assert.equal(payload.orderLegCollection[0].instruction, 'SELL');
    assert.throws(() => buildOrder(order, { ...original, orderStrategyType: 'TRIGGER' }), /复杂订单/);
});

test('option close orders do not accidentally open new contracts', () => {
    assert.equal(buildOrder({ ...order, symbol: 'AAPL  260918C00200000', side: -1, isClose: true }).orderLegCollection[0].instruction, 'SELL_TO_CLOSE');
});

test('cancel failure is rejected and never produces an optimistic cancelled order', async t => {
    const { broker, updates } = await setup(t, (_path, init) => init.method === 'DELETE' ? json({ error: 'rejected' }, 400) : undefined);
    await assert.rejects(broker.cancelOrder('42'), /撤单失败.*400/);
    assert.equal(updates.length, 0);
});

test('successful cancel uses the confirmed REST status', async t => {
    const { broker, updates } = await setup(t, (path, init) => {
        if (init.method === 'DELETE') return new Response(null, { status: 204 });
        if (path.endsWith('/orders/42')) return json({ ...rawOrder, status: 'PENDING_CANCEL' });
    });
    await broker.cancelOrder('42');
    assert.equal(updates[0].status, 6, 'accepted cancellation is not yet a cancelled order');
});

test('successful placement returns a real ID and readback failure does not invite a duplicate trade', async t => {
    const { broker, calls, errors } = await setup(t, (path, init) => {
        if (init.method === 'POST') return new Response(null, { status: 201, headers: { Location: 'https://api.schwabapi.com/trader/v1/accounts/A/orders/42' } });
        if (path.endsWith('/orders/42')) return json({ error: 'temporary' }, 503);
    });
    assert.deepEqual(await broker.placeOrder(order), { orderId: '42' });
    assert.equal(calls.filter(call => call.init.method === 'POST').length, 1);
    assert.equal(JSON.parse(calls.find(call => call.init.method === 'POST').init.body).duration, 'DAY');
    assert.equal(errors.length, 1);
});

test('real long and short holdings are fetched for the selected account', async t => {
    const positions = [{ instrument: { symbol: 'AAPL' }, longQuantity: 7, shortQuantity: 0, averagePrice: 123 }, { instrument: { symbol: 'MSFT' }, shortQuantity: 3, longQuantity: 0, averagePrice: 450 }];
    const { broker, calls } = await setup(t, path => path.includes('?fields=positions') ? json({ securitiesAccount: { positions } }) : undefined);
    const result = await broker.positions();
    assert.deepEqual(result.map(p => [p.symbol, p.side, p.direction, p.qty, p.avgPrice]), [['AAPL', 1, '持有', 7, 123], ['MSFT', -1, '卖空', 3, 450]]);
    assert.ok(calls.some(call => call.path === '/trader/v1/accounts/A?fields=positions'));
    assert.equal(mapPositions([]).length, 0);
    assert.deepEqual(mapPositions([
        { instrument: {}, longQuantity: 5, shortQuantity: 0, averagePrice: 1 },
        { instrument: { symbol: 'GOOG' }, longQuantity: 1, shortQuantity: 0, averagePrice: 317.4, marketValue: 329.71, longOpenProfitLoss: 12.31 },
    ]).map(p => [p.symbol, p.qty, p.direction, p.marketPrice, p.pl]), [['GOOG', 1, '持有', 329.71, 12.31]]);
});

test('account picker shows a label not the raw account number and summary uses Schwab balances', async t => {
    const { broker } = await setup(t, path => path.includes('?fields=positions') ? json({
        securitiesAccount: {
            currentBalances: { liquidationValue: 2235.19, cashBalance: 320.93, buyingPower: 2664 },
            positions: [{ instrument: { symbol: 'GOOG' }, longQuantity: 1, shortQuantity: 0, averagePrice: 317.4 }],
        }
    }) : undefined);
    assert.deepEqual((await broker.accountsMetainfo()).map(a => a.name), ['Schwab …111', 'Schwab …222']);
    await broker.positions();
    const info = broker.accountManagerInfo();
    assert.deepEqual(info.summary.map(field => [field.text, field.wValue.value()]), [['净值', 2235.19], ['现金', 320.93], ['购买力', 2664]]);
    const defaults = Object.freeze([{ text: '导出' }]);
    const menu = await info.contextMenuActions({}, defaults);
    assert.equal(defaults.length, 1);
    assert.equal(menu[0].text, '导出');
    assert.ok(menu.some(item => item.text === '刷新持仓'));
    const sessions = (await broker.getOrderDialogOptions()).customFields[0].items.map(item => item.value);
    assert.deepEqual(sessions, ['NORMAL', 'AM', 'PM', 'SEAMLESS']);
});

test('working orders stay on the orders page and completed orders go to history newest first', async t => {
    const filled = { ...rawOrder, orderId: 1, status: 'FILLED', enteredTime: '2026-01-01T12:00:00Z', closeTime: '2026-01-01T13:00:00Z' };
    const olderWorking = { ...rawOrder, orderId: 2, enteredTime: '2026-01-02T12:00:00Z' };
    const newerWorking = { ...rawOrder, orderId: 3, enteredTime: '2026-01-03T12:00:00Z' };
    const { broker } = await setup(t, path => path.includes('/orders?') ? json([filled, olderWorking, newerWorking]) : undefined);
    assert.deepEqual((await broker.orders()).map(o => o.id), ['3', '2']);
    assert.deepEqual((await broker.ordersHistory()).map(o => o.id), ['1']);
});

test('account activity updates rows without clearing the account manager', async t => {
    let ordersFull = 0, positionsFull = 0, positionUpdates = 0;
    const events = new EventTarget();
    const broker = new Broker({
        connectionStatusUpdate() {}, currentAccountUpdate() {},
        ordersFullUpdate() { ordersFull++; },
        positionsFullUpdate() { positionsFull++; },
        orderUpdate() {},
        positionUpdate() { positionUpdates++; },
        showNotification() {},
    }, {
        events, pollInterval: 0,
        request: async path => {
            if (path.endsWith('/accountNumbers')) return json([{ hashValue: 'A', accountNumber: '111' }]);
            if (path.includes('?fields=positions')) return json({ securitiesAccount: { positions: [{ instrument: { symbol: 'AAPL' }, longQuantity: 1, shortQuantity: 0, averagePrice: 10 }] } });
            if (path.includes('/orders?')) return json([rawOrder]);
            throw new Error(path);
        }
    });
    t.after(() => broker.destroy());
    await broker._ready;
    clearTimeout(broker._refreshTimer); broker._refreshTimer = undefined;
    const before = { ordersFull, positionsFull };
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{}] } }));
    await until(() => positionUpdates > 0);
    assert.equal(ordersFull, before.ordersFull);
    assert.equal(positionsFull, before.positionsFull);
});

test('startup reads 30 days of orders plus older working orders, and capped result windows are split', async t => {
    const day = 86400000;
    const reads = [];
    let recentCalls = 0;
    const { broker } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const days = (Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime'))) / day;
        reads.push({ days, status: params.get('status'), maxResults: params.get('maxResults') });
        if (params.get('status') === 'WORKING') return json([{ ...rawOrder, orderId: 44 }]);
        recentCalls++;
        if (recentCalls === 1) return json(Array(1000).fill(rawOrder));
        return json([{ ...rawOrder, orderId: recentCalls === 2 ? 42 : 43 }]);
    }, { historyDelay: 60000 });
    const working = await broker.orders();
    assert.deepEqual(working.map(o => o.id).sort(), ['42', '43', '44']);
    assert.ok(reads.every(read => read.maxResults === '1000'));
    assert.ok(Math.abs(reads[0].days - 30) < 0.01 && reads[0].status === null, JSON.stringify(reads[0]));
    assert.ok(Math.abs(reads[1].days - 150) < 0.01 && reads[1].status === 'WORKING', JSON.stringify(reads[1]));
    assert.equal(reads.length, 4);
});

test('older history is read once, after startup or when the history is requested, and added silently', async t => {
    const day = 86400000;
    const now = Date.now();
    const reads = [];
    let fullUpdates = 0;
    const recentFilled = { ...rawOrder, orderId: 1, status: 'FILLED', enteredTime: new Date(now - 5 * day).toISOString() };
    const oldFilled = { ...rawOrder, orderId: 2, status: 'FILLED', enteredTime: new Date(now - 100 * day).toISOString() };
    const oldWorking = { ...rawOrder, orderId: 3, enteredTime: new Date(now - 60 * day).toISOString() };
    // Entry filled long ago, exit still working: only the history read finds it.
    const bracket = { ...rawOrder, orderId: 4, status: 'FILLED', orderStrategyType: 'TRIGGER', enteredTime: new Date(now - 90 * day).toISOString(), childOrderStrategies: [{ ...rawOrder, orderId: 5, enteredTime: new Date(now - 90 * day).toISOString() }] };
    const all = [recentFilled, oldFilled, oldWorking, bracket];
    const { broker, updates } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const from = Date.parse(params.get('fromEnteredTime')), to = Date.parse(params.get('toEnteredTime')), status = params.get('status');
        reads.push({ days: (to - from) / day, status });
        return json(all.filter(order => { const at = Date.parse(order.enteredTime); return at >= from && at <= to && (!status || order.status === status); }));
    }, { historyDelay: 60000 });
    broker.host.ordersFullUpdate = () => { fullUpdates++; };
    assert.deepEqual((await broker.orders()).map(o => o.id), ['3']);
    assert.equal(reads.length, 2);
    // Requesting the history reads the remaining range at once and waits for it.
    assert.deepEqual((await broker.ordersHistory()).map(o => o.id).sort(), ['1', '2', '4']);
    assert.equal(reads.length, 3);
    assert.ok(Math.abs(reads[2].days - 150) < 0.01 && reads[2].status === null, JSON.stringify(reads[2]));
    assert.equal(fullUpdates, 1);
    assert.deepEqual((await broker.orders()).map(o => o.id).sort(), ['3', '5']);
    assert.deepEqual((await broker.ordersHistory()).map(o => o.id).sort(), ['1', '2', '4']);
    assert.deepEqual(updates, []);
    await broker.ordersHistory();
    await broker._loadHistory();
    assert.equal(reads.length, 3, 'history is read once per account connection');
    // The poll keeps watching every stored working order, old ones included.
    await broker._refresh();
    assert.ok(reads[3].days > 89 && reads[3].days < 91, JSON.stringify(reads[3]));
});

test('without a history request the older orders are read after the startup delay', async t => {
    const reads = [];
    const { broker } = await setup(t, path => {
        if (path.includes('/orders?')) reads.push(new URL(path, 'http://localhost').searchParams.get('status'));
    }, { historyDelay: 20 });
    await broker._refresh(true);
    assert.equal(reads.length, 2);
    await until(() => reads.length === 3);
    assert.equal(reads[2], null);
    await sleep(100);
    await broker._refresh();
    await sleep(100);
    assert.equal(reads.length, 4);
});

test('an explicit history read rejects an incomplete range and succeeds when the retry recovers', async t => {
    const day = 86400000;
    const oldFilled = { ...rawOrder, orderId: 7, status: 'FILLED', enteredTime: new Date(Date.now() - 90 * day).toISOString() };
    let failHistory = true;
    const { broker, errors } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const span = Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime'));
        if (params.get('status') === 'WORKING' || span < 100 * day) return json([]);
        return failHistory ? json({ message: 'temporary' }, 503) : json([oldFilled]);
    }, { historyDelay: 60000 });
    await broker._refresh(true);
    await assert.rejects(broker.ordersHistory(), /HTTP 503/);
    assert.equal(broker._historyEpoch, undefined);
    assert.equal(errors.length, 1);
    failHistory = false;
    assert.deepEqual((await broker.ordersHistory()).map(order => order.id), ['7']);
});

test('background history failures are handled and a later poll retries them', async t => {
    const day = 86400000;
    const oldFilled = { ...rawOrder, orderId: 8, status: 'FILLED', enteredTime: new Date(Date.now() - 90 * day).toISOString() };
    let failHistory = true;
    const unhandled = [];
    const onUnhandled = reason => { unhandled.push(reason); };
    process.on('unhandledRejection', onUnhandled);
    t.after(() => process.removeListener('unhandledRejection', onUnhandled));
    const { broker, errors } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const span = Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime'));
        if (params.get('status') === 'WORKING' || span < 100 * day) return json([]);
        return failHistory ? json({ message: 'temporary' }, 503) : json([oldFilled]);
    }, { historyDelay: 10 });
    await broker._refresh(true);
    await until(() => errors.length === 1);
    await sleep(20);
    assert.deepEqual(unhandled, []);
    failHistory = false;
    await broker._refresh();
    await until(() => broker._historyEpoch === broker._epoch);
    assert.deepEqual((await broker.ordersHistory()).map(order => order.id), ['8']);
    assert.equal(errors.length, 1, 'the failed background range is reported once per connection');
});

test('switching accounts rejects an explicit history read from the previous account', async t => {
    const day = 86400000;
    let releaseHistory;
    const { broker } = await setup(t, path => {
        if (!path.startsWith('/trader/v1/accounts/A/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const span = Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime'));
        if (!params.get('status') && span > 100 * day) {
            return new Promise(resolve => { releaseHistory = resolve; });
        }
        return json([]);
    }, { historyDelay: 60000 });
    await broker.orders();
    const history = broker.ordersHistory();
    await until(() => releaseHistory);
    broker.setCurrentAccount('B');
    releaseHistory(json([{ ...rawOrder, orderId: 9, status: 'FILLED' }]));
    await assert.rejects(history, /账户已切换/);
});

test('a synchronous account switch from the history full-update callback rejects the old request', async t => {
    const day = 86400000;
    const oldFilled = { ...rawOrder, orderId: 10, status: 'FILLED', enteredTime: new Date(Date.now() - 90 * day).toISOString() };
    const { broker } = await setup(t, path => {
        if (!path.startsWith('/trader/v1/accounts/A/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const span = Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime'));
        if (params.get('status') === 'WORKING' || span < 100 * day) return json([]);
        return json([oldFilled]);
    }, { historyDelay: 60000 });
    let switched = false;
    broker.host.ordersFullUpdate = () => {
        if (switched) return;
        switched = true;
        broker.setCurrentAccount('B');
    };
    await assert.rejects(broker.ordersHistory(), /账户已切换/);
    assert.equal(switched, true);
    assert.equal(broker.currentAccount(), 'B');
});

test('startup shares one order read and the poll waits until it finishes', async t => {
    let release;
    let holdFirst = true;
    let current = [rawOrder];
    const calls = [], updates = [], held = [];
    const events = new EventTarget();
    const broker = new Broker({
        connectionStatusUpdate() {}, currentAccountUpdate() {},
        ordersFullUpdate() {}, positionsFullUpdate() {},
        orderUpdate(value) { updates.push(value); }, positionUpdate() {}, showNotification() {},
    }, {
        events, pollInterval: 10, historyDelay: 60000,
        request: async path => {
            calls.push(path);
            if (path.endsWith('/accountNumbers')) {
                events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
                return json([{ hashValue: 'A', accountNumber: '111' }, { hashValue: 'B', accountNumber: '222' }]);
            }
            if (path.includes('?fields=positions')) return json({ securitiesAccount: { positions: [] } });
            if (path.includes('/orders?')) {
                if (holdFirst) {
                    // Both startup reads (recent and older working orders) are held.
                    if (held.length === 1) holdFirst = false;
                    return new Promise(resolve => { held.push(resolve); release = () => held.forEach(done => done(json(current))); });
                }
                return json(current);
            }
            throw new Error(path);
        }
    });
    t.after(() => broker.destroy());
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    const pending = Promise.all([broker.orders(), broker.ordersHistory()]);
    const orderCalls = () => calls.filter(path => path.includes('/orders?'));
    await until(() => orderCalls().length === 2);
    await sleep(250);
    assert.equal(orderCalls().length, 2);
    assert.equal(broker._poll, undefined);
    assert.equal(calls.filter(path => path.endsWith('/accountNumbers')).length, 1);
    release();
    const [working, history] = await pending;
    assert.ok(broker._poll);
    clearInterval(broker._poll);
    clearTimeout(broker._refreshTimer);
    broker._refreshTimer = undefined;
    assert.deepEqual(working.map(order => order.id), ['42']);
    assert.deepEqual(history, []);
    // Opening the history also reads the older range once, without announcing it.
    await broker._loadHistory();
    assert.equal(orderCalls().length, 3);
    assert.deepEqual(updates, []);
    current = [{ ...rawOrder, price: 125 }];
    await broker._refresh();
    assert.equal(orderCalls().length, 4);
    assert.deepEqual(updates.map(order => [order.id, order.limitPrice]), [['42', 125]]);
});

test('activity during the shared order read is fetched once afterward and published', async t => {
    let release;
    let current = [rawOrder];
    const calls = [], updates = [];
    const events = new EventTarget();
    const broker = new Broker({
        connectionStatusUpdate() {}, currentAccountUpdate() {},
        ordersFullUpdate() {}, positionsFullUpdate() {},
        orderUpdate(value) { updates.push(value); }, positionUpdate() {}, showNotification() {},
    }, {
        events, pollInterval: 0,
        request: async path => {
            calls.push(path);
            if (path.endsWith('/accountNumbers')) return json([{ hashValue: 'A', accountNumber: '111' }]);
            if (path.includes('?fields=positions')) return json({ securitiesAccount: { positions: [] } });
            if (path.includes('/orders?')) {
                if (!release) {
                    const original = [rawOrder];
                    return new Promise(resolve => { release = () => resolve(json(original)); });
                }
                return json(current);
            }
            throw new Error(path);
        }
    });
    t.after(() => broker.destroy());
    const pending = broker.orders();
    const orderCalls = () => calls.filter(path => path.includes('/orders?'));
    await until(() => release);
    current = [{ ...rawOrder, status: 'FILLED', price: 125 }];
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{}] } }));
    release();
    const initial = await pending;
    assert.equal(initial.length, 1);
    assert.equal(initial[0].status, 6);
    assert.equal(initial[0].limitPrice, 100);
    assert.equal(orderCalls().length, 2);
    await until(() => updates.length === 1);
    assert.equal(orderCalls().length, 3);
    assert.deepEqual(updates.map(order => [order.id, order.status, order.limitPrice]), [['42', 2, 125]]);
    await sleep(250);
    assert.equal(orderCalls().length, 3);
    assert.equal(updates.length, 1);
});

test('switching accounts discards late responses and clears the previous cache', async t => {
    let release;
    const { broker, calls, accountUpdates } = await setup(t, path => path.startsWith('/trader/v1/accounts/A/orders?') ? new Promise(resolve => { release = () => resolve(json([rawOrder])); }) : undefined);
    const old = broker.orders();
    await until(() => release);
    const rejected = assert.rejects(old, /账户已切换/);
    broker.setCurrentAccount('B');
    release();
    await rejected;
    assert.equal(broker._orders.size, 0);
    await broker.positions();
    assert.ok(calls.some(call => call.path === '/trader/v1/accounts/B?fields=positions'));
    assert.equal(accountUpdates(), 2);
    assert.throws(() => broker.setCurrentAccount('C'), /未知/);
});

test('fill events refresh REST quantities and prices without parsing undocumented event numbers', async t => {
    const filled = { ...rawOrder, status: 'FILLED', filledQuantity: 10, orderActivityCollection: [{ executionLegs: [{ quantity: 4, price: 100 }, { quantity: 6, price: 110 }] }] };
    let current = rawOrder;
    const { broker, events, updates } = await setup(t, path => path.includes('/orders?') ? json([current]) : undefined);
    await broker.orders();
    current = filled;
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{ '2': 'OrderFillCompleted', '3': JSON.stringify({ SchwabOrderID: 42 }) }] } }));
    await until(() => updates.length > 0);
    assert.equal(updates[0].status, 2);
    assert.equal(updates[0].filledQty, 10);
    assert.equal(updates[0].avgPrice, 106);
});

test('an old-account event cannot inject its order into a newly selected account', async t => {
    let current = [];
    const { broker, events, updates, calls } = await setup(t, path => path.startsWith('/trader/v1/accounts/B/orders?') ? json(current) : undefined);
    broker.setCurrentAccount('B');
    await broker.orders();
    current = [{ ...rawOrder, orderId: 99 }];
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{ '1': '111', '2': 'OrderCreated', '3': { SchwabOrderID: 42 } }] } }));
    await until(() => updates.length > 0);
    assert.deepEqual(updates.map(o => o.id), ['99']);
    assert.ok(!calls.some(call => call.path.startsWith('/trader/v1/accounts/A/orders?')));
});

test('initial history, polling, account switches and reconnects do not replay order notifications', async t => {
    const history = ['FILLED', 'CANCELED', 'REJECTED'].map((status, index) => ({ ...rawOrder, orderId: index + 1, status }));
    const { broker, updates, events, errors } = await setup(t, path => path.includes('/orders?') ? json([rawOrder, ...history]) : undefined);
    await broker._refresh();
    assert.deepEqual((await broker.orders()).map(order => order.id), ['42']);
    assert.equal((await broker.ordersHistory()).length, 3);
    await broker._refresh();
    broker.setCurrentAccount('B');
    await broker._refresh();
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    await broker._ready;
    await broker._refresh();
    assert.deepEqual(updates, []);
    assert.deepEqual(errors, []);
});

test('new orders, partial fills, price changes and cancellations still publish once', async t => {
    let current = [{ ...rawOrder, enteredTime: undefined }];
    const { broker, updates, errors } = await setup(t, path => path.includes('/orders?') ? json(current) : undefined);
    // Initial library reads and polling share the same notification baseline.
    await broker.ordersHistory();
    await broker._refresh();
    assert.equal(updates.length, 0, 'a generated timestamp is not a change');
    current = [...current, { ...rawOrder, orderId: 43 }];
    await broker._refresh();
    current[1] = { ...current[1], filledQuantity: 3 };
    await broker._refresh();
    current[1] = { ...current[1], price: 102 };
    await broker._refresh();
    current[1] = { ...current[1], status: 'CANCELED' };
    await broker._refresh();
    await broker._refresh();
    assert.deepEqual(updates.map(order => [order.id, order.status, order.filledQty, order.limitPrice]), [
        ['43', 6, 0, 100], ['43', 6, 3, 100], ['43', 6, 3, 102], ['43', 1, 3, 102],
    ]);
    assert.deepEqual(errors, []);
});

test('REST readback after a trade is not announced again by the next poll', async t => {
    const canceled = { ...rawOrder, status: 'CANCELED' };
    let current = rawOrder;
    const { broker, updates } = await setup(t, (path, init) => {
        if (init.method === 'DELETE') { current = canceled; return new Response(null, { status: 204 }); }
        if (path.includes('/orders?') || path.endsWith('/orders/42')) return json(path.includes('?') ? [current] : current);
    });
    await broker.orders();
    await broker.cancelOrder('42');
    await broker._refresh();
    assert.equal(updates.length, 1);
    assert.equal(updates[0].status, 1);
});

test('stream reconnection reloads linked accounts after OAuth identity changes', async t => {
    let changed = false;
    const { broker, events } = await setup(t, path => changed && path.endsWith('/accountNumbers') ? json([{ hashValue: 'C', accountNumber: '333' }]) : undefined);
    broker._orders.set('42', mapOrder(rawOrder));
    changed = true;
    events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
    await broker._ready;
    assert.equal(broker.currentAccount(), 'C');
    assert.equal(broker._orders.size, 0);
});

test('gateway failures leave trade outcomes unconfirmed without retrying', async t => {
    const { broker, calls, updates } = await setup(t, (_path, init) => init.method ? json({ error: 'gateway timeout' }, 502) : undefined);
    await assert.rejects(broker.placeOrder(order), /结果尚未确认.*502.*勿重复提交/);
    await assert.rejects(broker.modifyOrder({ ...mapOrder(rawOrder), qty: 8 }), /结果尚未确认.*502.*勿重复提交/);
    await assert.rejects(broker.cancelOrder('42'), /结果尚未确认.*502.*勿重复提交/);
    assert.deepEqual(calls.filter(call => call.init.method).map(call => call.init.method), ['POST', 'PUT', 'DELETE']);
    assert.equal(updates.length, 0);
});

test('a submitted trade stays in its original account while the user switches accounts', async t => {
    let release;
    const { broker, calls, updates } = await setup(t, (_path, init) => init.method === 'POST' ? new Promise(resolve => {
        release = () => resolve(new Response(null, { status: 201, headers: { Location: '/accounts/A/orders/42' } }));
    }) : undefined);
    const placing = broker.placeOrder(order);
    await until(() => release);
    broker.setCurrentAccount('B');
    const submitted = calls.find(call => call.init.method === 'POST');
    assert.equal(submitted.path, '/trader/v1/accounts/A/orders');
    assert.equal(submitted.init.signal.aborted, false);
    release();
    assert.deepEqual(await placing, { orderId: '42' });
    assert.equal(updates.length, 0);
    assert.equal(calls.filter(call => call.init.method === 'POST').length, 1);
});

test('unsupported existing order types cannot be silently converted during modification', () => {
    const original = { ...rawOrder, orderType: 'TRAILING_STOP', stopPriceLinkBasis: 'BID', stopPriceOffset: 5 };
    assert.throws(() => buildOrder({ ...mapOrder(original), limitPrice: 100 }, original), /订单类型/);
});

test('account discovery starts after the broker factory installs the adapter', async t => {
    let installed;
    const statuses = [], calls = [];
    const broker = new Broker({
        connectionStatusUpdate(status) {
            assert.ok(installed, 'TradingView cannot receive updates before broker_factory returns');
            assert.equal(installed.connectionStatus(), status);
            statuses.push(status);
        },
        currentAccountUpdate() {},
    }, {
        events: new EventTarget(), pollInterval: 0,
        request: async path => {
            calls.push(path);
            return json([{ hashValue: 'A', accountNumber: '111' }]);
        }
    });
    // TradingView installs the connection in a promise continuation after
    // broker_factory returns; deferring by only one microtask is still too early.
    queueMicrotask(() => { installed = broker; });
    t.after(() => broker.destroy());
    await broker._ready;
    assert.equal(broker.connectionStatus(), 1);
    assert.equal(broker.currentAccount(), 'A');
    assert.deepEqual(calls, ['/trader/v1/accounts/accountNumbers']);
    assert.equal(statuses.at(-1), 1);
});

test('positions show Chinese asset types with a raw fallback and Chinese column titles', async t => {
    assert.equal(assetTypeLabel('EQUITY'), '股票');
    assert.equal(assetTypeLabel('COLLECTIVE_INVESTMENT'), '基金/ETF');
    assert.equal(assetTypeLabel('NEW_KIND'), 'NEW_KIND');
    assert.equal(assetTypeLabel(undefined), '');
    const [position] = mapPositions([{ instrument: { symbol: 'SPY', assetType: 'COLLECTIVE_INVESTMENT' }, longQuantity: 2, shortQuantity: 0, averagePrice: 500 }]);
    assert.equal(position.type, 'COLLECTIVE_INVESTMENT');
    assert.equal(position.assetTypeLabel, '基金/ETF');
    const { broker } = await setup(t, () => undefined);
    const info = broker.accountManagerInfo();
    const titles = [...info.positionColumns, ...info.orderColumns].map(column => column.label);
    for (const title of titles) assert.doesNotMatch(title, /[A-Za-z]/, title);
    assert.deepEqual(info.positionColumns.map(column => column.label), ['标的', '方向', '数量', '均价', '现价', '已实现盈亏', '未实现盈亏', '当日盈亏', '类型']);
});

test('polls read recent orders and older working orders only, merged into the stored orders', async t => {
    const day = 86400000;
    const now = Date.now();
    const oldFilled = { ...rawOrder, orderId: 1, status: 'FILLED', enteredTime: new Date(now - 100 * day).toISOString() };
    const oldWorking = { ...rawOrder, orderId: 2, enteredTime: new Date(now - 20 * day).toISOString() };
    const ranges = [];
    let current = [oldFilled, oldWorking];
    const { broker, updates } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        const params = new URL(path, 'http://localhost').searchParams;
        const from = Date.parse(params.get('fromEnteredTime')), to = Date.parse(params.get('toEnteredTime')), status = params.get('status');
        ranges.push(to - from);
        return json(current.filter(order => { const at = Date.parse(order.enteredTime); return at >= from && at <= to && (!status || order.status === status); }));
    }, { historyDelay: 60000 });
    assert.deepEqual((await broker.orders()).map(order => order.id), ['2']);
    assert.ok(Math.abs(ranges[0] - 30 * day) < 1000, 'startup reads 30 days of all orders');
    await broker._loadHistory();
    assert.equal((await broker.ordersHistory()).length, 1);
    current = [{ ...oldWorking, status: 'FILLED' }];
    await broker._refresh();
    assert.ok(ranges[3] > 19 * day && ranges[3] < 22 * day, `poll range covers the oldest working order: ${ranges[3] / day}`);
    assert.deepEqual((await broker.ordersHistory()).map(order => order.id).sort(), ['1', '2']);
    assert.deepEqual(await broker.orders(), []);
    assert.deepEqual(updates.map(order => [order.id, order.status]), [['2', 2]]);
    current = [];
    await broker._refresh();
    assert.ok(ranges[4] <= 3 * day + 5000, 'without working orders only recent days are read');
    assert.equal((await broker.ordersHistory()).length, 2);
});

test('stream subscription acknowledgements do not re-read orders during the startup read', async t => {
    let release;
    const { broker, calls, events } = await setup(t, path => path.includes('/orders?') && !release ? new Promise(resolve => { release = () => resolve(json([rawOrder])); }) : undefined);
    const orders = calls.filter(call => call.path.includes('/orders?')).length;
    const pending = broker.orders();
    await until(() => release);
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { service: 'ACCT_ACTIVITY', command: 'SUBS', content: { code: 0, msg: 'subscribed' } } }));
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { service: 'ACCT_ACTIVITY', content: [{ key: 'k', '1': '111', '2': 'SUBSCRIBED', '3': '' }] } }));
    release();
    await pending;
    await sleep(400);
    // Startup is two reads: the last 30 days and older working orders.
    assert.equal(calls.filter(call => call.path.includes('/orders?')).length, orders + 2);
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { service: 'ACCT_ACTIVITY', content: [{ key: 'k', '1': '111', '2': 'OrderCreated', '3': '' }] } }));
    await until(() => calls.filter(call => call.path.includes('/orders?')).length === orders + 3);
});

test('back-to-back position reads share one response until account activity or a poll', async t => {
    const { broker, calls, events } = await setup(t);
    const reads = () => calls.filter(call => call.path.includes('?fields=positions')).length;
    const before = reads();
    await broker.positions();
    await broker.positions();
    assert.equal(reads(), before + 1);
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{ '2': 'OrderFillCompleted' }] } }));
    await until(() => reads() === before + 2);
});
