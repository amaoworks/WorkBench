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

export async function overnightFetch() {
    return fetch("/api/modules/investment/overnight", {
        method: "GET",
        credentials: "same-origin",
    });
}

export const FUTU_WS = PREFIX + "/quote/ws";
export const NIGHT_RESOLUTIONS = new Set(["1", "5", "15", "30"]);
export const EQUITY_24H_SESSION = "equity24h";

export function seriesKey(symbol, subsessionId) {
    return `${symbol}\0${subsessionId || "regular"}`;
}

const ET_CLOCK = new Intl.DateTimeFormat("en-US", {
    timeZone: "America/New_York", hour: "numeric", minute: "numeric", hourCycle: "h23",
});
const ET_DATE = new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/New_York", year: "numeric", month: "2-digit", day: "2-digit",
});

function etParts(formatter, ms) {
    return Object.fromEntries(formatter.formatToParts(new Date(ms)).map(part => [part.type, part.value]));
}

export function etMinuteOfDay(ms) {
    if (!Number.isFinite(Number(ms))) return NaN;
    const parts = etParts(ET_CLOCK, Number(ms));
    return Number(parts.hour) * 60 + Number(parts.minute);
}

export function isOvernightET(ms) {
    const minutes = etMinuteOfDay(ms);
    return minutes >= 20 * 60 || minutes < 4 * 60;
}

export function isSchwabExtendedET(ms) {
    const minutes = etMinuteOfDay(ms);
    return minutes >= 4 * 60 && minutes < 20 * 60;
}

export function equityTradingDateET(ms) {
    const parts = etParts(ET_DATE, Number(ms) - 4 * 60 * 60 * 1000);
    return `${parts.year}-${parts.month}-${parts.day}`;
}

export function usesSchwabTicks(subsessionId, timestamp) {
    const id = subsessionId || "regular";
    if (id === "night") return false;
    if (id === EQUITY_24H_SESSION) return isSchwabExtendedET(timestamp);
    return true;
}

export function usesFutuTicks(subsessionId, timestamp) {
    const id = subsessionId || "regular";
    if (id === "night") return true;
    if (id === EQUITY_24H_SESSION) return isOvernightET(timestamp);
    return false;
}

export class FutuStream {
    constructor({ events = window, socket = createWebSocket, url = FUTU_WS, retryDelay = 3000, status = overnightFetch } = {}) {
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
            if (this.closed || !this.wanted) return;
            if (response.status === 409) {
                this.wanted = false;
                return;
            }
            if (!response.ok) throw new Error("无法读取夜盘设置");
            const body = await response.clone().json().catch(() => ({}));
            if (this.closed || !this.wanted) return;
            if (!body.enabled || !body.providerEnabled) {
                this.wanted = false;
                return;
            }
        } catch {
            if (this.closed || !this.wanted) return;
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
                this.events.dispatchEvent(new CustomEvent("FUTU_STREAM_READY", {
                    detail: { generation: message.stream.generation },
                }));
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
