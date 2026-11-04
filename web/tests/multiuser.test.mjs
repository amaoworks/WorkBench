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
const { default: InvestmentAdminSettings } = await server.ssrLoadModule("/src/modules/investment/InvestmentAdminSettings.tsx");
const { default: SettingsPage } = await server.ssrLoadModule("/src/features/settings/SettingsPage.tsx");
const { default: AdminSettingsPage } = await server.ssrLoadModule("/src/features/settings/AdminSettingsPage.tsx");
const { TelegramPanel } = await server.ssrLoadModule("/src/features/settings/TelegramPanel.tsx");
const { ModuleSettingsDialog } = await server.ssrLoadModule("/src/features/settings/ModuleSettingsDialog.tsx");
const { settingsRegistry, adminSettingsRegistry } = await server.ssrLoadModule("/src/modules/registry.ts");

function renderAs(t, role, component, { path = "/settings", seed } = {}) {
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
  client.setQueryData(["settings"], {
    appearance,
    ai: { enabled: false, model: "fixture-model", baseUrl: "https://ai.invalid/v1", hasApiKey: false },
    logging: { level: "info" },
    deployment: {
      listenAddress: "127.0.0.1:9090", dataPath: "/workspace/data.db", databaseDriver: "sqlite",
      backupSupported: true, authMode: "password", publicUrl: "https://workbench.test", allowedHosts: [],
    },
  });
  client.setQueryData(["settings", "telegram", "bot"], {
    enabled: true, chatId: "not-a-global-recipient", hasBotToken: true, failedDeliveries: 3,
    status: { lastAttemptAt: null, lastSuccessAt: null, lastError: "personal-delivery-error" },
  });
  client.setQueryData(["settings", "telegram", "recipient"], {
    enabled: true, chatId: "personal-chat-id", configured: true, hasBotToken: true,
    status: { lastAttemptAt: null, lastSuccessAt: null, lastError: "personal-delivery-error" },
  });
  client.setQueryData(["admin", "users"], { items: [user] });
  client.setQueryData(["modules"], { items: [{
    id: "todo", name: "待办", version: "1", icon: "todo", kind: "builtin", contractVersion: 1,
    navigation: [], enabled: true, observedEnabled: true,
  }] });
  seed?.(client);
  return renderToStaticMarkup(createElement(AuthContext.Provider, {
    value: { authenticated: true, mode: "password", mustChangePassword: false, user: { ...user, role } },
  }, createElement(QueryClientProvider, { client }, createElement(MemoryRouter, { initialEntries: [path] }, component))));
}

test("private browser keys are distinct for every user", () => {
  assert.notEqual(userStorageKey("alice", "conversation-id"), userStorageKey("bob", "conversation-id"));
  assert.notEqual(userStorageKey("alice", "nav.v1"), "workbench.nav.v1");
  assert.notEqual(userStorageKey("a.b", "nav.v1"), userStorageKey("a", "b.nav.v1"));
});

for (const role of ["user", "admin"]) {
  test(`${role} investment settings expose only personal OAuth and shared availability`, (t) => {
    let client;
    const html = renderAs(t, role, createElement(InvestmentSettings, { enabled: true }), {
      seed: (value) => { client = value; client.removeQueries({ queryKey: ["investment", "futu"], exact: true }); },
    });
    assert.match(html, /登录 Schwab/);
    assert.equal(client.getQueryCache().find({ queryKey: ["investment", "futu"], exact: true }), undefined, "personal authorization must not mount shared-provider queries");
    assert.match(html, /共享夜盘行情/);
    assert.doesNotMatch(html, /App Key|App Secret|Bot Token|futu-admin-account|OAuth 回调地址|启用夜盘行情|启用富途牛牛行情/);
  });

  test(`${role} personal business settings never expose global enabled toggles`, (t) => {
    const html = renderAs(t, role, createElement(ModulesPanel));
    assert.match(html, /待办设置/);
    assert.doesNotMatch(html, /role="switch"/);
  });

  test(`${role} disabled investment settings explain who can enable the business`, (t) => {
    const html = renderAs(t, role, createElement(InvestmentSettings, { enabled: false }));
    assert.match(html, /投资业务已停用/);
    if (role === "admin") {
      assert.match(html, /href="\/admin\/settings\?tab=modules"/);
      assert.match(html, /在后台设置中启用/);
    } else {
      assert.match(html, /联系管理员启用/);
      assert.doesNotMatch(html, /href="\/admin\/settings/);
    }
    assert.doesNotMatch(html, /登录 Schwab|App Key|role="switch"/);
  });
}

const builtin = (id, name, overrides = {}) => ({
  id, name, version: "1", icon: id, kind: "builtin", contractVersion: 1,
  navigation: [], enabled: true, observedEnabled: true, ...overrides,
});
const external = {
  id: "legacy", name: "历史外部业务", kind: "external", version: "1", icon: "blocks",
  contractVersion: 1, navigation: [], enabled: false, hasSettings: true,
  health: "healthy", pending: false, connectionNote: "legacy-connection-note",
};
const modules = [builtin("todo", "待办"), builtin("investment", "投资"), builtin("no_settings", "无配置业务"), external];
const seedModules = (client) => client.setQueryData(["modules"], { items: modules });

for (const role of ["user", "admin"]) {
  test(`${role} personal settings include appearance, business, recipient and security but no administration`, (t) => {
    const html = renderAs(t, role, createElement(SettingsPage));
    assert.match(html, /个人设置/);
    for (const section of ["appearance", "modules", "notifications", "security"]) assert.match(html, new RegExp(`id="settings-${section}"`));
    assert.match(html, /个人推送/);
    assert.match(html, /personal-chat-id/);
    assert.doesNotMatch(html, /id="settings-(ai|users|data)"|App Key|Bot Token|创建用户|日志等级|启用 Telegram 服务|启用 AI 服务/);
  });

  test(`${role} personal module list excludes legacy and businesses without personal configuration`, (t) => {
    const html = renderAs(t, role, createElement(ModulesPanel), { seed: seedModules });
    assert.match(html, /待办设置/);
    assert.match(html, /投资设置/);
    assert.doesNotMatch(html, /历史外部业务|无配置业务|legacy-connection-note|role="switch"|解除接入/);
  });
}

test("administration page exposes only workspace management sections", (t) => {
  const html = renderAs(t, "admin", createElement(AdminSettingsPage), { path: "/admin/settings" });
  assert.match(html, /后台设置/);
  for (const section of ["users", "modules", "ai", "notifications", "data"]) assert.match(html, new RegExp(`data-section="${section}"`));
  assert.match(html, /Bot Token/);
  assert.match(html, /启用 Telegram 服务/);
  assert.match(html, /启用 AI 服务/);
  assert.match(html, /日志等级/);
  assert.doesNotMatch(html, /个人推送|personal-chat-id|personal-delivery-error|当前密码|data-section="appearance"|data-section="security"/);
});

test("ordinary direct administration visits render permission denial without mounting administration queries", (t) => {
  let client;
  const html = renderAs(t, "user", createElement(AdminSettingsPage), {
    path: "/admin/settings?tab=users",
    seed: (value) => { client = value; client.clear(); },
  });
  assert.match(html, /无权|无权限|权限不足|仅管理员/);
  assert.doesNotMatch(html, /Bot Token|创建用户|启用 AI 服务|settings-nav/);
  assert.deepEqual(client.getQueryCache().getAll(), [], "denied pages must not create any settings or administration query");
});

test("administration business settings expose global controls and only registered shared settings", (t) => {
  const html = renderAs(t, "admin", createElement(ModulesPanel, { scope: "admin" }), { seed: seedModules });
  assert.match(html, /历史外部业务/);
  assert.match(html, /legacy-connection-note/);
  assert.match(html, /无配置业务/);
  assert.match(html, /role="switch"/);
  assert.match(html, /解除接入/);
  assert.match(html, /投资设置/);
  assert.doesNotMatch(html, /待办设置|无配置业务设置|历史外部业务设置/);
});

for (const role of ["user", "admin"]) {
  test(`${role} disabled personal businesses show role-specific enablement guidance instead of inactive settings`, (t) => {
    const html = renderAs(t, role, createElement(ModulesPanel), {
      seed: (client) => client.setQueryData(["modules"], { items: [builtin("todo", "待办", { enabled: false, observedEnabled: false })] }),
    });
    assert.doesNotMatch(html, /待办设置|role="switch"/);
    if (role === "admin") {
      assert.match(html, /在后台设置中启用/);
      assert.match(html, /href="\/admin\/settings\?tab=modules"/);
    } else {
      assert.match(html, /联系管理员启用/);
      assert.doesNotMatch(html, /href="\/admin\/settings/);
    }
  });
}

test("failed business retries belong only to administration", (t) => {
  const seed = (client) => client.setQueryData(["modules"], { items: [builtin("investment", "投资", { lastError: "fixture-startup-error" })] });
  const personal = renderAs(t, "admin", createElement(ModulesPanel), { seed });
  assert.doesNotMatch(personal, />重试<|role="switch"/);
  const admin = renderAs(t, "admin", createElement(ModulesPanel, { scope: "admin" }), { seed });
  assert.match(admin, />重试</);
  assert.match(admin, /fixture-startup-error/);
});

test("shared investment configuration contains provider credentials but never personal OAuth actions", (t) => {
  const html = renderAs(t, "admin", createElement(InvestmentAdminSettings, { enabled: true }));
  assert.match(html, /App Key/);
  assert.match(html, /App Secret/);
  assert.match(html, /OAuth 回调地址/);
  assert.match(html, /futu-admin-account/);
  assert.match(html, /启用夜盘行情/);
  assert.doesNotMatch(html, /登录 Schwab|重新授权|断开连接/);
});

test("business settings registries separate personal and shared capabilities", (t) => {
  assert.ok(settingsRegistry.todo);
  assert.ok(settingsRegistry.investment);
  assert.ok(adminSettingsRegistry.investment);
  assert.equal(adminSettingsRegistry.todo, undefined);
  assert.match(renderAs(t, "admin", createElement(ModuleSettingsDialog, { module: builtin("todo", "待办") })), /待办设置/);
  assert.equal(settingsRegistry.no_settings, undefined);
  assert.equal(adminSettingsRegistry.no_settings, undefined);
  assert.match(renderAs(t, "admin", createElement(ModuleSettingsDialog, { module: builtin("investment", "投资"), scope: "admin" })), /投资设置/);
});

test("Telegram service panel never mounts the administrator's personal recipient query", (t) => {
  let client;
  const html = renderAs(t, "admin", createElement(TelegramPanel), {
    path: "/admin/settings?tab=notifications",
    seed: (value) => { client = value; client.removeQueries({ queryKey: ["settings", "telegram", "recipient"], exact: true }); },
  });
  assert.match(html, /Bot Token/);
  assert.match(html, /启用 Telegram 服务/);
  assert.doesNotMatch(html, /个人推送|Chat ID|发送测试消息|personal-delivery-error|最近成功|失败投递/);
  assert.equal(client.getQueryCache().find({ queryKey: ["settings", "telegram", "recipient"], exact: true }), undefined);
});

for (const role of ["user", "admin"]) {
  test(`${role} unconfigured Schwab applications have actionable role-specific guidance`, (t) => {
    const html = renderAs(t, role, createElement(InvestmentSettings, { enabled: true }), {
      seed: (client) => client.setQueryData(["investment", "schwab"], {
        configured: false, appKey: "", hasAppSecret: false, callbackUrl: "", connected: false,
        reauthorizationRequired: false, lastError: "",
      }),
    });
    if (role === "admin") assert.match(html, /href="\/admin\/settings\?tab=modules(?:&amp;|&)module=investment"/);
    else {
      assert.match(html, /联系管理员/);
      assert.doesNotMatch(html, /href="\/admin\/settings/);
    }
    assert.doesNotMatch(html, /App Key|App Secret/);
  });
}
