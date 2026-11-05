import { schwabFetch } from './schwab.js';
import { EQUITY_24H_SESSION, futuFetch, futuResponseError, futuSessionContains, overnightFetch, NIGHT_RESOLUTIONS, seriesKey, usesFutuTicks } from './futu.js';
import { identifyAssetType, getAssetSubsessions } from './datafeed-symbols.js';
import { isSchwabTradeUpdate, mapSchwabQuote, seedSchwabVolumeBaseline, updateSchwabBars } from './datafeed-realtime.js';
import { futuBarTime, get24HourBars, getFutuBars, getFutuNightBars, getSchwabBars } from './datafeed-bars.js';
import { futuQuoteValues, mergeQuoteValues, quoteData, schwabRestQuoteValues, schwabStreamQuoteValues } from './datafeed-quotes.js';

const SUPPORTED_RESOLUTIONS = ['1', '5', '15', '30', '1D', '1W', '1M'];
const SUBSCRIBE_BATCH_MS = 100;

function olderFutuGeneration(candidate, current) {
    const parts = generation => {
        const value = String(generation);
        const separator = value.lastIndexOf(':');
        if (separator < 1 || !/^\d+$/.test(value.slice(separator + 1))) return null;
        return { instance: value.slice(0, separator), sequence: BigInt(value.slice(separator + 1)) };
    };
    const next = parts(candidate), active = parts(current);
    return next !== null && active !== null && next.instance === active.instance && next.sequence < active.sequence;
}

// Owns TradingView callbacks, connection epochs, caches and subscription lifetime.
// Provider conversion and history normalization live in the datafeed-* modules.
export default class Datafeed {
    constructor({ provider = 'schwab', request = schwabFetch, futuRequest = futuFetch, overnightRequest = overnightFetch, events = window, stream = window.ws, futuStream, resetCharts = () => {}, onSessionResolved = () => {}, now = Date.now } = {}) {
        this.provider = provider;
        this.controller = new AbortController();
        const scoped = request => (path, init = {}) => request(path, {
            ...init, signal: init.signal ? AbortSignal.any([init.signal, this.controller.signal]) : this.controller.signal,
        });
        this.request = scoped(request);
        this.futuRequest = scoped(futuRequest);
        this.overnightRequest = overnightRequest;
        this.events = events;
        this.now = now;
        this.streamReady = stream?.ready === true;
        this.streamEpoch = 0;
        this.SUB = new Map();
        this.futuSUB = new Map();
        this.futuDesired = new Set();
        this.futuOperations = new Map();
        this.futuRetryTimers = new Map();
        this.futuEpoch = 0;
        this.futuBackendGeneration = futuStream?.ready === true ? futuStream.generation : undefined;
        this.futuOwner = futuStream?.ready === true ? futuStream.owner : undefined;
        this.futuRetiredGenerations = new Set();
        this.futuConnected = futuStream?.ready === true;
        this.resetCharts = resetCharts;
        this.onSessionResolved = onSessionResolved;
        this.futuStream = futuStream;
        this.overlayCache = { enabled: false };
        this.overlayCachedAt = 0;
        this.overlayInflight = undefined;

        this.quotesListeners = new Map();
        this.barListeners = new Map();

        this.quote_cache = new Map();
        this.schwabQuotes = new Map();
        this.futuQuotes = new Map();
        this.futuQuoteTimes = new Map();
        this.futuOvernight = new Map();
        this.quoteRequests = new Map();
        this.snapshot_cache = new Map();
        this.snapshotTimes = new Map();
        this.latestBars = new Map();
        this.cumulativeVolumes = new Map();
        this.cumulativeVolumeDates = new Map();
        this.onLevelOne = (e) => {
            if (this.destroyed || this.provider !== 'schwab') return;
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
                const isTradeUpdate = isSchwabTradeUpdate(raw.service, item, existing);

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
                    this.schwabQuotes.set(symbolKey, { ...this.schwabQuotes.get(symbolKey), ...schwabStreamQuoteValues(raw.service, snapshot, quote) });
                    this.publishQuote(symbolKey);
                    if (this.barListeners?.size > 0) {
                        if (isTradeUpdate) {
                            updateSchwabBars(this.barListeners, this.latestBars, this.cumulativeVolumes, symbolKey, quote, timestamp, this.cumulativeVolumeDates);
                        } else {
                            seedSchwabVolumeBaseline(this.barListeners, this.cumulativeVolumes, this.cumulativeVolumeDates, symbolKey, quote.volume, timestamp);
                        }
                    }
                }
            }
        };
        this.onStreamReady = () => {
            if (this.destroyed || this.provider !== 'schwab') return;
            const firstReady = this.streamEpoch === 0 && !this.streamReady;
            this.streamReady = true;
            if (firstReady) {
                this.queueSubscriptions();
                return;
            }
            this.streamEpoch++;
            this.SUB.clear();
            this.snapshot_cache.clear();
            this.snapshotTimes.clear();
            this.quote_cache.clear();
            this.schwabQuotes.clear();
            this.quoteRequests.clear();
            this.cumulativeVolumes.clear();
            this.cumulativeVolumeDates.clear();
            this.latestBars.clear();
            for (const listener of this.barListeners.values()) listener.onReset?.();
            this.resetCharts();
            this.queueSubscriptions();
        };
        this.onStreamClosed = () => {
            if (this.provider !== 'schwab' || this.destroyed) return;
            this.streamReady = false; this.streamEpoch++; this.SUB.clear();
        };
        this.onFutuKL = (e) => {
            const item = e.detail;
            if (!item?.symbol || !item.bar) return;
            const bar = { ...item.bar, time: this.provider === 'futu' ? futuBarTime(item.resolution, Number(item.bar.time)) : item.bar.time };
            if (this.destroyed || !Object.values(bar).every(Number.isFinite)) return;
            for (const listener of this.barListeners.values()) {
                if (listener.symbol !== item.symbol || listener.resolution !== item.resolution) continue;
                if (this.provider === 'futu') {
                    if (!NIGHT_RESOLUTIONS.has(item.resolution)) {
                        if (listener.subsessionId !== 'regular') continue;
                    } else if (!futuSessionContains(listener.subsessionId, bar.time, this.overlayCache.enabled)) continue;
                } else if (!usesFutuTicks(listener.subsessionId, bar.time)) continue;
                const key = seriesKey(item.symbol, listener.subsessionId);
                const cache = this.latestBars.get(key) ?? this.latestBars.set(key, new Map()).get(key);
                const previous = cache.get(item.resolution);
                if (previous && bar.time < previous.time) continue;
                cache.set(item.resolution, { ...bar });
                listener.onTick?.({ ...bar });
            }
        };
        this.onFutuQuote = (e) => {
            const item = e.detail;
            if (this.destroyed || !item?.symbol || !item.quote) return;
            if (!this.hasFutuListener(item.symbol, '')) return;
            if (this.provider === 'futu') {
                const quote = futuQuoteValues(item.quote, this.overlayCache.enabled);
                if (!Number.isFinite(quote.lp)) return;
                const time = Number(item.quote.time);
                if (Number.isFinite(time) && time < (this.futuQuoteTimes.get(item.symbol) ?? 0)) return;
                if (item.snapshot && this.futuQuotes.has(item.symbol)) return;
                if (Number.isFinite(time)) this.futuQuoteTimes.set(item.symbol, time);
                this.futuQuotes.set(item.symbol, quote);
                this.publishQuote(item.symbol);
                return;
            }
            if (this.mergedQuote(item.symbol).cs !== 'night') return;
            // A delayed initial snapshot cannot replace a live quote.
            if (item.snapshot && this.futuOvernight.has(item.symbol)) return;
            this.futuOvernight.set(item.symbol, { ...item.quote });
            this.publishQuote(item.symbol);
        };
        this.onFutuClosed = () => {
            this.futuEpoch++;
            this.futuConnected = false;
            const symbols = [...new Set([...this.futuOvernight.keys(), ...this.futuQuotes.keys()])];
            this.futuOvernight.clear();
            this.futuQuotes.clear();
            this.futuQuoteTimes.clear();
            if (this.provider === 'futu') this.quoteRequests.clear();
            symbols.forEach(symbol => this.publishQuote(symbol));
            for (const [key] of this.latestBars) {
                if (this.provider === 'futu' || key.endsWith(`\0night`) || key.endsWith(`\0${EQUITY_24H_SESSION}`)) this.latestBars.delete(key);
            }
        };
        this.onFutuReady = (event) => {
            const generation = event?.detail?.generation;
            const owner = event?.detail?.owner;
            if (this.isStaleFutuGeneration(generation)) return;
            if (this.futuConnected) this.futuEpoch++;
            this.futuConnected = true;
            this.observeFutuGeneration(generation, owner);
            for (const listener of this.barListeners.values()) {
                if (this.provider === 'futu' || listener.subsessionId === 'night' || listener.subsessionId === EQUITY_24H_SESSION) {
                    this.latestBars.delete(seriesKey(listener.symbol, listener.subsessionId));
                    listener.onReset?.();
                }
            }
            if (this.provider === 'futu') this.resetCharts();
            this.queueFutuSubscriptions();
        };
        events.addEventListener('LEVELONE_ANY', this.onLevelOne);
        events.addEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        events.addEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
        events.addEventListener('FUTU_KL', this.onFutuKL);
        events.addEventListener('FUTU_QUOTE', this.onFutuQuote);
        events.addEventListener('FUTU_STREAM_READY', this.onFutuReady);
        events.addEventListener('FUTU_STREAM_CLOSED', this.onFutuClosed);
    }

    async ensureFutuOverlay() {
        const now = Date.now();
        if (this.destroyed) return this.overlayCache;
        if (this.overlayCache && now - this.overlayCachedAt < 30_000) return this.overlayCache;
        if (this.overlayInflight) return this.overlayInflight;
        const pending = (async () => {
            let overlay;
            try {
                const response = await this.overnightRequest();
                if (!response.ok) throw new Error('读取夜盘设置失败');
                const body = await response.json();
                overlay = { enabled: body.enabled === true && body.providerEnabled === true, providerEnabled: body.providerEnabled === true };
            } catch {
                overlay = { enabled: false };
            }
            if (this.destroyed) return overlay;
            this.overlayCache = overlay;
            this.overlayCachedAt = Date.now();
            if (!this.destroyed) {
                if (this.provider === 'futu' && overlay.providerEnabled) this.futuStream?.start?.();
                else if (!overlay.enabled) this.futuStream?.stop?.();
                if (!overlay.enabled) {
                    this.futuOvernight.clear();
                    for (const [symbol, quote] of this.futuQuotes) if (quote.cs === 'night') this.futuQuotes.delete(symbol);
                }
                for (const symbol of this.quote_cache.keys()) this.publishQuote(symbol);
            }
            return overlay;
        })();
        this.overlayInflight = pending;
        try {
            return await pending;
        } finally {
            if (this.overlayInflight === pending) this.overlayInflight = undefined;
        }
    }

    onReady(callback) {
        // TradingView needs a separate task, not a fixed startup delay.
        setTimeout(() => {
            callback({
                supported_resolutions: [...SUPPORTED_RESOLUTIONS],
            });
        }, 0);
    }

    async searchSymbols(userInput, exchange, symbolType, onResult) {
        if (!userInput || userInput.trim() === '') {
            onResult([]);
            return;
        }

        const epoch = this.streamEpoch;
        const url = this.provider === 'futu' ? '/symbols?query=' + encodeURIComponent(userInput)
            : '/marketdata/v1/instruments?symbol=' + encodeURIComponent(userInput) + '&projection=symbol-search';
        try {
            const response = await (this.provider === 'futu' ? this.futuRequest(url) : this.request(url));
            if (this.destroyed || epoch !== this.streamEpoch) return;
            if (!response.ok) {
                console.error("搜索请求失败: " + response.status);
                onResult([]);
                return;
            }
            const data = await response.json();
            if (this.destroyed || epoch !== this.streamEpoch) return;
            const items = this.provider === 'futu' ? data.items : Array.isArray(data) ? data : (data.instruments || []);
            const searchResults = (items || []).map((item) => {
                return {
                    symbol: item.symbol,
                    description: item.description || item.symbol,
                    exchange: item.exchange || "NASDAQ",
                    type: this.provider === 'futu' ? item.type : item.assetType || 'stock',
                };
            });
            onResult(searchResults);
        } catch (err) {
            if (!this.destroyed && epoch === this.streamEpoch) onResult([]);
        }
    }

    identifyAssetType(symbolName) {
        return identifyAssetType(symbolName);
    }

    getAssetSubsessions(assetType, overlayEnabled = false) {
        return getAssetSubsessions(assetType, overlayEnabled);
    }

    async resolveSymbol(symbolName, onResolve, onError, extension) {
        const epoch = this.streamEpoch;
        try {
            let cleanSymbol = symbolName.trim();
            let instrument;
            if (this.provider === 'futu') {
                cleanSymbol = cleanSymbol.split(':').at(-1).toUpperCase();
                if (this.identifyAssetType(cleanSymbol) !== 'EQUITIES') throw new Error('富途看盘仅支持美股和 ETF');
                const response = await this.futuRequest('/symbols?query=' + encodeURIComponent(cleanSymbol));
                if (!response.ok) throw await futuResponseError(response, '查询富途标的失败');
                const body = await response.json();
                instrument = body.items?.find(item => item.symbol === cleanSymbol);
                if (!instrument) throw new Error('未找到美股或 ETF，请输入完整代码');
            }
            if (this.destroyed || epoch !== this.streamEpoch) return;
            const assetType = this.identifyAssetType(cleanSymbol);
            const overlay = await this.ensureFutuOverlay();
            const subsessions = this.getAssetSubsessions(assetType, overlay.enabled);

            const requestedSessionId = (extension && extension.session) ? extension.session : "regular";
            const foundSub = subsessions.find(item => item.id === requestedSessionId) || subsessions[0];

            const symbolInfo = {
                name: cleanSymbol,
                description: instrument?.description || cleanSymbol,
                type: instrument?.type || assetType,
                exchange: instrument?.exchange || (assetType === 'FOREX' ? 'FX' : 'US'),
                listed_exchange: instrument?.exchange || (assetType === 'FOREX' ? 'FX' : 'US'),
                format: "price",
                pricescale: assetType === "FOREX" ? 100000 : 100, // 外汇保留更多位小数
                minmov: 1,
                has_intraday: true,
                has_weekly_and_monthly: true,
                supported_resolutions: [...SUPPORTED_RESOLUTIONS],
                data_status: "streaming",
                currency_code: "USD",
                timezone: "America/New_York",
                subsessions: subsessions,
                session: foundSub.session,
                subsession_id: foundSub.id,
            };

            // TradingView expects symbol resolution on a later task.
            setTimeout(() => {
                if (this.destroyed || epoch !== this.streamEpoch) return;
                onResolve(symbolInfo);
                this.onSessionResolved({ symbol: cleanSymbol, session: foundSub.id, nightAvailable: subsessions.some(item => item.id === 'night'), fullDayAvailable: subsessions.some(item => item.id === EQUITY_24H_SESSION), requestedSession: requestedSessionId });
            }, 0);

        } catch (err) {
            if (!this.destroyed && epoch === this.streamEpoch && typeof onError === 'function') {
                onError(String(err?.message || err));
            }
        }
    }

    async getBars(symbolInfo, resolution, periodParams, onResult, onError) {
        const { name, subsession_id } = symbolInfo
        const session = subsession_id || 'regular';
        const { from, to } = periodParams
        const epoch = this.streamEpoch;
        try {
            if (!SUPPORTED_RESOLUTIONS.includes(resolution)) {
                onResult([], { noData: true });
                return;
            }
            if (this.provider === 'futu') {
                if (this.identifyAssetType(name) !== 'EQUITIES') throw new Error('富途看盘仅支持美股和 ETF');
                const overlay = await this.ensureFutuOverlay();
                if (!overlay.providerEnabled) throw new Error('富途行情服务未启用');
                if ((session === 'night' || session === EQUITY_24H_SESSION) && !overlay.enabled) throw new Error('夜盘行情未启用');
                if (session !== 'regular' && !NIGHT_RESOLUTIONS.has(resolution)) throw new Error('扩展时段仅支持分钟 K 线');
                const futuEpoch = this.futuEpoch;
                const bars = await getFutuBars(path => this.futuRequest(path), name, resolution, periodParams, session,
                    () => this.assertHistoryCurrent(epoch, futuEpoch), overlay.enabled);
                this.seedLatest(name, session, resolution, bars);
                onResult(bars, { noData: bars.length === 0 });
                return;
            }
            if (session === 'night' || session === EQUITY_24H_SESSION) {
                const overlay = await this.ensureFutuOverlay();
                const equity = this.identifyAssetType(name) === 'EQUITIES';
                if (!overlay.enabled || !equity || !NIGHT_RESOLUTIONS.has(resolution)) {
                    onResult([], { noData: true });
                    return;
                }
                const bars = session === EQUITY_24H_SESSION
                    ? await this.get24HourBars(name, resolution, periodParams, epoch, this.futuEpoch)
                    : await this.getFutuNightBars(name, resolution, from, to, epoch, this.futuEpoch);
                this.seedLatest(name, session, resolution, bars);
                onResult(bars, { noData: bars.length === 0 });
                return;
            }
            const bars = await this.getSchwabBars(name, resolution, periodParams, epoch, session !== 'regular');
            this.seedLatest(name, session, resolution, bars);
            onResult(bars, { noData: bars.length === 0 });
        } catch (err) {
            if (!this.destroyed && typeof onError === "function") onError(String(err?.message || err));
        }
    }

    seedLatest(name, session, resolution, bars) {
        if (!bars.length) return;
        const key = seriesKey(name, session);
        const symbolCache = this.latestBars.get(key) ?? this.latestBars.set(key, new Map()).get(key);
        const latest = bars.at(-1), cached = symbolCache.get(resolution);
        if (!cached || latest.time > cached.time) symbolCache.set(resolution, { ...latest });
    }

    assertHistoryCurrent(epoch, futuEpoch) {
        if (this.destroyed || epoch !== this.streamEpoch || futuEpoch !== this.futuEpoch) {
            throw new Error('行情连接已变更，请重新读取历史数据');
        }
    }

    async getFutuNightBars(name, resolution, from, to, epoch, futuEpoch, requireCompleteHistory = false) {
        return getFutuNightBars(path => this.futuRequest(path), name, resolution, from, to,
            () => this.assertHistoryCurrent(epoch, futuEpoch), { requireCompleteHistory });
    }

    async get24HourBars(name, resolution, periodParams, epoch, futuEpoch) {
        return get24HourBars(path => this.request(path), path => this.futuRequest(path), name, resolution, periodParams,
            () => this.assertHistoryCurrent(epoch, futuEpoch));
    }

    async getSchwabBars(name, resolution, periodParams, epoch, extended) {
        return getSchwabBars(path => this.request(path), name, resolution, periodParams, extended, () => {
            if (this.destroyed || epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取历史数据');
        });
    }

    subscribeBars(symbolInfo, resolution, onTick, listenerGuid, onReset) {
        if (!SUPPORTED_RESOLUTIONS.includes(resolution)) return;
        const subsessionId = symbolInfo.subsession_id || 'regular';
        this.barListeners.set(listenerGuid, { symbol: symbolInfo.name, resolution, onTick, onReset, subsessionId });
        if (this.provider === 'schwab' && subsessionId !== 'night') this.queueSubscriptions();
        if (this.provider === 'futu' || subsessionId === 'night' || subsessionId === EQUITY_24H_SESSION) this.queueFutuSubscriptions();
    }
    unsubscribeBars(listenerGuid) {
        const listener = this.barListeners.get(listenerGuid);
        this.barListeners.delete(listenerGuid);
        if (listener && (this.provider === 'futu' || listener.subsessionId === 'night' || listener.subsessionId === EQUITY_24H_SESSION)) {
            this.setFutuSubscriptionDesired(listener.symbol, listener.resolution, this.hasFutuListener(listener.symbol, listener.resolution));
        }
    }
    unsubscribeQuotes(listenerGuid) {
        const listener = this.quotesListeners.get(listenerGuid);
        this.quotesListeners.delete(listenerGuid);
        for (const symbol of listener?.symbols ?? []) {
            if (!this.hasFutuListener(symbol, '') && this.futuOvernight.delete(symbol)) this.publishQuote(symbol);
        }
        this.queueFutuSubscriptions();
        if (!this.quotesListeners.size) { clearInterval(this.quoteSessionTimer); this.quoteSessionTimer = undefined; }
    }
    quoteSymbol(symbol) {
        return this.provider === 'futu' ? String(symbol).trim().split(':').at(-1).toUpperCase() : symbol;
    }
    subscribeQuotes(symbols, fastSymbols, callback, listenerGuid) {
        const names = new Map([...symbols, ...fastSymbols].map(symbol => [this.quoteSymbol(symbol), symbol]));
        this.quotesListeners.set(listenerGuid, {
            symbols: new Set(names.keys()), callback: values => callback(values.map(quote => ({ ...quote, n: names.get(quote.n) ?? quote.n }))),
        });
        this.queueSubscriptions();
        this.queueFutuSubscriptions();
        // Keep the market phase current even when no trades arrive at a boundary.
        this.quoteSessionTimer ??= setInterval(() => {
            const symbols = new Set([...this.quotesListeners.values()].flatMap(listener => [...listener.symbols]));
            for (const symbol of symbols) this.publishQuote(symbol);
            this.queueFutuSubscriptions();
        }, 30_000);
    }
    // Widgets subscribe one after another while the terminal starts (watchlist,
    // chart, details, order panel). A short batch window sends their symbols in
    // one ADD per service instead of one command per widget.
    queueSubscriptions(delay = SUBSCRIBE_BATCH_MS) {
        if (this.provider !== 'schwab' || !this.streamReady || this.subscribeTimer || this.destroyed) return;
        this.subscribeTimer = setTimeout(async () => {
            this.subscribeTimer = undefined;
            if (!this.streamReady || this.destroyed) return;
            const epoch = this.streamEpoch;
            const symbols = new Set([...this.barListeners.values()].filter(listener => listener.subsessionId !== 'night').map(listener => listener.symbol));
            for (const listener of this.quotesListeners.values()) for (const symbol of listener.symbols) symbols.add(symbol);
            const fields = {
                LEVELONE_EQUITIES: '0,1,2,3,4,7,8,10,11,12,15,17,25,29,31,33,35,36,43',
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
    isStaleFutuGeneration(rawGeneration) {
        if (rawGeneration === undefined || rawGeneration === null || rawGeneration === '') return false;
        const generation = String(rawGeneration);
        return this.futuRetiredGenerations.has(generation) ||
            (this.futuBackendGeneration !== undefined && olderFutuGeneration(generation, this.futuBackendGeneration));
    }

    observeFutuGeneration(rawGeneration, rawOwner = this.futuOwner) {
        const hasGeneration = rawGeneration !== undefined && rawGeneration !== null && rawGeneration !== '';
        const hasOwner = rawOwner !== undefined && rawOwner !== null && rawOwner !== '';
        if (!hasGeneration && !hasOwner) return false;
        const generation = hasGeneration ? String(rawGeneration) : this.futuBackendGeneration;
        const owner = hasOwner ? String(rawOwner) : this.futuOwner;
        if (hasGeneration && this.isStaleFutuGeneration(generation)) return false;
        const generationChanged = hasGeneration && this.futuBackendGeneration !== undefined && this.futuBackendGeneration !== generation;
        const ownerChanged = hasOwner && this.futuOwner !== undefined && this.futuOwner !== owner;
        if (generationChanged) {
            this.futuRetiredGenerations.add(this.futuBackendGeneration);
            if (this.futuRetiredGenerations.size > 32) this.futuRetiredGenerations.delete(this.futuRetiredGenerations.values().next().value);
        }
        if (hasGeneration) this.futuBackendGeneration = generation;
        if (hasOwner) this.futuOwner = owner;
        if (!generationChanged && !ownerChanged) return false;
        this.resetFutuLeaseState();
        return true;
    }

    resetFutuLeaseState() {
        for (const timer of this.futuRetryTimers.values()) clearTimeout(timer);
        this.futuRetryTimers.clear();
        this.futuOperations.clear();
        this.futuSUB.clear();
        this.futuDesired = this.desiredFutuSubscriptions();
        const symbols = [...this.futuOvernight.keys()];
        this.futuOvernight.clear();
        symbols.forEach(symbol => this.publishQuote(symbol));
    }

    hasFutuSession() {
        return this.futuConnected && Boolean(this.futuBackendGeneration) && Boolean(this.futuOwner);
    }

    isCurrentFutuIdentity(requestGeneration, requestOwner) {
        return this.hasFutuSession() &&
            requestGeneration === this.futuBackendGeneration &&
            requestOwner === this.futuOwner;
    }

    acceptFutuCommandResponse(payload, requestGeneration, requestOwner) {
        if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return false;
        if (payload?.applied === false) return false;
        if (payload?.generation !== undefined && payload?.generation !== null && payload?.generation !== '' &&
            String(payload.generation) !== requestGeneration) {
            return false;
        }
        if (payload?.owner !== undefined && payload?.owner !== null && payload?.owner !== '' &&
            String(payload.owner) !== requestOwner) {
            return false;
        }
        return true;
    }

    invalidateFutuSession(rawGeneration) {
        if (rawGeneration !== undefined && rawGeneration !== null && rawGeneration !== '') {
            const generation = String(rawGeneration);
            if (!this.isStaleFutuGeneration(generation) &&
                this.futuBackendGeneration !== undefined &&
                this.futuBackendGeneration !== generation) {
                this.futuRetiredGenerations.add(this.futuBackendGeneration);
                if (this.futuRetiredGenerations.size > 32) {
                    this.futuRetiredGenerations.delete(this.futuRetiredGenerations.values().next().value);
                }
                this.futuBackendGeneration = generation;
            }
        }
        this.futuConnected = false;
        this.futuOwner = undefined;
        this.resetFutuLeaseState();
    }

    wantsFutuQuote(symbol) {
        return this.identifyAssetType(symbol) === 'EQUITIES' &&
            (this.provider === 'futu' || this.mergedQuote(symbol).cs === 'night');
    }

    usesFutuBars(listener) {
        return this.provider === 'futu'
            ? SUPPORTED_RESOLUTIONS.includes(listener.resolution) && this.identifyAssetType(listener.symbol) === 'EQUITIES'
            : (listener.subsessionId === 'night' || listener.subsessionId === EQUITY_24H_SESSION) && NIGHT_RESOLUTIONS.has(listener.resolution);
    }

    futuAvailable(overlay) {
        return this.provider === 'futu' ? overlay.providerEnabled === true : overlay.enabled;
    }

    hasFutuListener(symbol, resolution) {
        if (resolution === '') return this.wantsFutuQuote(symbol) &&
            [...this.quotesListeners.values()].some(listener => listener.symbols.has(symbol));
        return [...this.barListeners.values()].some(listener => listener.symbol === symbol && listener.resolution === resolution && this.usesFutuBars(listener));
    }

    setFutuSubscriptionDesired(symbol, resolution, desired) {
        const key = symbol + '\0' + resolution;
        const retry = this.futuRetryTimers.get(key);
        if (retry) {
            clearTimeout(retry);
            this.futuRetryTimers.delete(key);
        }
        if (!this.destroyed && desired && this.hasFutuListener(symbol, resolution)) this.futuDesired.add(key);
        else this.futuDesired.delete(key);
        this.queueFutuReconcile(symbol, resolution);
    }

    queueFutuRetry(symbol, resolution) {
        const key = symbol + '\0' + resolution;
        if (this.futuRetryTimers.has(key)) return;
        const timer = setTimeout(() => {
            this.futuRetryTimers.delete(key);
            this.queueFutuReconcile(symbol, resolution);
        }, 2000);
        this.futuRetryTimers.set(key, timer);
    }

    queueFutuReconcile(symbol, resolution) {
        const key = symbol + '\0' + resolution;
        if (this.futuOperations.has(key)) return;
        const subscribed = this.futuSUB.get(key) === 'subscribed';
        if (this.futuDesired.has(key) && !subscribed && !this.hasFutuSession()) return;
        if (!this.futuDesired.has(key) && subscribed && !this.hasFutuSession()) {
            this.futuSUB.delete(key);
            return;
        }
        const operation = this.reconcileFutuSubscription(symbol, resolution);
        this.futuOperations.set(key, operation);
        void operation.finally(() => {
            if (this.futuOperations.get(key) !== operation) return;
            this.futuOperations.delete(key);
            const desired = !this.destroyed && this.futuDesired.has(key) && this.hasFutuListener(symbol, resolution);
            const isSubscribed = this.futuSUB.get(key) === 'subscribed';
            if (desired !== isSubscribed && !this.futuRetryTimers.has(key)) this.queueFutuReconcile(symbol, resolution);
        }).catch(() => {});
    }

    async reconcileFutuSubscription(symbol, resolution) {
        const key = symbol + '\0' + resolution;
        while (true) {
            const desired = !this.destroyed && this.futuDesired.has(key) && this.hasFutuListener(symbol, resolution);
            const subscribed = this.futuSUB.get(key) === 'subscribed';
            if (desired === subscribed) {
                if (!desired) this.futuSUB.delete(key);
                return;
            }
            const requestGeneration = this.futuBackendGeneration;
            const requestOwner = this.futuOwner;
            if (desired) {
                if (!this.hasFutuSession()) return;
                let overlay;
                try { overlay = await this.ensureFutuOverlay(); } catch {
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    this.futuSUB.delete(key);
                    this.queueFutuRetry(symbol, resolution);
                    return;
                }
                if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                if (!this.futuAvailable(overlay)) {
                    this.futuDesired.delete(key);
                    this.futuSUB.delete(key);
                    return;
                }
                if (!this.hasFutuSession()) return;
                this.futuSUB.set(key, 'pending');
                try {
                    const response = await this.futuRequest('/quote/ws/command', {
                        method: 'POST',
                        body: JSON.stringify({
                            command: 'ADD',
                            symbol,
                            resolution,
                            generation: requestGeneration,
                            owner: requestOwner,
                        }),
                    });
                    if (!response.ok) throw new Error('富途订阅失败');
                    const payload = await response.clone().json().catch(() => null);
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    if (!this.acceptFutuCommandResponse(payload, requestGeneration, requestOwner)) {
                        this.invalidateFutuSession(payload?.generation);
                        return;
                    }
                    // Re-read desired state after the awaited ADD. If the last
                    // listener left while it was pending, the loop sends UNSUB.
                    this.futuSUB.set(key, 'subscribed');
                } catch {
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    this.futuSUB.delete(key);
                    if (!this.destroyed && this.futuDesired.has(key) && this.hasFutuListener(symbol, resolution)) this.queueFutuRetry(symbol, resolution);
                    return;
                }
            } else {
                if (!this.hasFutuSession()) {
                    this.futuSUB.delete(key);
                    return;
                }
                let overlay;
                try { overlay = await this.ensureFutuOverlay(); } catch {
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    this.queueFutuRetry(symbol, resolution);
                    return;
                }
                if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                // Disabling overnight coverage closes the gateway and drops its
                // leases; no HTTP UNSUB is possible or needed after that.
                if (!this.futuAvailable(overlay)) {
                    this.futuSUB.delete(key);
                    return;
                }
                this.futuSUB.set(key, 'releasing');
                try {
                    const response = await this.futuRequest('/quote/ws/command', {
                        method: 'POST',
                        body: JSON.stringify({
                            command: 'UNSUB',
                            symbol,
                            resolution,
                            generation: requestGeneration,
                            owner: requestOwner,
                        }),
                    });
                    if (!response.ok && response.status !== 409) throw new Error('富途取消订阅失败');
                    const payload = response.ok ? await response.clone().json().catch(() => null) : null;
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    if (response.ok && !this.acceptFutuCommandResponse(payload, requestGeneration, requestOwner)) {
                        this.invalidateFutuSession(payload?.generation);
                        return;
                    }
                    this.futuSUB.delete(key);
                } catch {
                    if (!this.isCurrentFutuIdentity(requestGeneration, requestOwner)) return;
                    this.futuSUB.set(key, 'subscribed');
                    this.queueFutuRetry(symbol, resolution);
                    return;
                }
            }
        }
    }

    queueFutuSubscriptions(delay = 0) {
        if (this.destroyed || this.futuTimer) return;
        this.futuTimer = setTimeout(async () => {
            this.futuTimer = undefined;
            if (this.destroyed) return;
            const overlay = await this.ensureFutuOverlay();
            if (this.destroyed) return;
            if (!this.futuAvailable(overlay)) {
                this.futuSUB.clear();
                this.futuDesired.clear();
                return;
            }
            const desired = this.desiredFutuSubscriptions();
            if (desired.size) this.futuStream?.start?.();
            else if (this.provider !== 'futu') this.futuStream?.stop?.();
            const known = new Set([...this.futuDesired, ...this.futuSUB.keys(), ...desired]);
            for (const key of known) {
                const [symbol, resolution] = key.split('\0');
                this.setFutuSubscriptionDesired(symbol, resolution, desired.has(key));
            }
        }, delay);
    }
    desiredFutuSubscriptions() {
        const desired = new Set([...this.barListeners.values()]
            .filter(listener => this.usesFutuBars(listener))
            .map(listener => listener.symbol + '\0' + listener.resolution));
        for (const listener of this.quotesListeners.values()) for (const symbol of listener.symbols) {
            if (this.wantsFutuQuote(symbol)) desired.add(symbol + '\0');
        }
        return desired;
    }
    destroy() {
        if (this.destroyed) return;
        this.destroyed = true;
        this.streamEpoch++;
        this.futuEpoch++;
        this.controller.abort();
        clearTimeout(this.subscribeTimer);
        clearTimeout(this.futuTimer);
        clearInterval(this.quoteSessionTimer);
        this.events.removeEventListener('LEVELONE_ANY', this.onLevelOne);
        this.events.removeEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        this.events.removeEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
        this.events.removeEventListener('FUTU_KL', this.onFutuKL);
        this.events.removeEventListener('FUTU_QUOTE', this.onFutuQuote);
        this.events.removeEventListener('FUTU_STREAM_READY', this.onFutuReady);
        this.events.removeEventListener('FUTU_STREAM_CLOSED', this.onFutuClosed);
        for (const timer of this.futuRetryTimers.values()) clearTimeout(timer);
        this.futuRetryTimers.clear();
        const futuSubscriptions = new Set([
            ...this.futuDesired,
            ...this.futuSUB.keys(),
            ...this.futuOperations.keys(),
            ...[...this.barListeners.values()]
                .filter(listener => this.usesFutuBars(listener))
                .map(listener => listener.symbol + '\0' + listener.resolution),
        ]);
        for (const key of futuSubscriptions) {
            const [symbol, resolution] = key.split('\0');
            this.futuDesired.delete(key);
            this.queueFutuReconcile(symbol, resolution);
        }
        this.barListeners.clear();
        this.quotesListeners.clear();
        this.futuStream?.stop?.();
        this.quoteRequests.clear();
        for (const cache of [this.quote_cache, this.schwabQuotes, this.futuQuotes, this.futuQuoteTimes, this.futuOvernight,
            this.snapshot_cache, this.snapshotTimes, this.latestBars, this.cumulativeVolumes, this.cumulativeVolumeDates, this.SUB]) cache.clear();
    }

    mergedQuote(symbol) {
        if (this.provider === 'futu') return this.futuQuotes.get(symbol) || {};
        return mergeQuoteValues(this.schwabQuotes.get(symbol), this.futuOvernight.get(symbol), {
            equity: this.identifyAssetType(symbol) === 'EQUITIES', overnightEnabled: this.overlayCache.enabled, now: this.now(),
        });
    }

    publishQuote(symbol) {
        if (this.destroyed) return;
        const quote = this.mergedQuote(symbol);
        if (this.provider === 'schwab' && quote.cs && quote.cs !== 'night') this.futuOvernight.delete(symbol);
        this.quote_cache.set(symbol, quote);
        for (const { symbols, callback } of this.quotesListeners.values()) {
            if (symbols.has(symbol)) callback?.([quoteData(symbol, quote)]);
        }
    }

    async loadFutuQuotes(symbols) {
        const epoch = this.streamEpoch, futuEpoch = this.futuEpoch;
        const overlay = await this.ensureFutuOverlay();
        this.assertHistoryCurrent(epoch, futuEpoch);
        if (!overlay.providerEnabled) throw new Error('富途行情服务未启用');
        const missing = [...new Set(symbols)].filter(symbol => this.identifyAssetType(symbol) === 'EQUITIES' &&
            !Number.isFinite(this.futuQuotes.get(symbol)?.lp) && !this.quoteRequests.has(symbol));
        for (let offset = 0; offset < missing.length; offset += 50) {
            const batch = missing.slice(offset, offset + 50);
            const pending = (async () => {
                try {
                    const response = await this.futuRequest('/quotes?' + new URLSearchParams({ symbols: batch.join(',') }));
                    if (!response.ok) throw await futuResponseError(response, '读取富途报价失败');
                    const data = await response.json();
                    this.assertHistoryCurrent(epoch, futuEpoch);
                    if (!Array.isArray(data.items)) throw new Error('富途报价格式无效');
                    for (const item of data.items) {
                        if (!batch.includes(item.symbol) || this.futuQuotes.has(item.symbol)) continue;
                        const quote = futuQuoteValues(item, overlay.enabled);
                        if (!Number.isFinite(quote.lp)) continue;
                        this.futuQuotes.set(item.symbol, quote);
                        if (Number.isFinite(Number(item.time))) this.futuQuoteTimes.set(item.symbol, Number(item.time));
                        this.publishQuote(item.symbol);
                    }
                } finally {
                    for (const symbol of batch) if (this.quoteRequests.get(symbol) === pending) this.quoteRequests.delete(symbol);
                }
            })();
            for (const symbol of batch) this.quoteRequests.set(symbol, pending);
        }
        await Promise.all(symbols.map(symbol => this.quoteRequests.get(symbol)));
        this.assertHistoryCurrent(epoch, futuEpoch);
    }

    async loadQuotes(symbols) {
        if (this.provider === 'futu') return this.loadFutuQuotes(symbols);
        const missing = [...new Set(symbols)].filter(symbol => !Number.isFinite(this.schwabQuotes.get(symbol)?.lp) && !this.quoteRequests.has(symbol));
        const epoch = this.streamEpoch;
        for (let offset = 0; offset < missing.length; offset += 50) {
            const batch = missing.slice(offset, offset + 50);
            const pending = (async () => {
                try {
                    const query = new URLSearchParams({ symbols: batch.join(','), fields: 'quote,regular,reference' });
                    const response = await Promise.resolve().then(() => this.request('/marketdata/v1/quotes?' + query));
                    if (!response.ok) throw new Error('读取报价失败');
                    const data = await response.json();
                    if (this.destroyed || epoch !== this.streamEpoch) return;
                    for (const symbol of batch) {
                        if (!data[symbol]) continue;
                        // An initial REST response may fill missing fields, but
                        // must never overwrite a newer stream update.
                        const initial = schwabRestQuoteValues(data[symbol]), current = this.schwabQuotes.get(symbol);
                        if (current?.lp !== undefined && current.lp !== initial.lp) { delete initial.ch; delete initial.chp; }
                        this.schwabQuotes.set(symbol, { ...initial, ...current });
                        this.publishQuote(symbol);
                    }
                } catch { /* Other available provider values can still be returned. */ }
                finally {
                    for (const symbol of batch) if (this.quoteRequests.get(symbol) === pending) this.quoteRequests.delete(symbol);
                }
            })();
            for (const symbol of batch) this.quoteRequests.set(symbol, pending);
        }
        await Promise.all(symbols.map(symbol => this.quoteRequests.get(symbol)));
    }

    async getQuotes(symbols, onDataCallback, onErrorCallback) {
        const epoch = this.streamEpoch;
        try {
            await this.loadQuotes(symbols.map(symbol => this.quoteSymbol(symbol)));
            if (this.destroyed) return;
            if (epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取报价');
            setTimeout(() => {
                if (!this.destroyed && epoch === this.streamEpoch) onDataCallback(symbols.map(symbol => quoteData(symbol, this.mergedQuote(this.quoteSymbol(symbol)))));
            }, 0);
        } catch (error) {
            if (!this.destroyed) onErrorCallback(error);
        }
    }
}
