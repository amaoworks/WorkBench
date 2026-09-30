import assert from 'node:assert/strict';
import test from 'node:test';
import { loadChartState, saveChartState } from '../chart/chart-state.js';

function browserStorage() {
    const values = new Map();
    return { localStorage: { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) } };
}

test('chart selections survive reopening, including option and futures symbols', () => {
    const host = browserStorage();
    assert.deepEqual(loadChartState(host), { symbol: 'AAPL', resolution: '1D', session: 'regular' });
    for (const state of [
        { symbol: 'MSFT', resolution: '15', session: 'extended' },
        { symbol: 'AAPL', resolution: '30', session: 'equity24h' },
        { symbol: 'AAPL', resolution: '1', session: 'night' },
        { symbol: 'AAPL 270115C00200000', resolution: '1W', session: 'regular' },
        { symbol: '/ES', resolution: '1M', session: 'regular' },
    ]) {
        saveChartState(state, host);
        assert.deepEqual(loadChartState(host), state);
    }
});

test('unsupported stored selections recover to valid chart defaults', () => {
    const host = browserStorage();
    host.localStorage.getItem = () => '{invalid json';
    assert.deepEqual(loadChartState(host), { symbol: 'AAPL', resolution: '1D', session: 'regular' });
    host.localStorage.getItem = () => JSON.stringify({ symbol: ' ', resolution: '10', session: 'unknown' });
    assert.deepEqual(loadChartState(host), { symbol: 'AAPL', resolution: '1D', session: 'regular' });
    for (const session of ['extended', 'night', 'equity24h']) {
        host.localStorage.getItem = () => JSON.stringify({ symbol: 'MSFT', resolution: '1D', session });
        assert.deepEqual(loadChartState(host), { symbol: 'MSFT', resolution: '5', session });
    }
});

test('unavailable browser storage does not prevent chart startup or selection', () => {
    const host = { get localStorage() { throw new Error('storage disabled'); } };
    assert.deepEqual(loadChartState(host), { symbol: 'AAPL', resolution: '1D', session: 'regular' });
    assert.doesNotThrow(() => saveChartState({ symbol: 'MSFT', resolution: '5', session: 'extended' }, host));
});
