import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { createServer } from "vite";
import { marketDataStatusSchema, overviewQuotesSchema } from "../src/modules/investment/schema.ts";

const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)), configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false }, appType: "custom",
});
after(() => server.close());
const { AuthContext } = await server.ssrLoadModule("/src/shared/auth.ts");
const { default: InvestmentPage } = await server.ssrLoadModule("/src/modules/investment/InvestmentPage.tsx");
const { default: QuotesWidget } = await server.ssrLoadModule("/src/modules/investment/InvestmentQuotesWidget.tsx");
const { default: PositionsPage } = await server.ssrLoadModule("/src/modules/investment/PositionsPage.tsx");
const status = (provider, reauthorizationRequired = false) => ({
  provider, schwabConnected: provider === "schwab", reauthorizationRequired,
  futuEnabled: provider === "futu", overnightEnabled: false,
});

function render(t, { provider = "futu", reauth = false, role = "user", component, seed } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  t.after(() => client.clear());
  client.setQueryData(["investment", "market-data"], status(provider, reauth));
  client.setQueryData(["investment", "schwab"], {
    configured: true, appKey: "", callbackUrl: "", hasAppSecret: false,
    connected: provider === "schwab", reauthorizationRequired: reauth, lastError: "",
  });
  seed?.(client);
  return renderToStaticMarkup(createElement(AuthContext.Provider, {
    value: { authenticated: true, mode: "password", mustChangePassword: false,
      user: { id: "alice", username: "alice", displayName: "Alice", role, status: "active", mustChangePassword: false,
        createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" } },
  }, createElement(QueryClientProvider, { client }, createElement(MemoryRouter, { initialEntries: ["/investment"] },
    component ?? createElement(InvestmentPage)))));
}

test("market data status requires an explicit supported provider and capability flags", () => {
  for (const provider of ["schwab", "futu", "none"]) assert.equal(marketDataStatusSchema.safeParse(status(provider)).success, true);
  assert.equal(marketDataStatusSchema.safeParse(status("auto")).success, false);
  assert.equal(marketDataStatusSchema.safeParse({ provider: "futu" }).success, false);
});

for (const provider of ["futu", "schwab"]) {
  test(`${provider} source can load a terminal without requiring another source`, (t) => {
    const html = render(t, { provider });
    assert.match(html, /iframe[^>]+title="投资终端"/);
    assert.doesNotMatch(html, /暂无可用行情源|未连接 Schwab|Schwab 授权已失效/);
  });
}

test("expired Schwab authorization keeps Futu viewing but warns that trading is suspended", (t) => {
  const html = render(t, { provider: "futu", reauth: true });
  assert.match(html, /iframe[^>]+title="投资终端"/);
  assert.match(html, /已切换富途看盘，账户与交易已暂停/);
  assert.match(html, /重新授权/);
});

for (const role of ["admin", "user"]) {
  test(`${role} without any source gets only a permitted setup action`, (t) => {
    const html = render(t, { provider: "none", role });
    assert.doesNotMatch(html, /<iframe/);
    assert.match(html, /暂无可用行情源/);
    if (role === "admin") assert.match(html, /href="\/admin\/settings/);
    else {
      assert.match(html, /联系管理员启用富途行情/);
      assert.doesNotMatch(html, /href="\/admin\/settings/);
    }
  });
}

test("expired Schwab without Futu still requires reauthorization", (t) => {
  const html = render(t, { provider: "none", reauth: true });
  assert.doesNotMatch(html, /<iframe/);
  assert.match(html, /Schwab 授权已失效/);
});

const widget = { id: "investment.quotes", module: "investment", title: "行情", dataRoute: "/api/modules/investment/overview/quotes" };
const quotes = { provider: "futu", status: "ok", session: "regular", items: [
  { symbol: "AAPL", description: "Apple", price: 200, changePct: 2, session: "regular", held: false, watched: true },
] };
test("overview quotes validate and display the actual Futu source", (t) => {
  assert.equal(overviewQuotesSchema.safeParse(quotes).success, true);
  assert.equal(overviewQuotesSchema.safeParse({ ...quotes, provider: undefined }).success, false);
  const html = render(t, { component: createElement(QuotesWidget, { widget }),
    seed: (client) => client.setQueryData(["investment", "widget", widget.id], quotes) });
  assert.match(html, /富途/);
  assert.doesNotMatch(html, /Schwab/);
  assert.match(html, /200\.00/);
});

test("an upstream Futu failure is not hidden as an empty watchlist", (t) => {
  const html = render(t, { component: createElement(QuotesWidget, { widget }),
    seed: (client) => client.setQueryData(["investment", "widget", widget.id], {
      ...quotes, status: "error", message: "行情权限不足", items: [],
    }) });
  assert.match(html, /行情暂不可用/);
  assert.match(html, /行情权限不足/);
});

test("Futu viewing does not unlock personal brokerage positions", (t) => {
  const html = render(t, { component: createElement(PositionsPage),
    seed: (client) => client.setQueryData(["investment", "positions"], { status: "unconfigured", accounts: [], items: [] }) });
  assert.match(html, /未连接券商/);
  assert.doesNotMatch(html, /<iframe/);
});
