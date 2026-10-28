// Run before module imports: a failed import must not leave an empty terminal.
// A normal first open can take tens of seconds, so slowness alone is never a
// failure: after SLOW_MS the cover offers a reload while it keeps waiting, and
// it fails only when no startup step has made progress for STALL_MS.
(() => {
    const SLOW_MS = 20000;
    const STALL_MS = 90000;
    const panel = document.getElementById('terminal-loading');
    const title = document.getElementById('terminal-loading-title');
    const message = document.getElementById('terminal-loading-message');
    const stepLine = document.getElementById('terminal-loading-step');
    const actions = document.getElementById('terminal-loading-actions');
    const steps = {
        library: '加载图表库',
        preferences: '读取图表设置',
        chart: '初始化图表',
        symbol: '查询标的信息',
        bars: '读取 K 线数据',
        render: '绘制图表',
    };
    let ready = false;
    let failed = false;
    let current = 'library';
    let slowTimer, stallTimer;
    const label = () => steps[current] || current;
    const notify = (type, extra = {}) => {
        // The parent page keeps its own watchdog in case this script never runs.
        try { window.parent.postMessage({ type, step: current, ...extra }, location.origin); } catch { /* opened standalone */ }
    };
    const fail = (text = '图表资源未能加载，请检查网络后重试。') => {
        if (ready || failed) return;
        failed = true;
        clearTimeout(slowTimer);
        clearTimeout(stallTimer);
        panel.setAttribute('role', 'alert');
        title.textContent = '图表加载失败';
        message.textContent = text;
        stepLine.textContent = `停在：${label()}`;
        actions.hidden = false;
        notify('workbench.terminal.failed', { message: text });
    };
    const armStall = () => {
        clearTimeout(stallTimer);
        stallTimer = setTimeout(() => fail(`${label()}超过 ${STALL_MS / 1000} 秒没有进展，请重新加载。`), STALL_MS);
    };
    const step = (name) => {
        if (ready || failed) return;
        current = name;
        stepLine.textContent = `当前步骤：${label()}…`;
        armStall();
        notify('workbench.terminal.progress');
    };
    const onError = (event) => {
        // Layout notifications can be deferred to the next frame while the
        // iframe resizes. This browser notification is not a startup failure.
        if (event.type === 'error' && ['ResizeObserver loop completed with undelivered notifications.', 'ResizeObserver loop limit exceeded'].includes(event.message)) return;
        fail();
    };
    const cleanup = () => {
        clearTimeout(slowTimer);
        clearTimeout(stallTimer);
        window.removeEventListener('error', onError, true);
        window.removeEventListener('unhandledrejection', onError);
    };
    window.terminalLoad = {
        ready() {
            if (ready) return;
            ready = true;
            panel.hidden = true;
            cleanup();
            notify('workbench.terminal.ready');
        },
        step,
        fail,
        get failed() { return failed; },
    };
    window.addEventListener('error', onError, true);
    window.addEventListener('unhandledrejection', onError);
    window.addEventListener('pagehide', cleanup, { once: true });
    slowTimer = setTimeout(() => {
        if (ready || failed) return;
        message.textContent = '还在加载';
        actions.hidden = false;
        notify('workbench.terminal.slow');
    }, SLOW_MS);
    step('library');
    document.getElementById('terminal-retry').addEventListener('click', () => location.reload());
})();
