import test from 'node:test';
import assert from 'node:assert/strict';
import Broker from '../chart/broker.js';
import { buildOrder, mapOrder, mapPositions } from '../chart/broker-models.js';

const order = { symbol: 'AAPL', side: 1, type: 1, qty: 10, limitPrice: 100 };
const rawOrder = { orderId: 42, orderType: 'LIMIT', orderStrategyType: 'SINGLE', session: 'NORMAL', duration: 'GOOD_TILL_CANCEL', quantity: 10, price: 100, status: 'WORKING', enteredTime: '2026-01-01T15:00:00Z', orderLegCollection: [{ instruction: 'BUY', quantity: 10, instrument: { symbol: 'AAPL', assetType: 'EQUITY' } }] };
const json = (data, status = 200, headers) => new Response(JSON.stringify(data), { status, headers });
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check) { for (let i = 0; i < 100; i++) { if (check()) return; await sleep(10); } assert.fail('condition timed out'); }

async function setup(t, handler = () => undefined) {
    const calls = [], updates = [], errors = [];
    let accountUpdates = 0;
    const events = new EventTarget();
    const broker = new Broker({
        connectionStatusUpdate() {}, currentAccountUpdate() { accountUpdates++; },
        ordersFullUpdate() {}, positionsFullUpdate() {}, orderUpdate(value) { updates.push(value); }, positionUpdate() {},
        showNotification(_title, message) { errors.push(message); },
    }, {
        events, pollInterval: 0,
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

test('old GTC orders remain visible and capped result windows are split', async t => {
    let rangeCalls = 0;
    const { broker } = await setup(t, path => {
        if (!path.includes('/orders?')) return;
        rangeCalls++;
        const params = new URL(path, 'http://localhost').searchParams;
        assert.equal(params.get('maxResults'), '3000');
        if (rangeCalls === 1) {
            assert.ok(Date.parse(params.get('toEnteredTime')) - Date.parse(params.get('fromEnteredTime')) > 300 * 86400000);
            return json(Array(3000).fill(rawOrder));
        }
        return json([{ ...rawOrder, orderId: rangeCalls === 2 ? 42 : 43 }]);
    });
    assert.deepEqual((await broker.orders()).map(o => o.id), ['42', '43']);
    assert.equal(rangeCalls, 3);
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
    const { events, updates } = await setup(t, path => path.includes('/orders?') ? json([filled]) : undefined);
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{ '2': 'OrderFillCompleted', '3': JSON.stringify({ SchwabOrderID: 42 }) }] } }));
    await until(() => updates.length > 0);
    assert.equal(updates[0].status, 2);
    assert.equal(updates[0].filledQty, 10);
    assert.equal(updates[0].avgPrice, 106);
});

test('an old-account event cannot inject its order into a newly selected account', async t => {
    const { broker, events, updates, calls } = await setup(t, path => path.startsWith('/trader/v1/accounts/B/orders?') ? json([{ ...rawOrder, orderId: 99 }]) : undefined);
    broker.setCurrentAccount('B');
    events.dispatchEvent(new CustomEvent('ACCT_ACTIVITY', { detail: { content: [{ '1': '111', '2': 'OrderCreated', '3': { SchwabOrderID: 42 } }] } }));
    await until(() => updates.length > 0);
    assert.deepEqual(updates.map(o => o.id), ['99']);
    assert.ok(!calls.some(call => call.path.startsWith('/trader/v1/accounts/A/orders?')));
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
