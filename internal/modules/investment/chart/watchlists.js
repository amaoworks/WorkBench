const ENDPOINT = '/api/modules/investment/watchlists';
const DRAFT_KEY = 'workbench.investment.watchlists.pending';
const nextTask = () => new Promise(resolve => setTimeout(resolve, 0));

function draftStorage() {
    try { return globalThis.sessionStorage; } catch { return null; }
}

export async function loadWatchlists(request = fetch, storage = draftStorage()) {
    const readDraft = () => { try { return storage?.getItem(DRAFT_KEY); } catch { return null; } };
    const discardDraft = () => { try { storage?.removeItem(DRAFT_KEY); } catch { /* browser storage unavailable */ } };
    // Fetch while the chart starts, including the CSRF token needed for an
    // immediate save when the user edits or leaves the page.
    const [response, csrf] = await Promise.all([
        request(ENDPOINT, { credentials: 'same-origin', signal: AbortSignal.timeout(10000) }),
        request('/api/auth/csrf', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) }),
    ]);
    if (!response.ok || !csrf.ok) throw new Error('无法读取已保存的自选表，请重新加载。');
    let initial = await response.json();
    let token = (await csrf.json()).token;
    if (!Number.isSafeInteger(initial.revision) || initial.revision < 0 || !token || !('state' in initial)) throw new Error('自选表响应无效，请重新加载。');
    const transmit = async draft => {
        const body = JSON.stringify(draft);
        if (new TextEncoder().encode(body).length > 24 * 1024) throw new Error('自选表内容超过保存限制，请减少列表或标的。');
        const send = () => request(ENDPOINT, {
            method: 'PUT', credentials: 'same-origin', keepalive: true, signal: AbortSignal.timeout(15000),
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': token }, body,
        });
        let response = await send();
        if (response.status === 400 && (await response.clone().json().catch(() => null))?.code === 'csrf_failed') {
            const refreshed = await request('/api/auth/csrf', { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
            if (!refreshed.ok) throw new Error('无法确认自选表已保存，请重试。');
            token = (await refreshed.json()).token;
            response = await send();
        }
        const result = await response.json().catch(() => null);
        if (!response.ok) {
            const error = new Error(result?.message || '无法确认自选表已保存，请重试。');
            error.conflict = response.status === 409;
            throw error;
        }
        if (!Number.isSafeInteger(result?.revision) || !Number.isSafeInteger(result?.sequence) || result.sequence < draft.sequence) throw new Error('无法确认自选表已保存，请重试。');
        // Never clear a newer draft when an older request finishes late.
        if (readDraft() === body) discardDraft();
        return result.revision;
    };
    let recoveryError, recoveryDraft;
    const previous = readDraft();
    if (previous) {
        const draft = JSON.parse(previous);
        try {
            // Resolve the previous document's pending write before using a GET
            // that may have raced its keepalive request during refresh.
            await transmit(draft);
            const latest = await request(ENDPOINT, { credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
            if (!latest.ok) throw new Error('无法读取已保存的自选表，请重新加载。');
            initial = await latest.json();
        } catch (error) {
            recoveryError = error;
            recoveryDraft = draft;
            initial = { ...initial, state: draft.state };
        }
    }
    // Successful recovery starts a new writer. Failed recovery keeps the old
    // draft's base version so reload/edit cannot bypass a remote conflict.
    const writer = recoveryDraft?.writer || Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('');
    const baseRevision = recoveryDraft?.revision ?? initial.revision;
    let sequence = recoveryDraft?.sequence ?? 0, staged = recoveryDraft;
    const stage = state => {
        if (!staged || JSON.stringify(staged.state) !== JSON.stringify(state)) {
            staged = { revision: baseRevision, writer, sequence: ++sequence, state };
            try { storage?.setItem(DRAFT_KEY, JSON.stringify(staged)); } catch { /* server saving still works without browser storage */ }
        }
        return staged;
    };
    return {
        initial, recoveryError, stage,
        discardDraft,
        save: (_revision, state) => transmit(stage(state)),
    };
}

function snapshot(api) {
    return {
        lists: Object.values(api.getAllLists() || {}).map(({ id, title, symbols }) => ({ id, title, symbols: [...symbols] })),
        activeId: api.getActiveListId() || '',
    };
}

export async function bindWatchlists(widget, { initial, save, stage = () => {}, recoveryError }, report = () => {}) {
    const api = await widget.watchList();
    if (!api) throw new Error('无法恢复自选表，请重新加载。');
    if (initial.state !== null) {
        const { lists, activeId } = initial.state;
        const previous = Object.keys(api.getAllLists() || {});
        // Keep stable IDs across browsers. Add saved lists before deleting local
        // defaults so the library does not create an extra fallback list.
        for (const list of lists) {
            api.createList(list.title, [...list.symbols], list.id);
        }
        await nextTask();
        for (const list of lists) {
            api.renameList(list.id, list.title);
        }
        if (activeId) api.setActiveList(activeId);
        for (const id of previous) if (!lists.some(list => list.id === id)) api.deleteList(id);
        await nextTask();
    }

    let revision = initial.revision;
    let acknowledged = initial.state === null ? '' : JSON.stringify(snapshot(api));
    let pending = null, running = null, failed = !!recoveryError, conflict = recoveryError?.conflict === true, disposed = false, timer;
    const show = error => { if (!disposed) report(error); };
    const sendPending = () => {
        if (running) return running;
        if (failed || !pending) return Promise.resolve();
        running = (async () => {
            while (pending && !failed && !disposed) {
                const state = pending;
                pending = null;
                try {
                    revision = await save(revision, state);
                    acknowledged = JSON.stringify(state);
                    if (pending && JSON.stringify(pending) === acknowledged) pending = null;
                    show(null);
                } catch (error) {
                    pending ??= state;
                    failed = true;
                    conflict = error.conflict === true;
                    show(error);
                }
            }
        })().finally(() => { running = null; });
        return running;
    };
    const capture = () => {
        const state = snapshot(api);
        // While a save is in flight, even returning to the old state needs a
        // follow-up write so that the in-flight edit cannot win afterwards.
        pending = running || failed || JSON.stringify(state) !== acknowledged ? state : null;
        if (pending) stage(pending);
    };
    const changed = () => {
        if (disposed) return;
        clearTimeout(timer);
        // The library emits list events before its async state update. Capture
        // on the next task, including additions/removals of an inactive list.
        timer = setTimeout(() => { timer = undefined; capture(); void sendPending(); }, 0);
    };
    const flush = async () => {
        if (timer !== undefined) {
            clearTimeout(timer);
            timer = undefined;
            await nextTask();
            if (!disposed) capture();
        }
        await sendPending();
    };
    const subscriptions = ['onListAdded', 'onListChanged', 'onListRemoved', 'onListRenamed', 'onActiveListChanged'].map(name => api[name]());
    for (const event of subscriptions) event.subscribe(null, changed);
    // Import the existing browser lists only when this workspace has no saved
    // state yet. An empty saved list must never revive old local symbols.
    if (initial.state === null || recoveryError) changed();
    if (recoveryError) show(recoveryError);
    return {
        flush,
        retry() { if (!conflict) { failed = false; return flush(); } },
        dispose({ discard = false } = {}) {
            if (disposed) return;
            clearTimeout(timer);
            if (!discard) capture();
            disposed = true;
            for (const event of subscriptions) event.unsubscribe(null, changed);
            // Send the newest snapshot independently of an older pending PUT.
            // The backend's writer/sequence check makes overtaking safe.
            if (!discard && pending && !conflict) void save(revision, pending).catch(() => {});
            pending = null;
        },
    };
}
