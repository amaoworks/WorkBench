// Theme changes are asynchronous in TradingView. Serialize them and read the
// latest requested theme when each update runs, including during chart startup.
export function bindTheme(widget, initialTheme, host = window) {
    let requested = initialTheme;
    let applied = initialTheme;
    let disposed = false;
    const ready = widget.chartReady();
    let pending = Promise.resolve();
    const setBackground = (theme) => {
        host.document.documentElement.style.setProperty('--terminal-background', theme === 'dark' ? '#131722' : '#ffffff');
        host.document.documentElement.style.colorScheme = theme;
    };
    setBackground(initialTheme);

    const sync = () => {
        pending = pending.then(async () => {
            await ready;
            if (disposed || applied === requested) return;
            const theme = requested;
            await widget.changeTheme(theme);
            if (disposed) return;
            applied = theme;
            setBackground(theme);
        }).catch((error) => host.console.error('无法同步投资终端主题', error));
    };
    const onMessage = (event) => {
        if (event.source !== host.parent || event.origin !== host.location.origin) return;
        if (event.data?.type !== 'workbench.theme' || !['light', 'dark'].includes(event.data.theme)) return;
        requested = event.data.theme;
        sync();
    };
    host.addEventListener('message', onMessage);
    sync();
    return () => {
        disposed = true;
        host.removeEventListener('message', onMessage);
    };
}
