import { schwabFetch } from './schwab.js';
import { futuFetch, NIGHT_RESOLUTIONS, seriesKey, usesFutuTicks } from './futu.js';
import { identifyAssetType, getAssetSubsessions } from './datafeed-symbols.js';
import { mapSchwabQuote, updateSchwabBars } from './datafeed-realtime.js';
import { getFutuNightBars, getSchwabBars, mergeSessionBars } from './datafeed-bars.js';

// Owns TradingView callbacks, connection epochs, caches and subscription lifetime.
// Provider conversion and history normalization live in the datafeed-* modules.
export default class Datafeed {
    constructor({ request = schwabFetch, futuRequest = futuFetch, events = window, stream = window.ws, futuStream, resetCharts = () => {} } = {}) {
        this.request = request;
        this.futuRequest = futuRequest;
        this.events = events;
        this.streamReady = stream?.ready === true;
        this.streamEpoch = 0;
        this.SUB = new Map();
        this.futuSUB = new Map();
        this.resetCharts = resetCharts;
        this.futuStream = futuStream;
        this.overlayCache = { enabled: false };
        this.overlayCachedAt = 0;

        this.quotesListeners = new Map();
        this.barListeners = new Map();

        this.quote_cache = new Map();
        this.snapshot_cache = new Map();
        this.snapshotTimes = new Map();
        this.latestBars = new Map();
        this.cumulativeVolumes = new Map();
        this.onLevelOne = (e) => {
            const raw = e.detail;
            if (!raw || !Array.isArray(raw.content)) return;
            const timestamp = Number(raw.timestamp ?? Date.now());
            if (!Number.isFinite(timestamp)) return;

            for (const item of raw.content) {
                const symbolKey = item.key;
                if (!symbolKey) continue;
                if (timestamp < (this.snapshotTimes.get(symbolKey) ?? 0)) continue;
                this.snapshotTimes.set(symbolKey, timestamp);

                const existing = this.snapshot_cache.get(symbolKey) || {};

                // Partial updates must not overwrite valid fields with nulls.
                const cleanedItem = {};
                for (const [k, v] of Object.entries(item)) {
                    if (v !== null && v !== undefined) {
                        cleanedItem[k] = v;
                    }
                }

                const snapshot = {
                    ...existing,
                    ...cleanedItem,
                };
                this.snapshot_cache.set(symbolKey, snapshot);

                const quote = mapSchwabQuote(raw.service, snapshot, timestamp);

                if (quote) {
                    this.quote_cache.set(symbolKey, quote);

                    if (this.quotesListeners.size > 0) {
                        this.quotesListeners.forEach(({ symbols, callback }) => {
                            if (!symbols.has(symbolKey)) return;
                            this.quote_cache.set(symbolKey, quote)
                            if (typeof callback === 'function') {
                                callback([{ s: 'ok', n: symbolKey, v: quote }]);
                            }
                        });
                    }
                    if (this.barListeners?.size > 0) {
                        updateSchwabBars(this.barListeners, this.latestBars, this.cumulativeVolumes, symbolKey, quote, timestamp);
                    }
                }
            }
        };
        this.onStreamReady = () => {
            this.streamReady = true;
            this.streamEpoch++;
            this.SUB.clear();
            this.snapshot_cache.clear();
            this.snapshotTimes.clear();
            this.quote_cache.clear();
            this.cumulativeVolumes.clear();
            this.latestBars.clear();
            for (const listener of this.barListeners.values()) listener.onReset?.();
            this.resetCharts();
            this.queueSubscriptions();
        };
        this.onStreamClosed = () => { this.streamReady = false; this.streamEpoch++; this.SUB.clear(); };
        this.onFutuKL = (e) => {
            const item = e.detail;
            if (!item?.symbol || !item.bar) return;
            const bar = { ...item.bar };
            for (const listener of this.barListeners.values()) {
                if (listener.symbol !== item.symbol || listener.resolution !== item.resolution) continue;
                if (!usesFutuTicks(listener.subsessionId, bar.time)) continue;
                const key = seriesKey(item.symbol, listener.subsessionId);
                const cache = this.latestBars.get(key) ?? this.latestBars.set(key, new Map()).get(key);
                cache.set(item.resolution, { ...bar });
                listener.onTick?.({ ...bar });
            }
        };
        this.onFutuQuote = (e) => {
            const item = e.detail;
            if (!item?.symbol || !item.quote) return;
            this.futuOvernight = this.futuOvernight ?? new Map();
            this.futuOvernight.set(item.symbol, item.quote);
        };
        this.onFutuReady = () => {
            this.futuSUB.clear();
            for (const listener of this.barListeners.values()) {
                if (listener.subsessionId === 'night' || listener.subsessionId === '24h') listener.onReset?.();
            }
            this.queueFutuSubscriptions();
        };
        events.addEventListener('LEVELONE_ANY', this.onLevelOne);
        events.addEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        events.addEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
        events.addEventListener('FUTU_KL', this.onFutuKL);
        events.addEventListener('FUTU_QUOTE', this.onFutuQuote);
        events.addEventListener('FUTU_STREAM_READY', this.onFutuReady);
    }

    async ensureFutuOverlay() {
        const now = Date.now();
        if (this.overlayCache && now - this.overlayCachedAt < 30_000) return this.overlayCache;
        try {
            const response = await this.futuRequest('');
            const body = await response.json();
            this.overlayCache = { enabled: body.enabled === true && body.overnightEnabled === true };
        } catch {
            this.overlayCache = { enabled: false };
        }
        this.overlayCachedAt = now;
        if (this.overlayCache.enabled) this.futuStream?.start?.();
        else this.futuStream?.stop?.();
        return this.overlayCache;
    }

    onReady(callback) {
        // TradingView needs a separate task, not a fixed startup delay.
        setTimeout(() => {
            callback({
                supported_resolutions: ['1', '5', '10', '15', '30', '1D', '1W', '1M'],
            });
        }, 0);
    }

    async searchSymbols(userInput, exchange, symbolType, onResult) {
        if (!userInput || userInput.trim() === '') {
            onResult([]);
            return;
        }

        const url = "/marketdata/v1/instruments?symbol=" + encodeURIComponent(userInput) + "&projection=symbol-search";
        try {
            const response = await this.request(url);
            if (!response.ok) {
                console.error("搜索请求失败: " + response.status);
                onResult([]);
                return;
            }
            const data = await response.json();
            const items = Array.isArray(data) ? data : (data.instruments || []);
            const searchResults = items.map((item) => {
                return {
                    symbol: item.symbol,
                    description: item.description || item.symbol,
                    exchange: item.exchange || "NASDAQ",
                    type: item.assetType || "stock",
                };
            });
            onResult(searchResults);
        } catch (err) {
            onResult([]);
        }
    }

    identifyAssetType(symbolName) {
        return identifyAssetType(symbolName);
    }

    getAssetSubsessions(assetType, overlayEnabled = false) {
        return getAssetSubsessions(assetType, overlayEnabled);
    }

    async resolveSymbol(symbolName, onResolve, onError, extension) {
        try {
            const cleanSymbol = symbolName.trim();

            const assetType = this.identifyAssetType(cleanSymbol);
            const overlay = await this.ensureFutuOverlay();
            const subsessions = this.getAssetSubsessions(assetType, overlay.enabled);

            const requestedSessionId = (extension && extension.session) ? extension.session : "regular";
            const foundSub = subsessions.find(item => item.id === requestedSessionId) || subsessions[0];

            const symbolInfo = {
                name: cleanSymbol,
                description: cleanSymbol,
                type: assetType,
                exchange: assetType === "FOREX" ? "FX" : "US",
                listed_exchange: assetType === "FOREX" ? "FX" : "US",
                format: "price",
                pricescale: assetType === "FOREX" ? 100000 : 100, // 外汇保留更多位小数
                minmov: 1,
                has_intraday: true,
                has_weekly_and_monthly: true,
                data_status: "streaming",
                currency_code: "USD",
                timezone: "America/New_York",
                subsessions: subsessions,
                session: foundSub.session,
                subsession_id: foundSub.id,
            };

            // TradingView expects symbol resolution on a later task.
            setTimeout(() => {
                onResolve(symbolInfo);
            }, 0);

        } catch (err) {
            if (typeof onError === "function") {
                onError(err);
            }
        }
    }

    async getBars(symbolInfo, resolution, periodParams, onResult, onError) {
        const { name, subsession_id } = symbolInfo
        const session = subsession_id || 'regular';
        const { from, to } = periodParams
        const epoch = this.streamEpoch;
        try {
            if (session === 'night') {
                const overlay = await this.ensureFutuOverlay();
                if (!overlay.enabled || this.identifyAssetType(name) !== 'EQUITIES' || !NIGHT_RESOLUTIONS.has(resolution)) {
                    onResult([], { noData: true });
                    return;
                }
                const bars = await this.getFutuNightBars(name, resolution, from, to, epoch);
                this.seedLatest(name, session, resolution, bars);
                onResult(bars, { noData: bars.length === 0 });
                return;
            }
            if (session === '24h' && NIGHT_RESOLUTIONS.has(resolution)) {
                const overlay = await this.ensureFutuOverlay();
                if (overlay.enabled && this.identifyAssetType(name) === 'EQUITIES') {
                    const [schwab, night] = await Promise.all([
                        this.getSchwabBars(name, resolution, periodParams, epoch, true),
                        this.getFutuNightBars(name, resolution, from, to, epoch),
                    ]);
                    if (this.destroyed || epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取历史数据');
                    const merged = mergeSessionBars(schwab, night);
                    this.seedLatest(name, session, resolution, merged);
                    onResult(merged, { noData: merged.length === 0 });
                    return;
                }
            }
            const bars = await this.getSchwabBars(name, resolution, periodParams, epoch, session !== 'regular');
            this.seedLatest(name, session, resolution, bars);
            onResult(bars, { noData: bars.length === 0 });
        } catch (err) {
            if (typeof onError === "function") onError(err);
        }
    }

    seedLatest(name, session, resolution, bars) {
        if (!bars.length) return;
        const key = seriesKey(name, session);
        const symbolCache = this.latestBars.get(key) ?? this.latestBars.set(key, new Map()).get(key);
        const latest = bars.at(-1), cached = symbolCache.get(resolution);
        if (!cached || latest.time > cached.time) symbolCache.set(resolution, { ...latest });
    }

    async getFutuNightBars(name, resolution, from, to, epoch) {
        return getFutuNightBars(path => this.futuRequest(path), name, resolution, from, to, () => {
            if (this.destroyed || epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取历史数据');
        });
    }

    async getSchwabBars(name, resolution, periodParams, epoch, extended) {
        return getSchwabBars(path => this.request(path), name, resolution, periodParams, extended, () => {
            if (this.destroyed || epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取历史数据');
        });
    }

    subscribeBars(symbolInfo, resolution, onTick, listenerGuid, onReset) {
        const subsessionId = symbolInfo.subsession_id || 'regular';
        this.barListeners.set(listenerGuid, { symbol: symbolInfo.name, resolution, onTick, onReset, subsessionId });
        this.queueSubscriptions();
        if (subsessionId === 'night' || subsessionId === '24h') this.queueFutuSubscriptions();
    }
    unsubscribeBars(listenerGuid) {
        const listener = this.barListeners.get(listenerGuid);
        this.barListeners.delete(listenerGuid);
        if (listener && (listener.subsessionId === 'night' || listener.subsessionId === '24h')) {
            void this.futuRequest('/quote/ws/command', {
                method: 'POST', body: JSON.stringify({ command: 'UNSUB', symbol: listener.symbol, resolution: listener.resolution })
            }).catch(() => {});
        }
    }
    unsubscribeQuotes(listenerGuid) { this.quotesListeners.delete(listenerGuid); }
    subscribeQuotes(symbols, fastSymbols, callback, listenerGuid) {
        this.quotesListeners.set(listenerGuid, { symbols: new Set([...symbols, ...fastSymbols]), callback });
        this.queueSubscriptions();
    }
    queueSubscriptions(delay = 0) {
        if (!this.streamReady || this.subscribeTimer || this.destroyed) return;
        this.subscribeTimer = setTimeout(async () => {
            this.subscribeTimer = undefined;
            if (!this.streamReady || this.destroyed) return;
            const epoch = this.streamEpoch;
            const symbols = new Set([...this.barListeners.values()].map(listener => listener.symbol));
            for (const listener of this.quotesListeners.values()) for (const symbol of listener.symbols) symbols.add(symbol);
            const fields = {
                LEVELONE_EQUITIES: '0,1,2,3,4,7,8,10,11,12,15,17,25,29,31,33,43',
                LEVELONE_OPTIONS: '0,2,3,4,5,6,7,8,15,37,45,46',
                LEVELONE_FUTURES: '0,1,2,3,8,10,12,13,14,18,19,20,24',
                LEVELONE_FUTURES_OPTIONS: '0,1,2,3,8,10,12,13,14,17,19',
                LEVELONE_FOREX: '0,1,2,3,6,10,11,12,15,29,33'
            };
            const groups = new Map();
            for (const symbol of symbols) {
                if (this.SUB.has(symbol)) continue;
                this.SUB.set(symbol, 'pending');
                const service = 'LEVELONE_' + this.identifyAssetType(symbol);
                if (!groups.has(service)) groups.set(service, []);
                groups.get(service).push(symbol);
            }
            for (const [service, keys] of groups) {
                if (!this.streamReady || epoch !== this.streamEpoch || this.destroyed) break;
                try {
                    const response = await this.request('/marketdata/ws/command', {
                        method: 'POST', body: JSON.stringify({ service, command: 'ADD', keys: keys.join(','), fields: fields[service] })
                    });
                    if (!response.ok) throw new Error('订阅失败');
                    if (epoch === this.streamEpoch) keys.forEach(key => this.SUB.set(key, 'subscribed'));
                } catch {
                    if (epoch === this.streamEpoch) {
                        keys.forEach(key => this.SUB.delete(key));
                        this.queueSubscriptions(2000);
                    }
                }
            }
        }, delay);
    }
    queueFutuSubscriptions(delay = 0) {
        if (this.destroyed || this.futuTimer) return;
        this.futuTimer = setTimeout(async () => {
            this.futuTimer = undefined;
            if (this.destroyed) return;
            const overlay = await this.ensureFutuOverlay();
            if (!overlay.enabled || this.destroyed) return;
            for (const listener of this.barListeners.values()) {
                if (listener.subsessionId !== 'night' && listener.subsessionId !== '24h') continue;
                if (!NIGHT_RESOLUTIONS.has(listener.resolution)) continue;
                const key = listener.symbol + '\0' + listener.resolution;
                if (this.futuSUB.has(key)) continue;
                this.futuSUB.set(key, 'pending');
                try {
                    const response = await this.futuRequest('/quote/ws/command', {
                        method: 'POST', body: JSON.stringify({ command: 'ADD', symbol: listener.symbol, resolution: listener.resolution })
                    });
                    if (!response.ok) throw new Error('夜盘订阅失败');
                    this.futuSUB.set(key, 'subscribed');
                } catch {
                    this.futuSUB.delete(key);
                    this.queueFutuSubscriptions(2000);
                }
            }
        }, delay);
    }
    destroy() {
        this.destroyed = true;
        clearTimeout(this.subscribeTimer);
        clearTimeout(this.futuTimer);
        this.events.removeEventListener('LEVELONE_ANY', this.onLevelOne);
        this.events.removeEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        this.events.removeEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
        this.events.removeEventListener('FUTU_KL', this.onFutuKL);
        this.events.removeEventListener('FUTU_QUOTE', this.onFutuQuote);
        this.events.removeEventListener('FUTU_STREAM_READY', this.onFutuReady);
        for (const listener of this.barListeners.values()) {
            if (listener.subsessionId === 'night' || listener.subsessionId === '24h') {
                void this.futuRequest('/quote/ws/command', {
                    method: 'POST', body: JSON.stringify({ command: 'UNSUB', symbol: listener.symbol, resolution: listener.resolution })
                }).catch(() => {});
            }
        }
        this.barListeners.clear();
        this.quotesListeners.clear();
        this.futuStream?.stop?.();
    }

    async getQuotes(symbols, onDataCallback, onErrorCallback) {
        try {
            const quotePromises = symbols.map(async (symbol) => {

                const quote = this.quote_cache.get(symbol);
                return {
                    s: "ok",
                    n: String(symbol),
                    v: {
                        ...(quote || {})
                    }
                };
            });
            const results = await Promise.all(quotePromises);
            onDataCallback(results);
        } catch (error) {
            onErrorCallback(error);
        }
    }
}
