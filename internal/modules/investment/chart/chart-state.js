import { EQUITY_24H_SESSION, NIGHT_RESOLUTIONS } from './futu.js';

const STORAGE_KEY = 'workbench.investment.chart.v1';
const RESOLUTIONS = new Set([...NIGHT_RESOLUTIONS, '1D', '1W', '1M']);
const SESSIONS = new Set(['regular', 'extended', 'night', EQUITY_24H_SESSION]);

export const requiresMinuteResolution = session => session === 'extended' || session === 'night' || session === EQUITY_24H_SESSION;

function normalizeState(value) {
    const symbol = typeof value?.symbol === 'string' && value.symbol.trim().length > 0 && value.symbol.length <= 200
        ? value.symbol.trim() : 'AAPL';
    const session = SESSIONS.has(value?.session) ? value.session : 'regular';
    let resolution = RESOLUTIONS.has(value?.resolution) ? value.resolution : '1D';
    if (requiresMinuteResolution(session) && !NIGHT_RESOLUTIONS.has(resolution)) resolution = '5';
    return { symbol, resolution, session };
}

export function loadChartState(host = window) {
    try {
        return normalizeState(JSON.parse(host.localStorage.getItem(STORAGE_KEY)));
    } catch {
        return normalizeState(null);
    }
}

export function saveChartState(state, host = window) {
    try {
        host.localStorage.setItem(STORAGE_KEY, JSON.stringify(normalizeState(state)));
    } catch { /* Chart controls remain usable when browser storage is unavailable. */ }
}
