import { EQUITY_24H_SESSION, equityTradingDateET, seriesKey, usesSchwabTicks } from './futu.js';
import { getBucketTime } from './datafeed-bars.js';

// Convert provider fields without changing their fallback or missing-value rules.
export function mapSchwabQuote(service, snapshot, timestamp) {
    let quote = null;
    switch (service) {
        case 'LEVELONE_EQUITIES': {
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
    return quote;
}

const SCHWAB_TRADE_FIELDS = {
    LEVELONE_EQUITIES: { volume: ['8'] },
    LEVELONE_OPTIONS: { volume: ['8'] },
    LEVELONE_FUTURES: { volume: ['8'] },
    LEVELONE_FUTURES_OPTIONS: { volume: ['8'] },
    LEVELONE_FOREX: { volume: ['6'] },
};

function numericField(source, fields) {
    for (const field of fields) {
        if (Object.hasOwn(source, field) && source[field] !== null && source[field] !== '') {
            const value = Number(source[field]);
            if (Number.isFinite(value)) return value;
        }
    }
    return undefined;
}

// Streamer packets are sparse patches. Compare the final price that the chart
// actually plots after merging, so a lower-priority fallback field cannot
// manufacture a trade when the provider's primary field remains unchanged.
function plottedPrice(service, snapshot) {
    const quote = mapSchwabQuote(service, snapshot, 0);
    const price = quote?.rtc ?? quote?.lp;
    return Number.isFinite(price) ? price : undefined;
}

export function isSchwabTradeUpdate(service, incoming, previousSnapshot = {}) {
    const fields = SCHWAB_TRADE_FIELDS[service];
    if (!fields) return false;
    const cleanedPatch = Object.fromEntries(Object.entries(incoming).filter(([, value]) => value !== null && value !== undefined));
    const nextSnapshot = { ...previousSnapshot, ...cleanedPatch };
    const nextPrice = plottedPrice(service, nextSnapshot);
    const previousPrice = plottedPrice(service, previousSnapshot);
    if (nextPrice !== undefined && previousPrice !== undefined && nextPrice !== previousPrice) return true;
    const nextVolume = numericField(incoming, fields.volume);
    const previousVolume = numericField(previousSnapshot, fields.volume);
    return nextVolume !== undefined && previousVolume !== undefined && nextVolume > previousVolume;
}

export function seedSchwabVolumeBaseline(listeners, cumulativeVolumes, cumulativeVolumeDates, symbolKey, volume, timestamp) {
    if (!Number.isFinite(volume)) return;
    for (const { symbol, resolution, subsessionId } of listeners.values()) {
        if (symbol !== symbolKey || !usesSchwabTicks(subsessionId, timestamp)) continue;
        const key = seriesKey(symbolKey, subsessionId);
        const volumeKey = `${key}\0${resolution}`;
        const volumeDate = subsessionId === EQUITY_24H_SESSION ? equityTradingDateET(timestamp) : '';
        const priorDate = cumulativeVolumeDates.get(volumeKey);
        const priorVolume = cumulativeVolumes.get(volumeKey);
        if (!cumulativeVolumes.has(volumeKey) || volume < priorVolume || (subsessionId === EQUITY_24H_SESSION && priorDate !== volumeDate)) {
            cumulativeVolumes.set(volumeKey, volume);
            cumulativeVolumeDates.set(volumeKey, volumeDate);
        }
    }
}

// Each series is updated once before cloned bars are delivered to its listeners.
export function updateSchwabBars(listeners, latestBars, cumulativeVolumes, symbolKey, quote, timestamp, cumulativeVolumeDates = new Map()) {
    const VOLUME = quote.volume;
    const LAST_PRICE = quote.rtc ?? quote.lp;
    if (!Number.isFinite(LAST_PRICE) || LAST_PRICE <= 0 || !Number.isFinite(VOLUME)) return;
    const updatedBars = new Map();
    for (const { symbol, resolution, onTick, subsessionId } of listeners.values()) {
        if (symbol !== symbolKey || !usesSchwabTicks(subsessionId, timestamp)) continue;
        const key = seriesKey(symbolKey, subsessionId);
        const symbolCache = latestBars.get(key);
        if (!symbolCache) continue;
        const seriesUpdated = updatedBars.get(key) ?? new Map();
        if (seriesUpdated.has(resolution)) {
            onTick?.({ ...seriesUpdated.get(resolution) });
            continue;
        }
        const lastBar = symbolCache.get(resolution);
        if (!lastBar) continue;
        const volumeKey = `${key}\0${resolution}`;
        const volumeDate = subsessionId === EQUITY_24H_SESSION ? equityTradingDateET(timestamp) : '';
        const previousDate = cumulativeVolumeDates.get(volumeKey);
        const previousVolume = cumulativeVolumes.get(volumeKey);
        const prevVolume = subsessionId === EQUITY_24H_SESSION && previousDate !== volumeDate
            ? VOLUME
            : (previousVolume ?? VOLUME);
        const volumeDelta = Math.max(0, VOLUME - prevVolume);
        cumulativeVolumes.set(volumeKey, VOLUME);
        cumulativeVolumeDates.set(volumeKey, volumeDate);
        const bucketTime = getBucketTime(resolution, timestamp);
        if (!Number.isFinite(bucketTime) || bucketTime < lastBar.time) continue;
        const isNewBucket = bucketTime > lastBar.time;
        const newBar = isNewBucket ? {
            time: bucketTime, open: LAST_PRICE, high: LAST_PRICE, low: LAST_PRICE, close: LAST_PRICE, volume: volumeDelta
        } : {
            ...lastBar, high: Math.max(lastBar.high, LAST_PRICE), low: Math.min(lastBar.low, LAST_PRICE),
            close: LAST_PRICE, volume: lastBar.volume + volumeDelta
        };
        symbolCache.set(resolution, newBar);
        seriesUpdated.set(resolution, newBar);
        updatedBars.set(key, seriesUpdated);
        onTick?.({ ...newBar });
    }
}
