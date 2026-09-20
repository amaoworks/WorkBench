// Older Safari/WebViews require an absolute ws(s) URL in the constructor.
export function webSocketURL(path, base = location.href) {
    const url = new URL(path, base);
    if (url.protocol === 'https:') url.protocol = 'wss:';
    else if (url.protocol === 'http:') url.protocol = 'ws:';
    return url.href;
}

export function createWebSocket(path) {
    return new WebSocket(webSocketURL(path));
}
