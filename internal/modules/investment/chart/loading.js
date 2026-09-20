// Run before module imports: a failed import must not leave an empty terminal.
(() => {
    const panel = document.getElementById('terminal-loading');
    const title = document.getElementById('terminal-loading-title');
    const message = document.getElementById('terminal-loading-message');
    const actions = document.getElementById('terminal-loading-actions');
    let ready = false;
    let timer;

    const fail = (text = '图表资源未能加载，请检查网络后重试。') => {
        if (ready) return;
        clearTimeout(timer);
        panel.setAttribute('role', 'alert');
        title.textContent = '图表加载失败';
        message.textContent = text;
        actions.hidden = false;
    };
    const onError = (event) => {
        // Layout notifications can be deferred to the next frame while the
        // iframe resizes. This browser notification is not a startup failure.
        if (event.type === 'error' && ['ResizeObserver loop completed with undelivered notifications.', 'ResizeObserver loop limit exceeded'].includes(event.message)) return;
        fail();
    };
    const cleanup = () => {
        clearTimeout(timer);
        window.removeEventListener('error', onError, true);
        window.removeEventListener('unhandledrejection', onError);
    };
    window.terminalLoad = {
        ready() {
            ready = true;
            panel.hidden = true;
            cleanup();
        },
        fail,
    };
    window.addEventListener('error', onError, true);
    window.addEventListener('unhandledrejection', onError);
    window.addEventListener('pagehide', cleanup, { once: true });
    timer = setTimeout(() => fail('图表加载时间较长，可以继续等待，或重新加载。'), 30000);
    document.getElementById('terminal-retry').addEventListener('click', () => location.reload());
})();
