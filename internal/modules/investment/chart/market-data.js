import { marketDataFetch } from './futu.js';

export async function loadMarketData(request = marketDataFetch) {
    const response = await request();
    if (!response.ok) throw new Error(`读取行情源失败 [HTTP ${response.status}]`);
    const body = await response.json();
    if (!['schwab', 'futu', 'none'].includes(body.provider) ||
        ['schwabConnected', 'reauthorizationRequired', 'futuEnabled', 'overnightEnabled'].some(key => typeof body[key] !== 'boolean') ||
        (body.provider === 'schwab' && (!body.schwabConnected || body.reauthorizationRequired)) ||
        (body.provider === 'futu' && !body.futuEnabled)) throw new Error('行情源状态无效');
    return body;
}

// A source change rebuilds the terminal, including TradingView's internal
// caches and broker adapter. Replacing only the socket would retain account UI.
export function watchMarketData(initial, onChange, { events = window, document = globalThis.document,
    request = marketDataFetch, interval = 15000, onError = () => {} } = {}) {
    let disposed = false, pending;
    const keys = ['provider', 'schwabConnected', 'reauthorizationRequired', 'futuEnabled', 'overnightEnabled'];
    const check = () => {
        if (disposed || pending) return pending;
        pending = loadMarketData(request).then(next => {
            if (!disposed && keys.some(key => next[key] !== initial[key])) {
                dispose();
                onChange(next);
            }
        }).catch(error => { if (!disposed) onError(error); }).finally(() => { pending = undefined; });
        return pending;
    };
    const visible = () => { if (document?.visibilityState === 'visible') void check(); };
    const timer = setInterval(() => { void check(); }, interval);
    const dispose = () => {
        disposed = true;
        clearInterval(timer);
        events.removeEventListener('focus', check);
        events.removeEventListener('SCHWAB_STREAM_CLOSED', check);
        events.removeEventListener('terminal:market-data-changed', check);
        document?.removeEventListener('visibilitychange', visible);
    };
    events.addEventListener('focus', check);
    events.addEventListener('SCHWAB_STREAM_CLOSED', check);
    events.addEventListener('terminal:market-data-changed', check);
    document?.addEventListener('visibilitychange', visible);
    return dispose;
}
