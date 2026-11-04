import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { createServer } from "vite";
import { apiResponse, resetIdentityRequests, setAPIIdentity } from "../src/shared/api.ts";
import { authStatusSchema, loginResponseSchema } from "../src/shared/schema.ts";
import { settingsSchema, createdUserSchema, managedUsersSchema, resetUserPasswordSchema } from "../src/features/settings/schema.ts";

const user = {
  id: "alice", username: "alice", displayName: "Alice", role: "user", status: "active", mustChangePassword: false,
  createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z",
};
const appearance = { theme: "system", motion: "full", palette: "default" };

test("authenticated responses require a validated identity", () => {
  const status = { authenticated: true, mode: "password", mustChangePassword: false, user };
  assert.equal(authStatusSchema.safeParse(status).success, true);
  assert.equal(authStatusSchema.safeParse({ ...status, user: undefined }).success, false);
  assert.equal(authStatusSchema.safeParse({ ...status, user: { ...user, role: "owner" } }).success, false);
  assert.equal(authStatusSchema.safeParse({ ...status, user: { ...user, status: "unknown" } }).success, false);
  assert.equal(authStatusSchema.safeParse({ authenticated: false, mode: "password", mustChangePassword: false }).success, true);
  assert.equal(loginResponseSchema.safeParse({ authenticated: true, mustChangePassword: false, user }).success, true);
});

test("personal settings omit management fields without weakening supplied management data", () => {
  assert.deepEqual(settingsSchema.parse({ appearance }), { appearance });
  assert.equal(settingsSchema.safeParse({ appearance, ai: { enabled: true } }).success, false);
  assert.equal(settingsSchema.safeParse({ appearance, logging: { level: "trace" } }).success, false);
  assert.equal(settingsSchema.safeParse({ appearance, deployment: { authMode: "password" } }).success, false);
});

test("user administration validates list, creation and password reset envelopes", () => {
  assert.equal(managedUsersSchema.safeParse({ items: [user] }).success, true);
  assert.equal(createdUserSchema.safeParse({ user, initialPassword: "Generated-123" }).success, true);
  assert.equal(createdUserSchema.safeParse({ user }).success, false);
  assert.equal(resetUserPasswordSchema.safeParse({ initialPassword: "Generated-123" }).success, true);
  assert.equal(resetUserPasswordSchema.safeParse({ user }).success, false);
});

test("identity changes abort old workspace requests but retain the outer authentication request", async (t) => {
  resetIdentityRequests();
  const signals = new Map();
  t.mock.method(globalThis, "fetch", async (path, init) => {
    signals.set(path, init.signal);
    if (path === "/api/auth/status") return Response.json({});
    return new Promise((resolve, reject) => {
      if (path === "/new") resolve(Response.json({}));
      else init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true });
    });
  });
  const old = apiResponse("/old");
  await apiResponse("/api/auth/status");
  const rejected = assert.rejects(old, { name: "AbortError" });
  resetIdentityRequests();
  await rejected;
  assert.equal(signals.get("/old").aborted, true);
  assert.equal(signals.get("/api/auth/status"), undefined);
  await apiResponse("/new");
  assert.equal(signals.get("/new").aborted, false);
  resetIdentityRequests();
});

test("caller cancellation remains effective inside the identity request scope", async (t) => {
  const caller = new AbortController();
  let signal;
  t.mock.method(globalThis, "fetch", async (_path, init) => {
    signal = init.signal;
    return Response.json({});
  });
  await apiResponse("/fixture", { signal: caller.signal });
  caller.abort();
  assert.equal(signal.aborted, true);
  resetIdentityRequests();
});

test("outgoing requests pin the user before awaiting CSRF, and old writes cannot acquire the next identity", async (t) => {
  resetIdentityRequests();
  setAPIIdentity("alice");
  const csrf = Promise.withResolvers();
  const sent = [];
  t.mock.method(globalThis, "fetch", async (path, init) => {
    if (path === "/api/auth/csrf") return csrf.promise;
    sent.push({ path, user: init.headers.get("X-Workbench-User") });
    return Response.json({});
  });
  const old = apiResponse("/api/modules/todo/tasks", { method: "POST", body: "{}" });
  const rejected = assert.rejects(old, { name: "AbortError" });
  setAPIIdentity("bob");
  csrf.resolve(Response.json({ token: "old-csrf" }));
  await rejected;
  assert.equal(sent.length, 0);
  await apiResponse("/api/modules/todo/tasks");
  assert.deepEqual(sent, [{ path: "/api/modules/todo/tasks", user: "bob" }]);
  resetIdentityRequests();
});

const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)), configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false }, appType: "custom",
});
after(() => server.close());
const { AuthContext, userStorageKey } = await server.ssrLoadModule("/src/shared/auth.ts");
const { default: InvestmentSettings } = await server.ssrLoadModule("/src/modules/investment/InvestmentSettings.tsx");
const { ModulesPanel } = await server.ssrLoadModule("/src/features/settings/ModulesPanel.tsx");

function renderAs(t, role, component) {
  const previousWindow = globalThis.window;
  globalThis.window = { location: { protocol: "https:", origin: "https://workbench.test" }, matchMedia: () => ({ matches: false }) };
  t.after(() => {
    if (previousWindow === undefined) delete globalThis.window;
    else globalThis.window = previousWindow;
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  t.after(() => client.clear());
  client.setQueryData(["investment", "schwab"], {
    configured: true, appKey: role === "admin" ? "shared-app" : "", hasAppSecret: role === "admin",
    callbackUrl: role === "admin" ? "https://workbench.test/oauth/schwab" : "", connected: false,
    reauthorizationRequired: false, lastError: "",
  });
  client.setQueryData(["investment", "overnight"], { enabled: true, provider: "futu", providerEnabled: true });
  client.setQueryData(["investment", "futu"], {
    enabled: true, overnightEnabled: true, managed: true, serviceState: "running", serviceError: "",
    account: "futu-admin-account", hasPassword: true, connected: true, qotLogined: true, lastError: "",
  });
  client.setQueryData(["settings"], { appearance });
  client.setQueryData(["modules"], { items: [{
    id: "todo", name: "待办", version: "1", icon: "todo", kind: "builtin", contractVersion: 1,
    navigation: [], enabled: true, observedEnabled: true,
  }] });
  return renderToStaticMarkup(createElement(AuthContext.Provider, {
    value: { authenticated: true, mode: "password", mustChangePassword: false, user: { ...user, role } },
  }, createElement(QueryClientProvider, { client }, createElement(MemoryRouter, null, component))));
}

test("private browser keys are distinct for every user", () => {
  assert.notEqual(userStorageKey("alice", "conversation-id"), userStorageKey("bob", "conversation-id"));
  assert.notEqual(userStorageKey("alice", "nav.v1"), "workbench.nav.v1");
  assert.notEqual(userStorageKey("a.b", "nav.v1"), userStorageKey("a", "b.nav.v1"));
});

test("ordinary investment settings expose personal OAuth without shared credentials or provider controls", (t) => {
  const html = renderAs(t, "user", createElement(InvestmentSettings, { enabled: true }));
  assert.match(html, /登录 Schwab/);
  assert.match(html, /共享夜盘行情/);
  assert.doesNotMatch(html, /App Key|App Secret|Bot Token|futu-admin-account|OAuth 回调地址/);
});

test("administrators retain Schwab application and shared Futu settings", (t) => {
  const html = renderAs(t, "admin", createElement(InvestmentSettings, { enabled: true }));
  assert.match(html, /App Key/);
  assert.match(html, /App Secret/);
  assert.match(html, /OAuth 回调地址/);
  assert.match(html, /futu-admin-account/);
});

test("ordinary users retain personal business settings but not global enabled toggles", (t) => {
  const html = renderAs(t, "user", createElement(ModulesPanel));
  assert.match(html, /待办设置/);
  assert.doesNotMatch(html, /role="switch"/);
  assert.match(renderAs(t, "admin", createElement(ModulesPanel)), /role="switch"/);
});
