import { normalizeChartState } from './chart-state.js';

const ENDPOINT = '/api/modules/investment/preferences';
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);

function validateEntries(body) {
    if (!object(body?.entries)) throw new Error('投资设置响应无效，请重新加载。');
    for (const [name, entry] of Object.entries(body.entries)) {
        if (!object(entry) || !Number.isSafeInteger(entry.revision) || entry.revision < 1 || !Object.hasOwn(entry, 'value')) throw new Error('投资设置响应无效，请重新加载。');
        const value = entry.value;
        if (value === null) continue;
        if (name.startsWith('tv:') && typeof value !== 'string' || name === 'account' && typeof value !== 'string' ||
            name === 'chart' && (!object(value) || JSON.stringify(normalizeChartState(value)) !== JSON.stringify({ symbol: value.symbol, resolution: value.resolution, session: value.session })) ||
            name === 'layout' && (!object(value) || !Array.isArray(value.charts) || !value.charts.length)) throw new Error('投资设置响应无效，请重新加载。');
    }
    return body.entries;
}

export async function loadPreferences({ request = fetch, report = () => {} } = {}) {
    const [response, csrf] = await Promise.all([
        request(ENDPOINT, { credentials: 'same-origin', signal: AbortSignal.timeout(10000) }),
        request('/api/auth/csrf', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) }),
    ]);
    if (!response.ok || !csrf.ok) throw new Error('无法读取投资设置，请重新加载。');
    const entries = validateEntries(await response.json());
    let token = (await csrf.json()).token;
    if (typeof token !== 'string' || !token) throw new Error('无法读取投资设置，请重新加载。');
    const writer = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
    const values = new Map(Object.entries(entries).map(([name, entry]) => [name, entry.value]));
    const writes = new Map(), errors = new Map();
    let timer, running;
    const notify = () => report([...errors.values()].find(error => error.conflict) || [...errors.values()][0] || null);
    const transmit = async draft => {
        const body = JSON.stringify(draft);
        // Fetch keepalive has a 64 KiB quota. Large layouts save during normal use.
        const send = () => request(ENDPOINT, {
            method: 'PUT', credentials: 'same-origin', keepalive: new TextEncoder().encode(body).length < 60000,
            signal: AbortSignal.timeout(15000), headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token }, body,
        });
        let response = await send();
        if (response.status === 400 && (await response.clone().json().catch(() => null))?.code === 'csrf_failed') {
            const refreshed = await request('/api/auth/csrf', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
            if (!refreshed.ok) throw new Error('无法保存投资设置，请重试。');
            token = (await refreshed.json()).token;
            if (typeof token !== 'string' || !token) throw new Error('无法保存投资设置，请重试。');
            response = await send();
        }
        const result = await response.json().catch(() => null);
        if (!response.ok) {
            const error = new Error(result?.message || '无法保存投资设置，请重试。');
            error.conflict = response.status === 409;
            throw error;
        }
        if (!Number.isSafeInteger(result?.revision) || !Number.isSafeInteger(result?.sequence) || result.sequence < draft.sequence) throw new Error('无法确认投资设置已保存，请重试。');
    };
    const flush = () => {
        clearTimeout(timer);
        if (running) return running;
        running = (async () => {
            while (true) {
                const next = [...writes.values()].find(write => write.pending && !errors.has(write.name));
                if (!next) break;
                const draft = next.pending;
                try {
                    await transmit(draft);
                    if (next.pending === draft) next.pending = null;
                    errors.delete(next.name);
                } catch (error) {
                    errors.set(next.name, error);
                }
                notify();
            }
        })().finally(() => { running = null; });
        return running;
    };
    const store = {
        get: (name, fallback = null) => values.has(name) ? values.get(name) : fallback,
        has: name => values.has(name),
        set(name, value) {
            if (values.has(name) && JSON.stringify(values.get(name)) === JSON.stringify(value)) return;
            values.set(name, structuredClone(value));
            let write = writes.get(name);
            if (!write) {
                write = { name, revision: entries[name]?.revision || 0, sequence: 0 };
                writes.set(name, write);
            }
            write.pending = { name, value: structuredClone(value), revision: write.revision, writer, sequence: ++write.sequence };
            clearTimeout(timer);
            timer = setTimeout(() => { void flush(); }, 80);
        },
        settingsAdapter() {
            return {
                initialSettings: Object.fromEntries([...values].filter(([name, value]) => name.startsWith('tv:') && value !== null).map(([name, value]) => [name.slice(3), value])),
                setValue: (key, value) => store.set(`tv:${key}`, value),
                removeValue: key => store.set(`tv:${key}`, null),
            };
        },
        flush,
        retry() {
            for (const [name, error] of errors) if (!error.conflict) errors.delete(name);
            return flush();
        },
        leave() {
            clearTimeout(timer);
            // Do not let an earlier in-flight write postpone the last selections.
            for (const write of writes.values()) if (write.pending && !errors.has(write.name)) {
                void transmit(write.pending).catch(() => {});
            }
        },
    };
    return store;
}
