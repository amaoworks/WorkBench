let csrfToken: string | null = null;

export class APIError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function getCSRFToken(): Promise<string> {
  if (csrfToken) return csrfToken;
  const response = await fetch("/api/auth/csrf", { credentials: "same-origin" });
  if (!response.ok) throw new Error("无法初始化安全令牌");
  const body = (await response.json()) as { token?: unknown } | null;
  if (typeof body?.token !== "string" || !body.token) throw new Error("安全令牌响应格式不正确");
  csrfToken = body.token;
  return body.token;
}

export async function apiResponse(path: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    headers.set("X-CSRF-Token", await getCSRFToken());
  }
  const response = await fetch(path, { ...init, method, headers, credentials: "same-origin" });
  if (!response.ok) {
    const body = (await response.json().catch(() => null)) as { code?: string; message?: string } | null;
    if (body?.code === "csrf_failed") csrfToken = null;
    throw new APIError(response.status, body?.code ?? "request_failed", body?.message ?? `请求失败 (${response.status})`);
  }
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
