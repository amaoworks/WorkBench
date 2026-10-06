export function bindChartLayout(widget, preferences, { beforeCapture = () => {}, report = () => {} } = {}) {
    let timer, disposed = false, generation = 0;
    const capture = () => {
        if (disposed) return Promise.resolve();
        clearTimeout(timer);
        beforeCapture();
        const current = ++generation;
        return new Promise(resolve => {
            const failed = error => { report(error); resolve(); };
            try {
                const saving = widget.save(layout => {
                    if (generation === current) preferences.set('layout', layout);
                    resolve();
                }, { includeDrawings: true });
                saving?.catch(failed);
            } catch (error) { failed(error); }
        });
    };
    const schedule = () => {
        if (disposed) return;
        clearTimeout(timer);
        timer = setTimeout(capture, 250);
    };
    widget.subscribe('onAutoSaveNeeded', capture);
    return {
        capture, schedule,
        dispose() {
            if (disposed) return;
            const saved = capture();
            disposed = true;
            clearTimeout(timer);
            widget.unsubscribe('onAutoSaveNeeded', capture);
            return saved;
        },
    };
}
