import { isOvernightET } from './futu.js';

// History normalization and time buckets shared with live bar aggregation.
export async function getFutuNightBars(request, name, resolution, from, to, assertCurrent) {
    const params = new URLSearchParams({ symbol: name, resolution });
    if (from) params.set('from', String(from));
    if (to) params.set('to', String(to));
    const response = await request('/kline?' + params.toString());
    assertCurrent();
    if (!response.ok) throw new Error('获取夜盘数据失败' + name);
    const data = await response.json();
    if (!Array.isArray(data.candles)) throw new Error('夜盘历史数据格式无效');
    return data.candles.map((item) => ({
        time: Number(item.time), open: Number(item.open), high: Number(item.high),
        low: Number(item.low), close: Number(item.close), volume: Number(item.volume),
    })).filter((bar) => Object.values(bar).every(Number.isFinite));
}

export async function getSchwabBars(request, name, resolution, periodParams, extended, assertCurrent) {
    const { from, to } = periodParams;
    const barsize = (resolution === "1D" || resolution === "1W" || resolution === "1M") ? ("&frequencyType=" + (resolution === "1D" ? "daily" : resolution === "1W" ? "weekly" : "monthly") + "&frequency=1&periodType=year") : ("&frequencyType=minute&frequency=" + resolution);
    const diffTime = to - from;
    const expandedFrom = from - Math.floor(diffTime * 0.5);
    const timeParams = "&startDate=" + (expandedFrom * 1000) + "&endDate=" + (periodParams.to * 1000);
    const url = "/marketdata/v1/pricehistory?symbol=" + encodeURIComponent(name) + (extended ? "&needExtendedHoursData=true" : "") + barsize + timeParams;
    const response = await request(url);
    if (!response.ok) throw new Error("获取历史数据失败" + name);
    const data = await response.json();
    assertCurrent();
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
    return [...unique.values()].sort((a, b) => a.time - b.time);
}

const RESOLUTIONS_MS = {
    '1': 60_000, '2': 120_000, '3': 180_000, '5': 300_000,
    '10': 600_000, '15': 900_000, '30': 1_800_000, '60': 3_600_000,
    '120': 7_200_000, '180': 10_800_000, '240': 14_400_000, '480': 28_800_000,
    '1D': 86_400_000
};

export function mergeSessionBars(schwab, night) {
    const unique = new Map();
    for (const bar of schwab) {
        if (!isOvernightET(bar.time)) unique.set(bar.time, bar);
    }
    for (const bar of night) unique.set(bar.time, bar);
    return [...unique.values()].sort((a, b) => a.time - b.time);
}

export function getBucketTime(resolution, timestamp = Date.now()) {
    // Calendar months vary in length; align to their first day in UTC.
    if (resolution === '1M') {
        const d = new Date(timestamp);
        return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1);
    }

    // The Unix epoch was a Thursday; offset weeks to start on Monday.
    if (resolution === '1W') {
        const WEEK_MS = 604_800_000;
        const OFFSET = 3 * 86_400_000;
        return Math.floor((timestamp + OFFSET) / WEEK_MS) * WEEK_MS - OFFSET;
    }

    const ms = RESOLUTIONS_MS[resolution];
    return Math.floor(timestamp / ms) * ms;
}
