import { SCHWAB_WS } from './schwab.js';
import { createWebSocket } from './websocket.js';

export class SchwabStream {
    constructor({ events = window, socket = createWebSocket, url = SCHWAB_WS, retryDelay = 3000 } = {}) {
        this.events = events;
        this.socket = socket;
        this.url = url;
        this.retryDelay = retryDelay;
        this.ready = false;
        this.connect();
    }
    connect() {
        if (this.closed) return;
        const ws = this.socket(this.url);
        this.ws = ws;
        ws.onmessage = ({ data }) => {
            if (this.closed || this.ws !== ws) return;
            let message;
            try { message = JSON.parse(data); } catch { return; }
            if (message.stream?.status === 'ready') {
                this.ready = true;
                this.events.dispatchEvent(new Event('SCHWAB_STREAM_READY'));
            }
            for (const item of [...(message.response || []), ...(message.data || [])]) {
                const service = item.service?.toUpperCase();
                if (!service) continue;
                this.events.dispatchEvent(new CustomEvent(service, { detail: item }));
                if (service.startsWith('LEVELONE_')) this.events.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: item }));
            }
        };
        ws.onclose = () => {
            if (this.ws !== ws) return;
            this.ws = undefined;
            this.ready = false;
            this.events.dispatchEvent(new Event('SCHWAB_STREAM_CLOSED'));
            if (!this.closed) this.timer = setTimeout(() => this.connect(), this.retryDelay);
        };
        ws.onerror = () => ws.close();
    }
    close() {
        this.closed = true;
        this.ready = false;
        clearTimeout(this.timer);
        this.ws?.close();
    }
}
