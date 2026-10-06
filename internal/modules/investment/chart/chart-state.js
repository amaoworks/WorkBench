import { EQUITY_24H_SESSION, NIGHT_RESOLUTIONS } from './futu.js';

const RESOLUTIONS = new Set([...NIGHT_RESOLUTIONS, '1D', '1W', '1M']);
const SESSIONS = new Set(['regular', 'extended', 'night', EQUITY_24H_SESSION]);

export const requiresMinuteResolution = session => session === 'extended' || session === 'night' || session === EQUITY_24H_SESSION;

export function normalizeChartState(value) {
    const symbol = typeof value?.symbol === 'string' && value.symbol.trim().length > 0 && value.symbol.length <= 200
        ? value.symbol.trim() : 'AAPL';
    const session = SESSIONS.has(value?.session) ? value.session : 'regular';
    let resolution = RESOLUTIONS.has(value?.resolution) ? value.resolution : '1D';
    if (requiresMinuteResolution(session) && !NIGHT_RESOLUTIONS.has(resolution)) resolution = '5';
    return { symbol, resolution, session };
}

export function loadChartState(preferences) {
    return normalizeChartState(preferences.get('chart'));
}

export function saveChartState(state, preferences) {
    preferences.set('chart', normalizeChartState(state));
}
