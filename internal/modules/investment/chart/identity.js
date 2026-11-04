// A terminal document keeps the identity it loaded with, even when another tab
// replaces the shared login cookie. This covers raw fetch, keepalive and sockets.
(() => {
    const originalFetch = window.fetch.bind(window);
    const OriginalWebSocket = window.WebSocket;
    const controller = new AbortController();
    const sockets = new Set();
    let userId, stopped = false, timer;
    const lost = () => new Error('登录用户已变更，请重新加载。');
    const invalidate = () => {
        if (stopped) return;
        stopped = true;
        clearInterval(timer);
        controller.abort(lost());
        window.dispatchEvent(new Event('terminal:identity-lost'));
        for (const socket of sockets) socket.close();
        sockets.clear();
    };
    const status = async () => {
        const response = await originalFetch('/api/auth/status', {
            credentials: 'same-origin', cache: 'no-store', signal: controller.signal,
        });
        if (!response.ok) throw lost();
        const body = await response.json();
        if (!body?.authenticated || typeof body.user?.id !== 'string' || !body.user.id) throw lost();
        return body.user.id;
    };
    const ready = status().then(id => {
        if (stopped) throw lost();
        userId = id;
        return id;
    }).catch(error => { invalidate(); throw error; });
    // Startup can fail before the charting library finishes downloading.
    ready.catch(() => {});
    const verify = async () => {
        try {
            if (await status() !== await ready) invalidate();
        } catch { invalidate(); }
    };
    window.fetch = async (input, init = {}) => {
        const url = new URL(typeof input === 'string' || input instanceof URL ? input : input.url, location.href);
        if (url.origin !== location.origin || !url.pathname.startsWith('/api/')) return originalFetch(input, init);
        const id = await ready;
        controller.signal.throwIfAborted();
        const headers = new Headers(init.headers ?? (input instanceof Request ? input.headers : undefined));
        headers.set('X-Workbench-User', id);
        const callerSignal = init.signal ?? (input instanceof Request ? input.signal : undefined);
        const signal = callerSignal ? AbortSignal.any([callerSignal, controller.signal]) : controller.signal;
        signal.throwIfAborted();
        const response = await originalFetch(input, { ...init, headers, signal });
        controller.signal.throwIfAborted();
        if (response.status === 401 || (response.status === 409 && (await response.clone().json().catch(() => null))?.code === 'account_changed')) {
            invalidate();
            throw lost();
        }
        return response;
    };
    window.WebSocket = class extends OriginalWebSocket {
        constructor(input, protocols) {
            if (!userId || stopped) throw lost();
            const url = new URL(input, location.href);
            if (url.host !== location.host || !url.pathname.startsWith('/api/')) throw new Error('无效的行情连接');
            url.searchParams.set('workbenchUser', userId);
            super(url.href, protocols);
            sockets.add(this);
            this.addEventListener('close', () => sockets.delete(this), { once: true });
        }
    };
    if (typeof BroadcastChannel !== 'undefined') {
        const channel = new BroadcastChannel('workbench-auth');
        channel.onmessage = ({ data }) => {
            if (data?.type === 'identity' && (typeof data.userId === 'string' || data.userId === null) && data.userId !== userId) invalidate();
        };
        window.addEventListener('pagehide', () => channel.close(), { once: true });
    }
    ready.then(() => {
        timer = setInterval(() => { void verify(); }, 15000);
        window.addEventListener('focus', () => { void verify(); });
        document.addEventListener('visibilitychange', () => {
            if (document.visibilityState === 'visible') void verify();
        });
    }).catch(() => {});
    window.addEventListener('pagehide', () => clearInterval(timer), { once: true });
    window.terminalIdentity = { ready, get active() { return !!userId && !stopped; } };
})();
