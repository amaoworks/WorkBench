let csrfToken: string | null = null;

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string
  ) {
    super(message);
  }
}

async function getCSRFToken(): Promise<string> {
  if (csrfToken) return csrfToken;
  const response = await fetch("/api/auth/csrf", { credentials: "same-origin" });
  if (!response.ok) throw new Error("无法初始化安全令牌");
  const body = (await response.json()) as { token: string };
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

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
	const response = await apiResponse(path, init);
  if (response.status === 204) return undefined as T;
  if (response.status === 202) return response.json() as Promise<T>;
  return response.json() as Promise<T>;
}
