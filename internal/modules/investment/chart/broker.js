import { schwabFetch } from './schwab.js';
import { buildOrder, flattenOrders, mapOrder, mapPositions } from './broker-models.js';

const ORDER_LIMIT = 3000;
const YEAR_MS = 365 * 24 * 60 * 60 * 1000;
const WORKING = 6;
const ORDER_COLUMNS = [
    { label: 'symbol', formatter: 'symbol', id: 'symbol', dataFields: ['symbol', 'symbol', 'message'] },
    { label: 'Status', id: 'status', dataFields: ['status'], formatter: 'status' },
    { label: 'Side', id: 'side', dataFields: ['side'], formatter: 'side' },
    { label: 'Type', id: 'type', formatter: 'type', dataFields: ['type'] },
    { label: 'limitPrice', id: 'limitPrice', dataFields: ['limitPrice', 'currency'], formatter: 'formatPriceInCurrency' },
    { label: 'stopPrice', id: 'stopPrice', dataFields: ['stopPrice', 'currency'], formatter: 'formatPriceInCurrency' },
    { label: 'Qty', id: 'qty', dataFields: ['qty'], formatter: 'formatQuantity' },
    { label: 'Filled', id: 'filledQty', dataFields: ['filledQty'], formatter: 'formatQuantity' },
    { label: 'avgPrice', id: 'avgPrice', dataFields: ['avgPrice', 'currency'], formatter: 'formatPriceInCurrency' },
];
const byRecent = (a, b) => (b.updateTime || 0) - (a.updateTime || 0);
// A missing broker timestamp falls back to Date.now() in mapOrder. It is not
// itself an order change and must not turn every poll into an update event.
const orderSignature = order => JSON.stringify({ ...order, updateTime: undefined });

function accountLabel(accountNumber) {
    const digits = String(accountNumber).replace(/\D/g, '');
    return digits ? `Schwab …${digits.slice(-4)}` : 'Schwab';
}

export default class Broker {
    constructor(host, { request = schwabFetch, events = window, pollInterval = 15000 } = {}) {
        this.host = host;
        this.request = request;
        this.events = events;
        this._accounts = [];
        this._account = '';
        this._epoch = 0;
        this._connectionStatus = 2;
        this._orders = new Map();
        this._rawOrders = new Map();
        this._ordersSnapshot = undefined;
        this._orderSignatures = undefined;
        this._controller = new AbortController();
        this._onActivity = () => this._scheduleRefresh();
        this._onReady = () => { this._ready = this._init(); };
        this._onDisconnect = () => this._setStatus(2);
        events.addEventListener('ACCT_ACTIVITY', this._onActivity);
        events.addEventListener('SCHWAB_STREAM_READY', this._onReady);
        events.addEventListener('SCHWAB_STREAM_CLOSED', this._onDisconnect);
        // The host installs its broker connection asynchronously after broker_factory returns.
        // Calling connectionStatusUpdate in this constructor aborts initialization
        // inside TradingView before even requesting the account list. A new task
        // lets the host's promise continuations finish; a microtask alone is too early.
        this._ready = new Promise(resolve => setTimeout(resolve, 0)).then(() => {
            if (!this._destroyed) return this._init();
        });
        if (pollInterval > 0) this._poll = setInterval(() => this._scheduleRefresh(), pollInterval);
    }

    _setStatus(status) { this._connectionStatus = status; this.host.connectionStatusUpdate(status); }
    _invalidate() {
        this._epoch++;
        this._controller.abort();
        this._controller = new AbortController();
        this._orders.clear();
        this._rawOrders.clear();
        this._ordersSnapshot = undefined;
        this._orderSignatures = undefined;
    }
    async _init() {
        const preferredAccount = this._account;
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
            this._setStatus(1);
            this.host.currentAccountUpdate();
            this._scheduleRefresh();
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
    _scheduleRefresh() {
        if (this._refreshTimer || !this._account || this._destroyed) return;
        this._refreshTimer = setTimeout(() => {
            this._refreshTimer = undefined;
            void this._refresh();
        }, 200);
    }
    async _refresh() {
        const epoch = this._epoch;
        try {
            // Account activity is an invalidation signal. REST is the source of truth,
            // including fills and events with no identifiable account in their payload.
            this._ordersSnapshot = undefined;
            const [orders, positions] = await Promise.all([this._loadOrders(), this.positions()]);
            if (epoch !== this._epoch) return;
            for (const order of orders) {
                const signature = orderSignature(order);
                if (this._orderSignatures.get(order.id) === signature) continue;
                this._orderSignatures.set(order.id, signature);
                this.host.orderUpdate(order);
            }
            positions.forEach(position => this.host.positionUpdate?.(position));
        } catch (error) { if (epoch === this._epoch) this._notify(error); }
    }
    connectionStatus() { return this._connectionStatus; }
    currentAccount() { return this._account; }
    async accountsMetainfo() { await this._ready; return this._accounts; }
    setCurrentAccount(id) {
        if (!this._accounts.some(a => a.id === id)) throw new Error('未知的交易账户');
        if (id === this._account) return;
        this._invalidate();
        this._account = id;
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
        const data = await this._json(context, '?fields=positions');
        const account = data.securitiesAccount;
        if (!account || (account.positions != null && !Array.isArray(account.positions))) throw new Error('Schwab 持仓响应无效');
        this._applyBalances(account);
        return mapPositions(account.positions || []);
    }
    async _loadOrders() {
        const context = await this._context();
        if (this._ordersSnapshot?.epoch === context.epoch) return this._ordersSnapshot.mapped;
        const to = Date.now();
        let requests = 0;
        const readRange = async (from, until) => {
            if (++requests > 128) throw new Error('订单过多，无法完整读取，请在 Schwab 查看');
            const params = new URLSearchParams({ fromEnteredTime: new Date(from).toISOString(), toEnteredTime: new Date(until).toISOString(), maxResults: String(ORDER_LIMIT) });
            const data = await this._json(context, `/orders?${params}`);
            if (!Array.isArray(data)) throw new Error('Schwab 订单响应无效');
            if (data.length < ORDER_LIMIT) return data;
            // Schwab has a result cap but no cursor. Split the time range, overlap the
            // boundary, then deduplicate. Never report a truncated list as complete.
            if (until - from <= 1) throw new Error('订单结果达到上限，无法完整读取');
            const middle = Math.floor((from + until) / 2);
            return [...await readRange(from, middle), ...await readRange(middle, until)];
        };
        const raw = flattenOrders(await readRange(to - YEAR_MS, to));
        this._checkContext(context);
        const all = new Map(raw.map(order => [String(order.orderId), order]));
        const mapped = [...all.values()].filter(o => o.orderLegCollection?.length).map(mapOrder).sort(byRecent);
        this._rawOrders = all;
        this._orders = new Map(mapped.map(order => [order.id, order]));
        // Initial data is returned through orders()/ordersHistory(). Replaying
        // it through orderUpdate would announce old fills/cancellations again.
        this._orderSignatures ??= new Map(mapped.map(order => [order.id, orderSignature(order)]));
        this._ordersSnapshot = { epoch: context.epoch, mapped };
        return mapped;
    }
    async orders() {
        return (await this._loadOrders()).filter(order => order.status === WORKING);
    }
    async ordersHistory() {
        return (await this._loadOrders()).filter(order => order.status !== WORKING);
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
                    label: 'symbol',
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
                    label: 'Qty',
                    id: 'qty',
                    dataFields: ['qty'],
                    formatter: 'formatQuantity',
                },
                {
                    label: 'avgPrice',
                    id: 'avgPrice',
                    dataFields: ['avgPrice', "currency"],
                    formatter: 'formatPriceInCurrency',
                },
                {
                    label: 'marketPrice',
                    id: 'marketPrice',
                    dataFields: ['marketPrice', "currency"],
                    formatter: 'formatPriceInCurrency',
                },
                {
                    label: 'realized P&L',
                    id: 'realizedPnl',
                    dataFields: ['realizedPnl', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: 'unrealized P&L',
                    id: 'pl',
                    dataFields: ['pl', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: 'Daily P&L',
                    id: 'dailyPnL',
                    dataFields: ['dailyPnL', "currency"],
                    formatter: 'profitInInstrumentCurrency',
                },
                {
                    label: 'type',
                    id: 'type',
                    dataFields: ['type'],
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
