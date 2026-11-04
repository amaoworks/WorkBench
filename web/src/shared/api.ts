export const AUTH_CHANNEL = "workbench-auth";

export function broadcastIdentity(userId: string | null) {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel(AUTH_CHANNEL);
  channel.postMessage({ type: "identity", userId });
  channel.close();
}

let csrfToken: string | null = null;
let identityRequests = new AbortController();
let identityUserId: string | null = null;

export function setAPIIdentity(userId: string) {
  if (identityUserId === userId && !identityRequests.signal.aborted) return;
  resetIdentityRequests();
  identityUserId = userId;
}

// An identity transition cancels reads, writes and streaming response bodies from the old workspace.
export function resetIdentityRequests() {
  identityRequests.abort();
  identityRequests = new AbortController();
  identityUserId = null;
  csrfToken = null;
  csrfPending = null;
}

export class APIError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

let csrfPending: Promise<string> | null = null;

// Concurrent first writes share one token read.
function getCSRFToken(): Promise<string> {
  if (csrfToken) return Promise.resolve(csrfToken);
  const identity = identityRequests;
  csrfPending ??= (async () => {
    const headers = new Headers();
    if (identityUserId) headers.set("X-Workbench-User", identityUserId);
    const response = await fetch("/api/auth/csrf", { credentials: "same-origin", headers, signal: identity.signal });
    if (!response.ok) throw new Error("无法初始化安全令牌");
    const body = (await response.json()) as { token?: unknown } | null;
    if (typeof body?.token !== "string" || !body.token) throw new Error("安全令牌响应格式不正确");
    identity.signal.throwIfAborted();
    csrfToken = body.token;
    return body.token;
  })().finally(() => {
    if (identity === identityRequests) csrfPending = null;
  });
  return csrfPending;
}

export async function apiResponse(path: string, init: RequestInit = {}): Promise<Response> {
  const identity = identityRequests;
  const userId = identityUserId;
  const unscoped = path === "/api/auth/status" || path === "/api/auth/login";
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (!unscoped && userId) headers.set("X-Workbench-User", userId);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    headers.set("X-CSRF-Token", await getCSRFToken());
  }
  const signal = unscoped ? init.signal :
    init.signal ? AbortSignal.any([init.signal, identity.signal]) : identity.signal;
  signal?.throwIfAborted();
  const response = await fetch(path, { ...init, method, headers, signal, credentials: "same-origin" });
  if (!response.ok) {
    if (response.status === 401 && !path.startsWith("/api/auth/")) window.dispatchEvent(new Event("workbench:auth-invalid"));
    const body = (await response.json().catch(() => null)) as { code?: string; message?: string } | null;
    if (body?.code === "csrf_failed") csrfToken = null;
    if (body?.code === "account_changed") {
      identity.abort();
      window.dispatchEvent(new Event("workbench:account-changed"));
    }
    throw new APIError(response.status, body?.code ?? "request_failed", body?.message ?? `请求失败 (${response.status})`);
  }
  if (method !== "GET" && ["/api/auth/logout", "/api/auth/logout-all", "/api/settings/password"].includes(path)) broadcastIdentity(null);
  return response;
}

// Unvalidated JSON stays unknown. Callers consuming fields must supply a schema.
export async function api(path: string, init: RequestInit = {}): Promise<unknown> {
  const response = await apiResponse(path, init);
  if (response.status === 204) return undefined;
  return response.json();
}

export async function apiValidated<T>(path: string, schema: { parse(value: unknown): T }, init: RequestInit = {}): Promise<T> {
  return schema.parse(await api(path, init));
}
