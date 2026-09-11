import { schwabFetch } from './schwab.js';

export default class Datafeed {
    constructor({ request = schwabFetch, events = window, stream = window.ws, resetCharts = () => {} } = {}) {
        this.request = request;
        this.events = events;
        this.streamReady = stream?.ready === true;
        this.streamEpoch = 0;
        this.SUB = new Map();
        this.resetCharts = resetCharts;


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

            // 遍历 content 数组逐个处理标的数据
            for (const item of raw.content) {
                const symbolKey = item.key;
                if (!symbolKey) continue;
                if (timestamp < (this.snapshotTimes.get(symbolKey) ?? 0)) continue;
                this.snapshotTimes.set(symbolKey, timestamp);

                // 1. 获取现有快照
                const existing = this.snapshot_cache.get(symbolKey) || {};

                // 2. 清洗无效值 (不覆盖旧的有效数据)
                const cleanedItem = {};
                for (const [k, v] of Object.entries(item)) {
                    if (v !== null && v !== undefined) {
                        cleanedItem[k] = v;
                    }
                }

                // 3. 拼装最新快照并缓存
                const snapshot = {
                    ...existing,
                    ...cleanedItem,
                };
                this.snapshot_cache.set(symbolKey, snapshot);

                // 4. 根据品种类型映射字段
                let quote = null; // 提前声明，防止下面作用域拿不到

                switch (raw.service) {
                    case 'LEVELONE_EQUITIES': {
                        // 注意这里的花括号 {}，它能隔离块级作用域，防止和别的 case 冲突
                        const LAST_PRICE = Number(snapshot['3'] || snapshot['33']);
                        const PREV_CLOSE = Number(snapshot['12'] || 0);
                        const VOLUME = Number(snapshot['8'] || 0);

                        quote = {
                            lp: Number(snapshot['29'] || LAST_PRICE),
                            ch: Number(snapshot['31'] || undefined),
                            chp: Number(snapshot['43'] || undefined),
                            ask: Number(snapshot['2'] || undefined),
                            bid: Number(snapshot['1'] || undefined),
                            open_price: Number(snapshot['17'] || undefined),
                            high_price: Number(snapshot['10'] || undefined),
                            low_price: Number(snapshot['11'] || undefined),
                            prev_close_price: PREV_CLOSE,
                            volume: VOLUME,
                            rtc: LAST_PRICE,
                            rch: PREV_CLOSE ? (LAST_PRICE - PREV_CLOSE) : undefined,
                            rchp: PREV_CLOSE ? ((LAST_PRICE - PREV_CLOSE) / PREV_CLOSE * 100) : undefined,
                            rtc_time: Number(timestamp) / 1000,
                            exchange: snapshot['25'],
                            description: snapshot['15'],
                        };
                        break;
                    }
                    case 'LEVELONE_OPTIONS': {
                        const LAST_PRICE = Number(snapshot['4'] || snapshot['37']);
                        const PREV_CLOSE = Number(snapshot['7'] || 0);
                        const VOLUME = Number(snapshot['8'] || 0);

                        quote = {
                            lp: LAST_PRICE,
                            ch: Number(snapshot['45'] || undefined),
                            chp: Number(snapshot['46'] || undefined),
                            ask: Number(snapshot['2'] || undefined),
                            bid: Number(snapshot['3'] || undefined),
                            open_price: Number(snapshot['15'] || undefined),
                            high_price: Number(snapshot['5'] || undefined),
                            low_price: Number(snapshot['6'] || undefined),
                            prev_close_price: PREV_CLOSE,
                            volume: VOLUME,
                        };
                        break;
                    }
                    case 'LEVELONE_FUTURES': {
                        const LAST_PRICE = Number(snapshot['3'] || snapshot['24']);
                        const PREV_CLOSE = Number(snapshot['14'] || 0);
                        const VOLUME = Number(snapshot['8'] || 0);

                        quote = {
                            lp: LAST_PRICE,
                            ch: Number(snapshot['19'] || undefined),
                            chp: Number(snapshot['20'] || undefined),
                            ask: Number(snapshot['2'] || undefined),
                            bid: Number(snapshot['1'] || undefined),
                            open_price: Number(snapshot['18'] || undefined),
                            high_price: Number(snapshot['12'] || undefined),
                            low_price: Number(snapshot['13'] || undefined),
                            prev_close_price: PREV_CLOSE,
                            volume: VOLUME,
                            rtc: LAST_PRICE,
                            rtc_time: Number(snapshot['10']) / 1000,
                        };
                        break;
                    }
                    case 'LEVELONE_FUTURES_OPTIONS': {
                        const LAST_PRICE = Number(snapshot['3'] || snapshot['19']);
                        const PREV_CLOSE = Number(snapshot['14'] || 0);
                        const VOLUME = Number(snapshot['8'] || 0);

                        quote = {
                            lp: LAST_PRICE,
                            ask: Number(snapshot['2'] || undefined),
                            bid: Number(snapshot['1'] || undefined),
                            open_price: Number(snapshot['17'] || undefined),
                            high_price: Number(snapshot['12'] || undefined),
                            low_price: Number(snapshot['13'] || undefined),
                            prev_close_price: PREV_CLOSE,
                            volume: VOLUME,
                            ch: PREV_CLOSE ? (LAST_PRICE - PREV_CLOSE) : undefined,
                            chp: PREV_CLOSE ? ((LAST_PRICE - PREV_CLOSE) / PREV_CLOSE * 100) : undefined,
                            rtc: LAST_PRICE,
                            rtc_time: Number(snapshot['10']) / 1000,
                        };
                        break;
                    }
                    case 'LEVELONE_FOREX': {
                        const LAST_PRICE = Number(snapshot['3'] || snapshot['29']);
                        const PREV_CLOSE = Number(snapshot['12'] || snapshot['3']);
                        const VOLUME = Number(snapshot['6'] || 0);

                        quote = {
                            lp: LAST_PRICE,
                            ask: Number(snapshot['2'] || undefined),
                            bid: Number(snapshot['1'] || undefined),
                            open_price: Number(snapshot['15'] || undefined),
                            high_price: Number(snapshot['10'] || undefined),
                            low_price: Number(snapshot['11'] || undefined),
                            prev_close_price: PREV_CLOSE,
                            volume: VOLUME,
                            ch: PREV_CLOSE ? (LAST_PRICE - PREV_CLOSE) : undefined,
                            chp: PREV_CLOSE ? ((LAST_PRICE - PREV_CLOSE) / PREV_CLOSE * 100) : undefined,
                            rtc: LAST_PRICE,
                            rtc_time: Number(snapshot['10']) / 1000,
                        };
                        break;
                    }
                    default:
                        break;
                }

                // 5. 如果映射成功，推入 quote 缓存 (并可在此处分发给图表)
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

                        const symbolCache = this.latestBars.get(symbolKey);
                        if (!symbolCache) continue;
                        const VOLUME = quote.volume;
                        const LAST_PRICE = quote.rtc ?? quote.lp;
                        if (!Number.isFinite(LAST_PRICE) || LAST_PRICE <= 0 || !Number.isFinite(VOLUME)) continue;
                        // 1. 全局成交量增量计算（极速，仅算1次）
                        const prevVolume = this.cumulativeVolumes.get(symbolKey) ?? VOLUME;
                        const volumeDelta = Math.max(0, VOLUME - prevVolume);
                        this.cumulativeVolumes.set(symbolKey, VOLUME);

                        // A shared series is updated once, even when several charts subscribe.
                        const updatedBars = new Map();
                        for (const { symbol, resolution, onTick } of this.barListeners.values()) {
                            if (symbol !== symbolKey) continue;
                            if (updatedBars.has(resolution)) {
                                onTick?.({ ...updatedBars.get(resolution) });
                                continue;
                            }
                            const lastBar = symbolCache.get(resolution);
                            if (!lastBar) continue;

                            // 3. 构建/更新 K 线
                            const bucketTime = getBucketTime(resolution, timestamp);
                            if (!Number.isFinite(bucketTime) || bucketTime < lastBar.time) continue;
                            const isNewBucket = bucketTime > lastBar.time;

                            const newBar = isNewBucket ? {
                                time: bucketTime,
                                open: LAST_PRICE,
                                high: LAST_PRICE,
                                low: LAST_PRICE,
                                close: LAST_PRICE,
                                volume: volumeDelta
                            } : {
                                ...lastBar,
                                high: Math.max(lastBar.high, LAST_PRICE),
                                low: Math.min(lastBar.low, LAST_PRICE),
                                close: LAST_PRICE,
                                volume: lastBar.volume + volumeDelta
                            };

                            // 4. 缓存并推送
                            symbolCache.set(resolution, newBar);
                            updatedBars.set(resolution, newBar);
                            onTick?.({ ...newBar });
                        }
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
        events.addEventListener('LEVELONE_ANY', this.onLevelOne);
        events.addEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        events.addEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
    }

    onReady(callback) {
        setTimeout(() => {
            callback({
                supported_resolutions: ['1', '5', '10', '15', '30', '1D', '1W', '1M'],
            });
        }, 1000);
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

    /**
   * 辅助函数 1：根据符号特征识别资产类型
   */
    identifyAssetType(symbolName) {
        if (!symbolName) return "EQUITIES";

        // 强制转为字符串，并清理首尾的普通空格
        const cleanSymbol = String(symbolName).trim();

        if (cleanSymbol.startsWith("./")) {
            return "FUTURES_OPTIONS";    // 期货期权
        } else if (cleanSymbol.startsWith("/")) {
            return "FUTURES";            // 期货
        } else if (cleanSymbol.includes("/")) {
            return "FOREX";              // 外汇
        }
        // 🌟 按照你的思路：只要中间还有空格，直接判定为期权！
        else if (cleanSymbol.includes(" ")) {
            return "OPTIONS";            // 期权: GOOGL 260812P00345000
        }
        else {
            return "EQUITIES";           // 正股: AAPL, GOOGL
        }
    }

    /**
     * 辅助函数 2：为不同资产类型分配专属的交易时段 (Subsessions)
     */
    getAssetSubsessions(assetType) {
        switch (assetType) {
            case "EQUITIES":
            case "OPTIONS":
                // 股票与股票期权：标准美股时间 (美东时间)
                return [
                    { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1600" },
                    { "description": "Extended Trading Hours", "id": "extended", "session": "0400-2000" },
                    { "description": "Premarket", "id": "premarket", "session": "0400-0930" },
                    { "description": "Postmarket", "id": "postmarket", "session": "1600-2000" },
                    { "description": "Night", "id": "night", "session": "2000-0400" }, // 如果有夜盘券商
                    { "description": "24h", "id": "24h", "session": "2000-2000" }
                ];

            case "FUTURES":
            case "FUTURES_OPTIONS":
                // 期货与期货期权：通常收盘较晚，或有几乎全天交易的时段 (以16:15收盘为例)
                return [
                    { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1615" },
                    { "description": "Extended Trading Hours", "id": "extended", "session": "1800-1700" },
                    { "description": "Premarket", "id": "premarket", "session": "1800-0930" },
                    { "description": "Postmarket", "id": "postmarket", "session": "1615-1700" },
                ];

            case "FOREX":
                // 外汇：通常是 24/5 全天候交易
                return [
                    { "description": "Regular Trading Hours", "id": "regular", "session": "0000-0000" },
                    { "description": "24h", "id": "24h", "session": "0000-0000" }
                ];

            default:
                // 兜底时间
                return [
                    { "description": "Regular Trading Hours", "id": "regular", "session": "0930-1600" }
                ];
        }
    }

    /**
     * 主函数：解析 Symbol 并返回给 TradingView 图表
     */
    async resolveSymbol(symbolName, onResolve, onError, extension) {
        try {
            const cleanSymbol = symbolName.trim();

            // 1. 调用提取出的函数获取类型
            const assetType = this.identifyAssetType(cleanSymbol);

            // 2. 调用提取出的函数获取该类型的专属时段列表
            const subsessions = this.getAssetSubsessions(assetType);

            // 3. 匹配用户请求的时段 (默认 regular)
            const requestedSessionId = (extension && extension.session) ? extension.session : "regular";
            const foundSub = subsessions.find(item => item.id === requestedSessionId) || subsessions[0];

            // 4. 构建 TradingView 要求的 symbolInfo
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

            // 使用 setTimeout 模拟微任务，符合 TradingView 的异步解析规范
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
        const { from, to } = periodParams
        const epoch = this.streamEpoch;

        const barsize = (resolution === "1D" || resolution === "1W" || resolution === "1M") ? ("&frequencyType=" + (resolution === "1D" ? "daily" : resolution === "1W" ? "weekly" : "monthly") + "&frequency=1&periodType=year") : ("&frequencyType=minute&frequency=" + resolution);

        const diffTime = to - from;
        const expandedFrom = from - Math.floor(diffTime * 0.5);
        const timeParams = "&startDate=" + (expandedFrom * 1000) + "&endDate=" + (periodParams.to * 1000);
        const url = "/marketdata/v1/pricehistory?symbol=" + encodeURIComponent(name) + (subsession_id && subsession_id !== "regular" ? "&needExtendedHoursData=true" : "") + barsize + timeParams;
        try {
            const response = await this.request(url);
            if (!response.ok) {
                throw new Error("获取历史数据失败" + name);
            }
            const data = await response.json();
            if (this.destroyed || epoch !== this.streamEpoch) throw new Error('行情连接已变更，请重新读取历史数据');
            if (!Array.isArray(data.candles)) throw new Error('Schwab 历史数据格式无效');
            const unique = new Map();
            for (const item of data.candles) {
                if (item.datetime == null || !Number.isFinite(Number(item.datetime))) throw new Error('Schwab 历史数据包含无效时间');
                const timestamp = Number(item.datetime);
                const time = ['1D', '1W', '1M'].includes(resolution) ? getBucketTime(resolution, timestamp) : timestamp;
                const bar = { time, open: Number(item.open), high: Number(item.high), low: Number(item.low), close: Number(item.close), volume: Number(item.volume) };
                if (!Object.values(bar).every(Number.isFinite)) throw new Error('Schwab 历史数据包含无效价格或时间');
                if (time < to * 1000) unique.set(time, bar);
            }
            const bars = [...unique.values()].sort((a, b) => a.time - b.time);
            if (bars.length) {
                const symbolCache = this.latestBars.get(name) ?? this.latestBars.set(name, new Map()).get(name);
                const latest = bars.at(-1), cached = symbolCache.get(resolution);
                // Reloads after reset need a seed even without firstDataRequest. Older
                // backfill must never roll back a bar already updated by the live stream.
                if (!cached || latest.time > cached.time) symbolCache.set(resolution, { ...latest });
            }
            const isNoData = bars.length === 0;
            onResult(bars, { noData: isNoData });
        } catch (err) {
            if (typeof onError === "function") {
                onError(err);
            }
        }
    }

    subscribeBars(symbolInfo, resolution, onTick, listenerGuid, onReset) {
        this.barListeners.set(listenerGuid, { symbol: symbolInfo.name, resolution, onTick, onReset });
        this.queueSubscriptions();
    }
    unsubscribeBars(listenerGuid) { this.barListeners.delete(listenerGuid); }
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
    destroy() {
        this.destroyed = true;
        clearTimeout(this.subscribeTimer);
        this.events.removeEventListener('LEVELONE_ANY', this.onLevelOne);
        this.events.removeEventListener('SCHWAB_STREAM_READY', this.onStreamReady);
        this.events.removeEventListener('SCHWAB_STREAM_CLOSED', this.onStreamClosed);
        this.barListeners.clear();
        this.quotesListeners.clear();
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


const RESOLUTIONS_MS = {
    '1': 60_000, '2': 120_000, '3': 180_000, '5': 300_000,
    '10': 600_000, '15': 900_000, '30': 1_800_000, '60': 3_600_000,
    '120': 7_200_000, '180': 10_800_000, '240': 14_400_000, '480': 28_800_000,
    '1D': 86_400_000
}

function getBucketTime(resolution, timestamp = Date.now()) {


    // 【月线】特殊处理：月份天数不固定，不能用除法，必须强制归入当月 1 号 00:00 UTC
    if (resolution === '1M') {
        const d = new Date(timestamp);
        return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
    }

    // 【周线】特殊处理：Unix元年(1970-01-01)是周四，直接整除会导致周线从周四开始。这里补偿3天偏移量对齐到周一
    if (resolution === '1W') {
        const WEEK_MS = 604_800_000;
        const OFFSET = 3 * 86_400_000;
        return Math.floor((timestamp + OFFSET) / WEEK_MS) * WEEK_MS - OFFSET;
    }

    // 【日内与日线】常规整除
    const ms = RESOLUTIONS_MS[resolution];
    return Math.floor(timestamp / ms) * ms;
}
