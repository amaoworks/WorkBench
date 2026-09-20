import { createWebSocket } from './websocket.js';

const PREFIX = "/api/modules/investment/futu";

let csrfToken = null;

async function csrf() {
    if (csrfToken) return csrfToken;
    const response = await fetch("/api/auth/csrf", { credentials: "same-origin" });
    if (!response.ok) throw new Error("无法获取 CSRF");
    const body = await response.json();
    csrfToken = body.token;
    return csrfToken;
}

export async function futuFetch(path = "", init = {}) {
    const method = (init.method ?? "GET").toUpperCase();
    const headers = new Headers(init.headers);
    if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
        headers.set("X-CSRF-Token", await csrf());
    }
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    const url = path.startsWith("http") ? path : PREFIX + path;
    const response = await fetch(url, { ...init, method, headers, credentials: "same-origin" });
    if (response.status === 400) {
        const body = await response.clone().json().catch(() => null);
        if (body?.code === "csrf_failed") {
            csrfToken = null;
            headers.set("X-CSRF-Token", await csrf());
            return fetch(url, { ...init, method, headers, credentials: "same-origin" });
        }
    }
    return response;
}

export const FUTU_WS = PREFIX + "/quote/ws";
export const NIGHT_RESOLUTIONS = new Set(["1", "5", "15", "30"]);

export function seriesKey(symbol, subsessionId) {
    return `${symbol}\0${subsessionId || "regular"}`;
}

export function isOvernightET(ms) {
    const parts = new Intl.DateTimeFormat("en-US", {
        timeZone: "America/New_York", hour: "numeric", minute: "numeric", hourCycle: "h23",
    }).formatToParts(new Date(ms));
    const hour = Number(parts.find((part) => part.type === "hour")?.value);
    const minute = Number(parts.find((part) => part.type === "minute")?.value);
    const minutes = hour * 60 + minute;
    return minutes >= 20 * 60 || minutes < 4 * 60;
}

export function usesSchwabTicks(subsessionId, timestamp) {
    const id = subsessionId || "regular";
    if (id === "night") return false;
    if (id === "24h") return !isOvernightET(timestamp);
    return true;
}

export function usesFutuTicks(subsessionId, timestamp) {
    const id = subsessionId || "regular";
    if (id === "night") return true;
    if (id === "24h") return isOvernightET(timestamp);
    return false;
}

export class FutuStream {
    constructor({ events = window, socket = createWebSocket, url = FUTU_WS, retryDelay = 3000, status = futuFetch } = {}) {
        this.events = events;
        this.socket = socket;
        this.url = url;
        this.retryDelay = retryDelay;
        this.status = status;
        this.ready = false;
        this.wanted = false;
    }
    async start() {
        if (this.wanted || this.closed) return;
        this.wanted = true;
        this.connect();
    }
    stop() {
        this.wanted = false;
        this.ready = false;
        clearTimeout(this.timer);
        this.ws?.close();
    }
    async connect() {
        if (this.closed || !this.wanted) return;
        try {
            const response = await this.status("");
            if (response.status === 409) {
                this.wanted = false;
                return;
            }
            const body = await response.clone().json().catch(() => ({}));
            if (!body.enabled || !body.overnightEnabled) {
                this.wanted = false;
                return;
            }
        } catch {
            this.timer = setTimeout(() => this.connect(), this.retryDelay);
            return;
        }
        const ws = this.socket(this.url);
        this.ws = ws;
        ws.onmessage = ({ data }) => {
            if (this.closed || this.ws !== ws) return;
            let message;
            try { message = JSON.parse(data); } catch { return; }
            if (message.stream?.status === "ready") {
                this.ready = true;
                this.events.dispatchEvent(new Event("FUTU_STREAM_READY"));
            }
            for (const item of (message.data || [])) {
                const service = item.service?.toUpperCase();
                if (service === "FUTU_KL") this.events.dispatchEvent(new CustomEvent("FUTU_KL", { detail: item }));
                if (service === "FUTU_QUOTE") this.events.dispatchEvent(new CustomEvent("FUTU_QUOTE", { detail: item }));
            }
        };
        ws.onclose = (event) => {
            if (this.ws !== ws) return;
            this.ws = undefined;
            this.ready = false;
            this.events.dispatchEvent(new Event("FUTU_STREAM_CLOSED"));
            if (!this.closed && this.wanted) this.timer = setTimeout(() => this.connect(), this.retryDelay);
            if (event?.code === 1008) this.wanted = false;
        };
        ws.onerror = () => ws.close();
    }
    close() {
        this.closed = true;
        this.wanted = false;
        this.ready = false;
        clearTimeout(this.timer);
        this.ws?.close();
    }
}
