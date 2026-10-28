import { schwabFetch } from './schwab.js';
import { buildOrder, flattenOrders, mapOrder, mapPositions } from './broker-models.js';

// Schwab has no cursor; a full page is split by time (see _readRange).
const ORDER_LIMIT = 1000;
const DAY_MS = 24 * 60 * 60 * 1000;
const YEAR_MS = 365 * DAY_MS;
// The terminal keeps up to 180 days of orders; older history stays in Schwab.
const ORDER_WINDOW_MS = 180 * DAY_MS;
// Startup reads all orders of the last 30 days plus WORKING orders back to 180
// days (Schwab filters one status per request). The rest of the 180 days, which
// also holds any other old non-terminal order, is read after startup settles or
// when the order history is requested, whichever comes first.
const STARTUP_MS = 30 * DAY_MS;
const HISTORY_DELAY_MS = 3000;
// Polls and account activity re-read only recent orders (plus any older working
// order) and merge them into the startup snapshot instead of the whole window.
const RECENT_MS = 3 * DAY_MS;
const TERMINAL_STATUSES = new Set(['CANCELED', 'EXPIRED', 'REPLACED', 'FILLED', 'REJECTED']);
const WORKING = 6;
const ORDER_COLUMNS = [
    { label: '标的', formatter: 'symbol', id: 'symbol', dataFields: ['symbol', 'symbol', 'message'] },
    { label: '状态', id: 'status', dataFields: ['status'], formatter: 'status' },
    { label: '方向', id: 'side', dataFields: ['side'], formatter: 'side' },
    { label: '类型', id: 'type', formatter: 'type', dataFields: ['type'] },
    { label: '限价', id: 'limitPrice', dataFields: ['limitPrice', 'currency'], formatter: 'formatPriceInCurrency' },
    { label: '止损价', id: 'stopPrice', dataFields: ['stopPrice', 'currency'], formatter: 'formatPriceInCurrency' },
    { label: '数量', id: 'qty', dataFields: ['qty'], formatter: 'formatQuantity' },
    { label: '已成交', id: 'filledQty', dataFields: ['filledQty'], formatter: 'formatQuantity' },
    { label: '成交均价', id: 'avgPrice', dataFields: ['avgPrice', 'currency'], formatter: 'formatPriceInCurrency' },
];
const byRecent = (a, b) => (b.updateTime || 0) - (a.updateTime || 0);
// A missing broker timestamp falls back to Date.now() in mapOrder. It is not
// itself an order change and must not turn every poll into an update event.
const orderSignature = order => JSON.stringify({ ...order, updateTime: undefined });

function isAccountChange(item) {
    if (!item) return true;
    if (!Array.isArray(item.content)) return false;
    return item.content.some(entry => entry?.['2'] !== 'SUBSCRIBED');
}
// Positions read by the chart, the account manager and the refresh loop within a
// moment of each other share one Schwab response. Account activity and polls
// bump the order generation and therefore always read fresh positions.
const POSITIONS_REUSE_MS = 2000;

function accountLabel(accountNumber) {
    const digits = String(accountNumber).replace(/\D/g, '');
    return digits ? `Schwab …${digits.slice(-4)}` : 'Schwab';
}

export default class Broker {
    constructor(host, { request = schwabFetch, events = window, pollInterval = 15000, historyDelay = HISTORY_DELAY_MS, preferredAccount = '', onAccountChanged = () => {}, startAfter } = {}) {
        this.host = host;
        this.request = request;
        this.events = events;
        this._accounts = [];
        this._account = preferredAccount;
        this._preferredAccount = preferredAccount;
        this._onAccountChanged = onAccountChanged;
        this._epoch = 0;
        this._connectionStatus = 2;
        this._orders = new Map();
        this._rawOrders = new Map();
        this._ordersSnapshot = undefined;
        this._ordersInflight = undefined;
        this._orderSignatures = undefined;
        this._ordersGeneration = 0;
        this._pollInterval = pollInterval;
        this._historyDelay = historyDelay;
        this._controller = new AbortController();
        // Stream ready during the constructor's first init must not start a second
        // account load. A ready event after that init is a real reconnect.
        this._boot = true;
        // The stream confirms the ACCT_ACTIVITY subscription with a command response
        // and a SUBSCRIBED message. Neither is an account change, so neither may
        // add an order re-read on top of the startup snapshot.
        this._onActivity = event => { if (isAccountChange(event?.detail)) this._scheduleRefresh(); };
        this._onReady = () => {
            if (this._boot || this._destroyed) return;
            this._ready = this._init();
        };
        this._onDisconnect = () => this._setStatus(2);
        events.addEventListener('ACCT_ACTIVITY', this._onActivity);
        events.addEventListener('SCHWAB_STREAM_READY', this._onReady);
        events.addEventListener('SCHWAB_STREAM_CLOSED', this._onDisconnect);
        // The host installs its broker connection asynchronously after broker_factory returns.
        // Calling connectionStatusUpdate in this constructor aborts initialization
        // inside TradingView before even requesting the account list. A new task
        // lets the host's promise continuations finish; a microtask alone is too early.
        // `startAfter` lets the terminal hold the account reads (accounts, recent
        // orders, positions, executions) until the first chart is drawn, so
        // they do not compete with the chart's own startup requests.
        const start = startAfter ? Promise.resolve(startAfter).catch(() => {}) : undefined;
        this._ready = new Promise(resolve => setTimeout(resolve, 0)).then(() => start).then(() => {
            if (!this._destroyed) return this._init();
        }).finally(() => { this._boot = false; });
    }

    _setStatus(status) { this._connectionStatus = status; this.host.connectionStatusUpdate(status); }
    _invalidate() {
        this._epoch++;
        this._controller.abort();
        this._controller = new AbortController();
        this._orders.clear();
        this._rawOrders.clear();
        this._ordersSnapshot = undefined;
        this._ordersInflight = undefined;
        this._orderSignatures = undefined;
        this._historyInflight = undefined;
        this._historyFrom = undefined;
        clearTimeout(this._historyTimer);
        this._historyTimer = undefined;
    }
    async _init() {
        const preferredAccount = this._preferredAccount;
        this._invalidate();
        this._account = '';
        const epoch = this._epoch;
        this._setStatus(2);
        try {
            const response = await this.request('/trader/v1/accounts/accountNumbers', { signal: this._controller.signal });
            await this._checkResponse(response, '读取账户');
            const accounts = await response.json();
            if (epoch !== this._epoch) return;
            if (!Array.isArray(accounts) || !accounts.length || accounts.some(a => !a.hashValue || !a.accountNumber)) throw new Error('Schwab 未返回可用账户');
            this._accounts = accounts.map(a => ({
                id: String(a.hashValue),
                name: accountLabel(a.accountNumber),
                type: 'live',
                currency: 'USD',
                currencySign: '$',
            }));
            this._account = this._accounts.some(a => a.id === preferredAccount) ? preferredAccount : this._accounts[0].id;
            this._preferredAccount = this._account;
            this._onAccountChanged(this._account);
            this._setStatus(1);
            this.host.currentAccountUpdate();
            // The library is about to call orders() and ordersHistory() itself.
            // Reuse that read instead of starting a second startup download.
            this._scheduleRefresh(true);
        } catch (error) {
            if (epoch !== this._epoch) return;
            this._accounts = [];
            this._account = '';
            this._setStatus(4);
            this._notify(error);
        }
    }
    async _checkResponse(response, action) {
        if (!response.ok) {
            const body = await response.text();
            throw new Error(`${action}失败 [HTTP ${response.status}]: ${body.slice(0, 300)}`);
        }
    }
    async _context() {
        let ready;
        do { ready = this._ready; await ready; } while (ready !== this._ready);
        if (!this._account) throw new Error('尚未连接交易账户');
        return { account: this._account, epoch: this._epoch, signal: this._controller.signal };
    }
    _checkContext(context) {
        if (context.epoch !== this._epoch || context.account !== this._account) throw new Error('账户已切换，请重试');
    }
    async _response(context, suffix, init = {}, action = '读取账户数据') {
        this._checkContext(context);
        const writing = init.method && init.method !== 'GET';
        let response;
        try {
            // Switching accounts cancels reads. An already submitted trade must finish
            // against its original account, without cancellation or automatic retries.
            response = await this.request(`/trader/v1/accounts/${encodeURIComponent(context.account)}${suffix}`, {
                ...init, signal: writing ? AbortSignal.timeout(45000) : context.signal
            });
        } catch (error) {
            if (writing) throw new Error(`${action}结果尚未确认，请在原账户核对订单，勿重复提交`, { cause: error });
            throw error;
        }
        if (!writing) this._checkContext(context);
        if (writing && (response.status >= 500 || response.status === 408)) {
            throw new Error(`${action}结果尚未确认 [HTTP ${response.status}]，请在原账户核对订单，勿重复提交`);
        }
        await this._checkResponse(response, action);
        return response;
    }
    async _json(context, suffix) {
        const response = await this._response(context, suffix);
        const data = await response.json();
        this._checkContext(context);
        return data;
    }
    _notify(error) {
        if (error.name !== 'AbortError') this.host.showNotification?.('Schwab', error.message, 0);
    }
    _watched(value) {
        if (this.host.factory?.createWatchedValue) return this.host.factory.createWatchedValue(value);
        let current = value;
        return { value: () => current, setValue(next) { current = next; }, subscribe() {}, unsubscribe() {} };
    }
    _ensureSummary() {
        if (!this._summary) {
            this._equity = this._watched(0);
            this._cash = this._watched(0);
            this._buyingPower = this._watched(0);
            this._summary = [
                { text: '净值', wValue: this._equity, formatter: 'fixed', isDefault: true },
                { text: '现金', wValue: this._cash, formatter: 'fixed', isDefault: true },
                { text: '购买力', wValue: this._buyingPower, formatter: 'fixed', isDefault: true },
            ];
        }
        return this._summary;
    }
    _applyBalances(account) {
        const bal = account?.currentBalances || {};
        const equity = Number(bal.liquidationValue ?? bal.equity);
        const cash = Number(bal.cashBalance);
        const buyingPower = Number(bal.buyingPower);
        this._ensureSummary();
        if (Number.isFinite(equity)) {
            this._lastEquity = equity;
            this._equity.setValue(equity);
            this.host.equityUpdate?.(equity);
        }
        if (Number.isFinite(cash)) this._cash.setValue(cash);
        if (Number.isFinite(buyingPower)) this._buyingPower.setValue(buyingPower);
    }
    _scheduleRefresh(reuse = false) {
        if (!this._account || this._destroyed) return;
        // Activity during a startup reuse timer must upgrade that refresh. Bumping
        // the generation lets the pending timer see that its snapshot is stale.
        if (!reuse) this._ordersGeneration++;
        if (this._refreshTimer) return;
        const generation = this._ordersGeneration;
        this._refreshTimer = setTimeout(() => {
            this._refreshTimer = undefined;
            void this._refresh(reuse && generation === this._ordersGeneration);
        }, 200);
    }
    _armPoll() {
        // The interval starts only after the first order read settles, so a short
        // poll cannot add another startup-sized request while that read is in flight.
        if (this._poll || this._destroyed || !(this._pollInterval > 0)) return;
        this._poll = setInterval(() => this._scheduleRefresh(), this._pollInterval);
    }
    async _refresh(reuse = false) {
        const epoch = this._epoch;
        // A read already in flight started before this invalidation. Join it, then
        // take one newer read after it settles instead of keeping its snapshot.
        const joined = !reuse && this._ordersInflight?.epoch === epoch
            ? this._ordersInflight.generation
            : this._ordersGeneration;
        try {
            // Account activity is an invalidation signal. REST is the source of truth,
            // including fills and events with no identifiable account in their payload.
            // The startup refresh reuses the read already started for orders()/ordersHistory().
            if (!reuse) this._ordersSnapshot = undefined;
            const [orders, positions] = await Promise.all([this._loadOrders(), this.positions()]);
            if (epoch !== this._epoch || this._destroyed) return;
            for (const order of orders) {
                const signature = orderSignature(order);
                if (this._orderSignatures.get(order.id) === signature) continue;
                this._orderSignatures.set(order.id, signature);
                this.host.orderUpdate(order);
            }
            positions.forEach(position => this.host.positionUpdate?.(position));
            this._scheduleHistory();
            if (this._ordersGeneration !== joined && !this._refreshTimer) void this._refresh(false);
        } catch (error) {
            if (epoch !== this._epoch || this._destroyed) return;
            if (this._ordersGeneration !== joined && !this._refreshTimer) {
                void this._refresh(false);
                return;
            }
            this._notify(error);
        }
    }
    connectionStatus() { return this._connectionStatus; }
    currentAccount() { return this._account; }
    async accountsMetainfo() { await this._ready; return this._accounts; }
    setCurrentAccount(id) {
        if (!this._accounts.some(a => a.id === id)) throw new Error('未知的交易账户');
        if (id === this._account) return;
        this._invalidate();
        this._account = id;
        this._preferredAccount = id;
        this._onAccountChanged(id);
        this.host.currentAccountUpdate();
        this.host.ordersFullUpdate();
        this.host.positionsFullUpdate();
        this._scheduleRefresh();
    }
    async isTradable(symbol) { return !symbol.includes('/'); }
    async symbolInfo(symbol) {
        return { qty: { min: 1, max: 1e9, step: 1 }, pipValue: 1, pipSize: 0.01, minTick: 0.01, description: symbol, currency: 'USD' };
    }
    async positions() {
        const context = await this._context();
        // The chart and the refresh loop ask for positions at the same moment on
        // startup and after account events. Concurrent callers share one read.
        const generation = this._ordersGeneration;
        let pending = this._positionsInflight;
        const fresh = pending && pending.epoch === context.epoch && pending.account === context.account
            && pending.generation === generation && (!pending.settledAt || Date.now() - pending.settledAt < POSITIONS_REUSE_MS);
        if (!fresh) {
            const promise = this._json(context, '?fields=positions').then(data => {
                const account = data.securitiesAccount;
                if (!account || (account.positions != null && !Array.isArray(account.positions))) throw new Error('Schwab 持仓响应无效');
                this._applyBalances(account);
                return account.positions || [];
            });
            const entry = pending = this._positionsInflight = { epoch: context.epoch, account: context.account, generation, promise };
            promise.then(() => { entry.settledAt = Date.now(); }, () => {
                if (this._positionsInflight === entry) this._positionsInflight = undefined;
            });
        }
        return mapPositions(await pending.promise);
    }
    async _loadOrders() {
        const context = await this._context();
        const generation = this._ordersGeneration;
        if (this._ordersSnapshot?.epoch === context.epoch && this._ordersSnapshot.generation === generation) return this._ordersSnapshot.mapped;
        if (this._ordersInflight?.epoch === context.epoch) return this._ordersInflight.promise;
        // After the startup read of an account, every read updates the stored
        // orders instead of replacing them (and dropping loaded history).
        const promise = this._readOrders(context, generation, this._fullOrdersEpoch === context.epoch);
        this._ordersInflight = { epoch: context.epoch, generation, promise };
        // The caller observes `promise`. This side chain must not surface the same rejection again.
        promise.finally(() => {
            if (this._ordersInflight?.promise === promise) this._ordersInflight = undefined;
            this._armPoll();
        }).catch(() => {});
        return promise;
    }
    _incrementalFrom(to) {
        let from = to - RECENT_MS;
        for (const raw of this._rawOrders.values()) {
            if (TERMINAL_STATUSES.has(raw.status)) continue;
            const entered = Date.parse(raw.enteredTime);
            if (Number.isFinite(entered) && entered - 1000 < from) from = entered - 1000;
        }
        return Math.max(from, to - ORDER_WINDOW_MS);
    }
    async _readRange(context, from, until, status, budget) {
        if (++budget.requests > 128) throw new Error('订单过多，无法完整读取，请在 Schwab 查看');
        const params = new URLSearchParams({ fromEnteredTime: new Date(from).toISOString(), toEnteredTime: new Date(until).toISOString(), maxResults: String(ORDER_LIMIT) });
        if (status) params.set('status', status);
        const data = await this._json(context, `/orders?${params}`);
        if (!Array.isArray(data)) throw new Error('Schwab 订单响应无效');
        if (data.length < ORDER_LIMIT) return data;
        // Schwab has a result cap but no cursor. Split the time range, overlap the
        // boundary, then deduplicate. Never report a truncated list as complete.
        if (until - from <= 1) throw new Error('订单结果达到上限，无法完整读取');
        const middle = Math.floor((from + until) / 2);
        return [...await this._readRange(context, from, middle, status, budget), ...await this._readRange(context, middle, until, status, budget)];
    }
    _storeOrders(all) {
        const mapped = [...all.values()].filter(o => o.orderLegCollection?.length).map(mapOrder).sort(byRecent);
        this._rawOrders = all;
        this._orders = new Map(mapped.map(order => [order.id, order]));
        return mapped;
    }
    async _readOrders(context, generation, incremental = false) {
        const to = Date.now();
        const budget = { requests: 0 };
        let raw;
        if (incremental) {
            raw = await this._readRange(context, this._incrementalFrom(to), to, '', budget);
        } else {
            // Startup: everything recent, plus older WORKING orders (the usual
            // long-lived GTC case) so they show up without the full history read.
            const recentFrom = to - STARTUP_MS;
            const [recent, working] = await Promise.all([
                this._readRange(context, recentFrom, to, '', budget),
                this._readRange(context, to - ORDER_WINDOW_MS, recentFrom, 'WORKING', budget),
            ]);
            raw = [...working, ...recent];
            this._historyFrom = recentFrom;
        }
        raw = flattenOrders(raw);
        this._checkContext(context);
        // An incremental read updates the recent part of the stored orders; older
        // orders outside its range are final and keep their stored state.
        const all = new Map(incremental ? this._rawOrders : []);
        for (const order of raw) all.set(String(order.orderId), order);
        if (!incremental) this._fullOrdersEpoch = context.epoch;
        const mapped = this._storeOrders(all);
        // Initial data is returned through orders()/ordersHistory(). Replaying
        // it through orderUpdate would announce old fills/cancellations again.
        this._orderSignatures ??= new Map(mapped.map(order => [order.id, orderSignature(order)]));
        this._ordersSnapshot = { epoch: context.epoch, generation, mapped };
        return mapped;
    }
    _scheduleHistory() {
        if (this._historyEpoch === this._epoch || this._historyTimer || this._destroyed) return;
        this._historyTimer = setTimeout(() => {
            this._historyTimer = undefined;
            // A background prefetch reports through _loadHistory and retries on a
            // later poll, but must not create an unhandled rejected promise.
            void this._loadHistory().catch(() => {});
        }, this._historyDelay);
    }
    // Reads the rest of the 180-day window once per account connection and adds
    // it silently: these orders are not news. A full update makes the library
    // re-read orders(), which adds older working orders found here.
    async _loadHistory() {
        if (this._historyEpoch === this._epoch || this._historyInflight?.epoch === this._epoch) return this._historyInflight?.promise;
        clearTimeout(this._historyTimer);
        this._historyTimer = undefined;
        const entry = { epoch: this._epoch };
        entry.promise = (async () => {
            try {
                await this._loadOrders();
                const context = await this._context();
                if (context.epoch !== entry.epoch) throw new Error('账户已切换，请重试');
                const until = this._historyFrom ?? Date.now();
                const raw = flattenOrders(await this._readRange(context, Date.now() - ORDER_WINDOW_MS, until, '', { requests: 0 }));
                this._checkContext(context);
                // Orders already stored came from a newer read; keep them.
                const all = new Map(this._rawOrders);
                let added = 0;
                for (const order of raw) {
                    const id = String(order.orderId);
                    if (!all.has(id)) { all.set(id, order); added++; }
                }
                this._historyEpoch = context.epoch;
                if (!added) return;
                const mapped = this._storeOrders(all);
                for (const order of mapped) if (!this._orderSignatures?.has(order.id)) this._orderSignatures?.set(order.id, orderSignature(order));
                if (this._ordersSnapshot?.epoch === context.epoch) this._ordersSnapshot = { ...this._ordersSnapshot, mapped };
                this.host.ordersFullUpdate();
            } catch (error) {
                // The next poll retries. Report a failure once per connection.
                if (entry.epoch === this._epoch && !this._destroyed && this._historyErrorEpoch !== entry.epoch) {
                    this._historyErrorEpoch = entry.epoch;
                    this._notify(error);
                }
                throw error;
            } finally {
                if (this._historyInflight === entry) this._historyInflight = undefined;
            }
        })();
        this._historyInflight = entry;
        return entry.promise;
    }
    async orders() {
        return (await this._loadOrders()).filter(order => order.status === WORKING);
    }
    async ordersHistory() {
        // The library asks for the history once when the account manager opens
        // and does not ask again on ordersFullUpdate(), so the answer waits for
        // the older range. orders() does not wait and shows working orders first.
        const initialContext = await this._context();
        await this._loadOrders();
        await this._loadHistory();
        const orders = await this._loadOrders();
        this._checkContext(initialContext);
        return orders.filter(order => order.status !== WORKING);
    }
    async executions(symbol) {
        const context = await this._context();
        const now = Date.now();
        const params = new URLSearchParams({ startDate: new Date(now - YEAR_MS).toISOString(), endDate: new Date(now).toISOString(), types: 'TRADE', symbol });
        const data = await this._json(context, `/transactions?${params}`);
        if (!Array.isArray(data)) throw new Error('Schwab 成交响应无效');
        return data.flatMap(transaction => (transaction.transferItems || []).filter(item => item.instrument?.assetType !== 'CURRENCY' && item.instrument?.symbol === symbol).map((item, index) => ({
            id: `${transaction.activityId}:${index}`, symbol, price: Number(item.price), side: item.amount > 0 ? 1 : -1,
            qty: Math.abs(item.amount), time: Date.parse(transaction.time) / 1000,
        })));
    }
    async _orderUpdate(context, id) {
        const raw = await this._json(context, `/orders/${encodeURIComponent(id)}`);
        const order = mapOrder(raw);
        this._rawOrders.set(order.id, raw);
        this._orders.set(order.id, order);
        this._orderSignatures?.set(order.id, orderSignature(order));
        this.host.orderUpdate(order);
    }
    async placeOrder(order) {
        const context = await this._context();
        const payload = buildOrder(order);
        const response = await this._response(context, '/orders', { method: 'POST', body: JSON.stringify(payload) }, '下单');
        const id = response.headers.get('Location')?.split('/').pop();
        // A successful write must not be reported as a failed order if reconciliation
        // fails afterwards: retrying the trade could duplicate a real execution.
        if (id) {
            try { await this._orderUpdate(context, id); } catch (error) { this._notify(error); }
        }
        this._scheduleRefresh();
        return id ? { orderId: id } : {};
    }
    async modifyOrder(order) {
        const context = await this._context();
        const original = await this._json(context, `/orders/${encodeURIComponent(order.id)}`);
        const payload = buildOrder(order, original);
        const response = await this._response(context, `/orders/${encodeURIComponent(order.id)}`, { method: 'PUT', body: JSON.stringify(payload) }, '修改订单');
        const id = response.headers.get('Location')?.split('/').pop();
        try {
            await this._orderUpdate(context, order.id);
            if (id) await this._orderUpdate(context, id);
        } catch (error) { this._notify(error); }
        this._scheduleRefresh();
    }
    async cancelOrder(orderId) {
        const context = await this._context();
        await this._response(context, `/orders/${encodeURIComponent(orderId)}`, { method: 'DELETE' }, '撤单');
        try { await this._orderUpdate(context, orderId); } catch (error) { this._notify(error); }
        this._scheduleRefresh();
    }
    async chartContextMenuActions(context) { return this.host.defaultContextMenuActions(context); }
    async getOrderDialogOptions() {
        return {
            customFields: [{
                inputType: 'ComboBox',
                id: 'session',
                title: '交易时段',
                saveToSettings: true,
                value: 'NORMAL',
                items: [
                    { text: '常规(9:30-16:00 ET)', value: 'NORMAL' },
                    { text: '盘前(7:00-9:25 ET)', value: 'AM' },
                    { text: '盘后(16:05-20:00 ET)', value: 'PM' },
                    { text: '延长时段(7:00-20:00 ET)', value: 'SEAMLESS' },
                ],
            }],
        };
    }
    subscribeEquity() { if (this._lastEquity != null) this.host.equityUpdate?.(this._lastEquity); }
    unsubscribeEquity() {}
    destroy() {
        this._destroyed = true;
        this._invalidate();
        clearInterval(this._poll);
        clearTimeout(this._refreshTimer);
        clearTimeout(this._historyTimer);
        this.events.removeEventListener('ACCT_ACTIVITY', this._onActivity);
        this.events.removeEventListener('SCHWAB_STREAM_READY', this._onReady);
        this.events.removeEventListener('SCHWAB_STREAM_CLOSED', this._onDisconnect);
    }
    accountManagerInfo() {
        return {
            accountTitle: 'Schwab',
            summary: this._ensureSummary(),
            orderColumns: ORDER_COLUMNS,
            orderColumnsSorting: { property: 'updateTime', asc: false },
            historyColumns: ORDER_COLUMNS,
            historyColumnsSorting: { property: 'updateTime', asc: false },
            positionColumns: [
                {
                    label: '标的',
                    formatter: 'symbol',
                    id: 'symbol',
                    dataFields: ['symbol', 'symbol', 'message'],
                },
                {
                    label: '方向',
                    id: 'direction',
                    dataFields: ['direction'],
                },
                {
                    label: '数量',
                    id: 'qty',
                    dataFields: ['qty'],
                    formatter: 'formatQuantity',
                },
                {
                    label: '均价',
                    id: 'avgPrice',
                    dataFields: ['avgPrice', "currency"],
                    formatter: 'formatPriceInCurrency',
                },
                {
                    label: '现价',
                    id: 'marketPrice',
                    dataFields: ['marketPrice', "currency"],
                    formatter: 'formatPriceInCurrency',
                },
                {
                    label: '已实现盈亏',
                    id: 'realizedPnl',
                    dataFields: ['realizedPnl', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: '未实现盈亏',
                    id: 'pl',
                    dataFields: ['pl', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: '当日盈亏',
                    id: 'dailyPnL',
                    dataFields: ['dailyPnL', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: '类型',
                    id: 'type',
                    dataFields: ['assetTypeLabel'],
                }
            ],
            pages: [
            ],
            contextMenuActions: async (_event, activePageActions = []) => [
                ...activePageActions,
                { text: '刷新持仓', action: () => this.host.positionsFullUpdate() },
                { text: '刷新订单', action: () => this.host.ordersFullUpdate() },
            ],
        }
    }
}
