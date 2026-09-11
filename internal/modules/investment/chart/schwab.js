const PREFIX = "/api/modules/investment/schwab";

let csrfToken = null;

async function csrf() {
  if (csrfToken) return csrfToken;
  const response = await fetch("/api/auth/csrf", { credentials: "same-origin" });
  if (!response.ok) throw new Error("无法获取 CSRF");
  const body = await response.json();
  csrfToken = body.token;
  return csrfToken;
}

export async function schwabFetch(path, init = {}) {
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

export const SCHWAB_WS = PREFIX + "/trader/ws";
