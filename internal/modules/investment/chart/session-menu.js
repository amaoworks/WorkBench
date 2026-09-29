import { EQUITY_24H_SESSION } from './futu.js';

const SESSIONS = [
    { id: 'regular', label: '常规 (RTH)', shortLabel: '常规' },
    { id: 'extended', label: '扩展 (ETH)', shortLabel: '扩展' },
    { id: 'night', label: '夜盘 (Night)', shortLabel: '夜盘' },
    { id: EQUITY_24H_SESSION, label: '全天 (24H)', shortLabel: '全天' },
];

// Keep the session control in TradingView's supported header toolbar API. The
// selected value follows resolved chart metadata, including cached symbols.
export function createSessionMenu(widget, { onSelect, onError = () => {}, host = window } = {}) {
    let currentSession = 'regular';
    let nightAvailable = false;
    let fullDayAvailable = false;
    let dropdown, dropdownAlign, pendingAlign;
    let generation = 0;
    let headerReady = false;
    let disposed = false;

    const buildOptions = () => {
        const selected = SESSIONS.find(session => session.id === currentSession) ?? SESSIONS[0];
        const compact = Number(host.innerWidth) <= 600;
        const items = SESSIONS
            .filter(session => session.id !== 'night' || nightAvailable)
            .filter(session => session.id !== EQUITY_24H_SESSION || fullDayAvailable)
            .map(session => ({
                title: `${session.id === currentSession ? '✓ ' : ''}${session.label}`,
                onSelect: () => { if (!disposed) onSelect?.(session.id); },
            }));
        return {
            // Put compact controls before the right-side utility buttons so the
            // native mobile toolbar does not push the menu to its clipped edge.
            align: compact ? 'left' : 'right',
            title: compact ? selected.shortLabel : `行情：${selected.shortLabel}`,
            tooltip: `行情时段：${selected.shortLabel}。切换图表行情时段。`,
            items,
        };
    };

    const updateDropdown = () => {
        if (disposed || !headerReady) return;
        const options = buildOptions();
        if (dropdown && dropdownAlign === options.align) {
            try { dropdown.applyOptions(options); } catch (error) { onError(error); }
            return;
        }
        if (pendingAlign === options.align) return;
        // applyOptions updates a dropdown, but does not move it between toolbar
        // groups. Recreate only this control at the breakpoint, never the chart.
        const request = ++generation;
        dropdown?.remove();
        dropdown = undefined;
        pendingAlign = options.align;
        Promise.resolve().then(() => {
            if (disposed || request !== generation) return null;
            return widget.createDropdown(options);
        }).then(created => {
            if (!created) return;
            if (disposed || request !== generation) {
                created.remove();
                return;
            }
            pendingAlign = undefined;
            dropdown = created;
            dropdownAlign = options.align;
            // Reconcile state changes while TradingView created the handle.
            updateDropdown();
        }).catch(error => {
            if (!disposed && request === generation) {
                pendingAlign = undefined;
                onError(error);
            }
        });
    };
    const onResize = () => updateDropdown();
    host.addEventListener?.('resize', onResize, { passive: true });

    Promise.resolve()
        .then(() => widget.headerReady())
        .then(() => { headerReady = true; updateDropdown(); })
        .catch(error => { if (!disposed) onError(error); });

    return {
        setResolvedSession(session, isNightAvailable, isFullDayAvailable) {
            if (disposed) return;
            nightAvailable = isNightAvailable === true;
            fullDayAvailable = isFullDayAvailable === true;
            currentSession = SESSIONS.some(item => item.id === session) ? session : 'regular';
            if ((currentSession === 'night' && !nightAvailable) || (currentSession === EQUITY_24H_SESSION && !fullDayAvailable)) currentSession = 'regular';
            updateDropdown();
        },
        dispose() {
            if (disposed) return;
            disposed = true;
            host.removeEventListener?.('resize', onResize);
            dropdown?.remove();
            dropdown = undefined;
        },
    };
}
