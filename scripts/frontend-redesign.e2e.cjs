// Disposable workspace. Broker portfolio data and notification list states are browser fixtures;
// no production data, external module, feed, notification channel, or brokerage service is used.
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

async function unusedPort() {
  const listener = createServer();
  await new Promise((resolve, reject) => {
    listener.once("error", reject);
    listener.listen(0, "127.0.0.1", resolve);
  });
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}

async function api(page, path, method, body) {
  return page.evaluate(async ({ path, method, body }) => {
    const { token } = await (await fetch("/api/auth/csrf")).json();
    const response = await fetch(path, {
      method,
      headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`${method} ${path}: ${response.status} ${await response.text()}`);
    return response.status === 204 ? null : response.json();
  }, { path, method, body });
}

async function expectNoOverflow(page, label) {
  const dimensions = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }));
  assert(dimensions.scrollWidth <= dimensions.width, `${label} horizontally overflows: ${dimensions.scrollWidth} > ${dimensions.width}`);
}

async function expectURL(page, pathname, params = {}) {
  await page.waitForFunction(({ pathname, params }) => {
    const url = new URL(location.href);
    return url.pathname === pathname && Object.entries(params).every(([key, value]) => url.searchParams.get(key) === value);
  }, { pathname, params });
}

async function installFixtures(page, state) {
  await page.route("**/investment/terminal**", (route) => route.fulfill({
    contentType: "text/html; charset=utf-8",
    body: "<!doctype html><title>模拟投资终端</title><p>模拟投资终端</p>",
  }));
  await page.route(/\/api\/notifications(?:\?.*)?$/, (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/notifications") return route.continue();
    const filteredEmpty = url.searchParams.get("source") === "todo" && url.searchParams.get("unread") === "true";
    return route.fulfill({
      json: filteredEmpty
        ? { items: [] }
        : {
            items: [{
              id: "fixture-notification", sourceModule: "news", severity: "info", title: "改版回归通知",
              content: "通知列表浏览器夹具", createdAt: new Date().toISOString(),
            }],
          },
    });
  });
  await page.route("**/api/modules/investment/positions", (route) => {
    const status = state.portfolioStatus;
    return route.fulfill({
      json: {
        status,
        message: status === "ready" ? undefined : `持仓夹具状态：${status}`,
        updatedAt: status === "ready" ? new Date().toISOString() : undefined,
        accounts: status === "ready"
          ? [{ accountHash: "account-fixture", label: "账户 ••1234", netWorth: 125000, cash: 25000, invested: 100000, dayPnl: 320 }]
          : [],
        items: status === "ready"
          ? [{
              accountHash: "account-fixture", accountLabel: "账户 ••1234", symbol: "AAPL", description: "Apple Inc.",
              assetType: "EQUITY", quantity: 20, averagePrice: 180, lastPrice: 190, marketValue: 3800,
              dayPnl: 40, dayPnlPct: 1.06, unrealizedPnl: 200, unrealizedPnlPct: 5.56, weight: 3.8,
            }]
          : [],
      },
    });
  });
  await page.route("**/api/modules/investment/orders?*", (route) => {
    const url = new URL(route.request().url());
    assert(["1", "7", "30", "90"].includes(url.searchParams.get("days")), "orders request must carry a supported days value");
    assert(["all", "working", "filled", "canceled"].includes(url.searchParams.get("status")), "orders request must carry status");
    assert(url.searchParams.get("tz"), "orders request must carry the browser timezone");
    const status = state.portfolioStatus;
    return route.fulfill({
      json: {
        status,
        message: status === "ready" ? undefined : `订单夹具状态：${status}`,
        updatedAt: status === "ready" ? new Date().toISOString() : undefined,
        items: status === "ready"
          ? [{
              id: "order-fixture", accountHash: "account-fixture", accountLabel: "账户 ••1234", orderType: "LIMIT",
              duration: "DAY", legs: [{ symbol: "MSFT", instruction: "BUY", quantity: 5, filledQuantity: 5, averageFill: 409.5 }], quantity: 5,
              filledQuantity: 5, price: 410, stopPrice: null, averageFill: 409.5, status: "FILLED", group: "filled",
              enteredAt: new Date().toISOString(), closedAt: new Date().toISOString(),
            }, {
              id: "stop-limit", accountHash: "account-fixture", accountLabel: "账户 ••1234", orderType: "STOP_LIMIT",
              duration: "DAY", legs: [{ symbol: "NVDA", instruction: "SELL", quantity: 1, filledQuantity: null, averageFill: null }],
              quantity: 1, filledQuantity: 0, price: 100, stopPrice: 95, averageFill: null, status: "WORKING", group: "working",
              enteredAt: new Date().toISOString(), closedAt: "",
            }, {
              id: "partial-spread", accountHash: "account-fixture", accountLabel: "账户 ••1234", orderType: "NET_DEBIT",
              duration: "DAY", legs: [
                { symbol: "SPREAD C1", instruction: "BUY_TO_OPEN", quantity: 2, filledQuantity: 1, averageFill: 5 },
                { symbol: "SPREAD C2", instruction: "SELL_TO_OPEN", quantity: 4, filledQuantity: 1, averageFill: 1.5 },
              ],
              quantity: 2, filledQuantity: null, price: 2, stopPrice: null, averageFill: null, status: "WORKING", group: "working",
              enteredAt: new Date().toISOString(), closedAt: "",
            }]
          : [],
      },
    });
  });
  const snapshot = {
    status: "ok", netWorth: 125000, dayPnl: 320, dayPnlPct: 0.26, cash: 25000, buyingPower: 50000, invested: 100000,
    accounts: 2, positions: 1, movers: [], symbols: ["AAPL"], updatedAt: new Date().toISOString(),
  };
  await page.route("**/api/modules/investment/overview/snapshot", (route) => route.fulfill({ json: snapshot }));
  await page.route("**/api/dashboard?include=data", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    for (const widget of body.widgets) {
      if (widget.widgetKind === "investment.snapshot") widget.data = snapshot;
    }
    return route.fulfill({ json: body });
  });
  await page.route("**/api/modules/investment/networth?*", (route) => {
    const url = new URL(route.request().url());
    const range = url.searchParams.get("range");
    const account = url.searchParams.get("account");
    assert(["1m", "3m", "1y", "all"].includes(range));
    state.networthQuery = { range, account };
    const data = [
      ["2026-10-01", { a: 100 }], ["2026-10-02", { a: 110 }], ["2026-10-05", {}],
      ["2026-10-06", { a: 120, b: 80 }], ["2026-10-07", { a: 130, b: 80 }],
    ];
    const points = data.map(([day, accounts]) => {
      const entries = Object.entries(accounts).filter(([hash]) => !account || hash === account);
      const value = entries.length ? entries.reduce((sum, [, amount]) => sum + amount, 0) : null;
      return { day, accountHashes: entries.map(([hash]) => hash), netWorth: value, cash: value, invested: value, dayPnl: value };
    });
    return route.fulfill({ json: { status: "ready", currency: "USD", range, accounts: ["a", "b"], points, updatedAt: new Date().toISOString() } });
  });
}

async function openModuleMenu(page, label) {
  const trigger = page.getByRole("link", { name: label, exact: true });
  await trigger.hover();
  const menu = page.getByRole("menu", { name: label, exact: true });
  await menu.waitFor();
  return { trigger, menu };
}

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-frontend-redesign-"));
  const port = await unusedPort();
  const baseURL = `http://127.0.0.1:${port}`;
  const app = spawn(process.env.WORKBENCH_BIN || join(__dirname, "../workbench"), [
    "-data", join(directory, "data.db"), "-auth", "local", "-listen", `127.0.0.1:${port}`,
  ], {
    env: {
      ...process.env,
      OPENAI_API_KEY: "",
      WORKBENCH_PUBLIC_URL: "",
      WORKBENCH_ALLOWED_HOSTS: "",
      WORKBENCH_FUTU_CONFIG_DIR: "",
      WORKBENCH_FUTU_RUNTIME_DIR: join(directory, "futu-runtime"),
    },
    stdio: "ignore",
  });
  let browser;
  let spawnError;
  app.on("error", (error) => { spawnError = error; });
  try {
    for (let attempt = 0; ; attempt++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
      if (await fetch(`${baseURL}/health/ready`).then((response) => response.ok).catch(() => false)) break;
      if (attempt > 100) throw new Error("Temporary Workbench did not become ready");
      await new Promise((resolve) => setTimeout(resolve, 100));
    }

    browser = await chromium.launch({
      headless: true,
      executablePath: process.env.CHROMIUM_PATH || "/usr/bin/chromium",
      args: ["--no-sandbox"],
    });
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    const state = { portfolioStatus: "ready" };
    await installFixtures(page, state);
    const errors = [];
    page.on("pageerror", (error) => errors.push(`pageerror: ${error.message}`));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(`console: ${message.text()}`);
    });

    await page.goto(baseURL);
    await page.getByRole("link", { name: "Workbench 总览", exact: true }).waitFor();
    const history = page.getByRole("region", { name: "净值历史", exact: true });
    await history.getByRole("img").waitFor();
    assert.equal(await history.locator("svg[role=img] path").count(), 2, "missing days and account changes must break the curve");
    assert.equal(await history.locator("svg[role=img] circle").count(), 4);
    await Promise.all([
      page.waitForResponse((response) => response.url().includes("/investment/networth?") && new URL(response.url()).searchParams.get("range") === "1y"),
      history.getByRole("button", { name: "1 年", exact: true }).click(),
    ]);
    assert.equal(state.networthQuery.range, "1y");
    await history.getByLabel("净值账户", { exact: true }).click();
    await Promise.all([
      page.waitForResponse((response) => response.url().includes("/investment/networth?") && new URL(response.url()).searchParams.get("account") === "b"),
      page.getByRole("option", { name: "账户 b", exact: true }).click(),
    ]);
    await history.getByText("每日明细", { exact: true }).click();
    await history.getByRole("columnheader", { name: "净值 (USD)", exact: true }).waitFor();
    const aiButton = page.getByRole("button", { name: "打开 AI", exact: true });
    assert.equal(await aiButton.getAttribute("aria-keyshortcuts"), "Control+J");
    await page.keyboard.press("Control+j");
    const aiPanel = page.getByRole("dialog", { name: /问 AI/ });
    await aiPanel.waitFor();
    await page.keyboard.press("Control+j");
    await aiPanel.waitFor({ state: "detached" });

    // Desktop pointer and keyboard navigation.
    let nav = await openModuleMenu(page, "待办");
    await nav.menu.getByRole("menuitem", { name: /今天/ }).waitFor();
    await page.mouse.move(0, 400);
    await nav.menu.waitFor({ state: "detached" });
    await nav.trigger.focus();
    await nav.trigger.press("ArrowDown");
    nav = { trigger: nav.trigger, menu: page.getByRole("menu", { name: "待办", exact: true }) };
    await nav.menu.waitFor();
    assert.equal(await nav.menu.getByRole("menuitem").first().evaluate((node) => node === document.activeElement), true);
    await nav.menu.getByRole("menuitem").first().press("End");
    assert.equal(await nav.menu.getByRole("menuitem").last().evaluate((node) => node === document.activeElement), true);
    await nav.menu.getByRole("menuitem").last().press("Home");
    assert.equal(await nav.menu.getByRole("menuitem").first().evaluate((node) => node === document.activeElement), true);
    await nav.menu.getByRole("menuitem").first().press("Escape");
    await nav.menu.waitFor({ state: "detached" });
    assert.equal(await nav.trigger.evaluate((node) => node === document.activeElement), true, "Escape must restore top-nav focus");

    // Settings is already mounted before module settings is entered repeatedly through a business menu.
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("heading", { name: "业务", exact: true }).waitFor();
    for (let attempt = 0; attempt < 2; attempt++) {
      const { menu } = await openModuleMenu(page, "待办");
      await menu.getByRole("menuitem", { name: /Wallos 续费/ }).click();
      const dialog = page.getByRole("dialog", { name: "待办设置", exact: true });
      await dialog.getByLabel("Wallos 地址", { exact: true }).waitFor();
      await expectURL(page, "/settings", { tab: "modules", module: "todo" });
      if (attempt === 0) {
        await dialog.getByRole("button", { name: "关闭业务设置", exact: true }).click();
        await dialog.waitFor({ state: "detached" });
        await page.waitForFunction(() => !new URL(location.href).searchParams.has("module"));
        continue;
      }
      const address = dialog.getByLabel("Wallos 地址", { exact: true });
      await address.fill("https://discard.example.com");
      await dialog.getByRole("button", { name: "关闭业务设置", exact: true }).click();
      const discard = page.getByRole("alertdialog", { name: "放弃未保存的修改？", exact: true });
      await discard.getByRole("button", { name: "继续编辑", exact: true }).click();
      assert.equal(await address.inputValue(), "https://discard.example.com");
      await dialog.getByRole("button", { name: "关闭业务设置", exact: true }).click();
      await discard.getByRole("button", { name: "放弃修改", exact: true }).click();
      await dialog.waitFor({ state: "detached" });
      await page.waitForFunction(() => !new URL(location.href).searchParams.has("module"));
    }

    // Source and unread filters are server-side; a selected zero-result source remains clearable.
    await page.goto(`${baseURL}/notifications?filter=unread&source=todo`);
    await page.getByText("此来源没有未读通知", { exact: true }).waitFor();
    assert.equal(await page.getByRole("button", { name: "待办", exact: true }).getAttribute("aria-pressed"), "true");
    await page.getByRole("button", { name: "清除来源", exact: true }).click();
    await page.waitForFunction(() => !new URL(location.href).searchParams.has("source"));
    await page.getByText("改版回归通知", { exact: true }).waitFor();
    assert.equal(new URL(page.url()).searchParams.get("filter"), "unread", "clearing source must retain the unread filter");

    // Priority/category fields create real tasks and remain queryable after a view change.
    await page.goto(`${baseURL}/todo`);
    await page.getByLabel("待办标题", { exact: true }).fill("改版高优先级待办");
    await page.getByLabel("优先级", { exact: true }).click();
    await page.getByRole("option", { name: "高优先级", exact: true }).click();
    await page.getByLabel("分类", { exact: true }).fill("work");
    await page.getByRole("button", { name: "添加", exact: true }).click();
    await page.getByText("改版高优先级待办", { exact: true }).waitFor();
    await page.getByLabel("按分类筛选", { exact: true }).fill("work");
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await expectURL(page, "/todo", { category: "work" });
    await page.getByText("改版高优先级待办", { exact: true }).waitFor();
    await page.getByRole("checkbox", { name: "标记为完成：改版高优先级待办", exact: true }).click();
    await page.getByRole("navigation", { name: "待办视图", exact: true }).getByRole("link", { name: /^已完成/ }).click();
    await expectURL(page, "/todo", { view: "done", category: "work" });
    await page.getByText("改版高优先级待办", { exact: true }).waitFor();

    // Portfolio subpages survive direct navigation and reload, and consume all required order query fields.
    for (const fixture of [
      { path: "/investment/positions", heading: "持仓", text: "AAPL" },
      { path: "/investment/orders", heading: "订单", text: "MSFT" },
    ]) {
      await page.goto(`${baseURL}${fixture.path}`);
      await page.getByRole("heading", { name: fixture.heading, exact: true }).waitFor();
      await page.getByText(fixture.text, { exact: true }).waitFor();
      await page.reload();
      await page.getByText(fixture.text, { exact: true }).waitFor();
      if (fixture.heading === "订单") {
        await page.getByText("触发 $95.00", { exact: true }).waitFor();
        await page.getByText("委托 2 · 成交 1 · $5.00", { exact: true }).waitFor();
        await page.getByText("委托 4 · 成交 1 · $1.50", { exact: true }).waitFor();
      }
    }
    for (const [status, expected] of [
      ["unconfigured", "尚未连接券商"],
      ["reauth", "Schwab 授权已失效"],
      ["error", "券商数据加载失败"],
    ]) {
      state.portfolioStatus = status;
      await page.goto(`${baseURL}/investment/positions`);
      await page.getByText(expected, { exact: true }).waitFor();
      if (status === "error") await page.getByText("持仓夹具状态：error", { exact: true }).waitFor();
      await page.goto(`${baseURL}/investment/orders`);
      await page.getByText(expected, { exact: true }).waitFor();
      if (status === "error") await page.getByText("订单夹具状态：error", { exact: true }).waitFor();
    }
    state.portfolioStatus = "ready";

    // A direct module subpage refresh reports module-list failure instead of a generic 404.
    const failurePage = await context.newPage();
    await failurePage.route("**/api/modules", (route) => route.fulfill({
      status: 503,
      contentType: "application/json",
      body: '{"code":"fixture","message":"模拟业务列表失败"}',
    }));
    await failurePage.goto(`${baseURL}/investment/positions`);
    await failurePage.getByText("业务列表加载失败", { exact: true }).waitFor({ timeout: 20_000 });
    await failurePage.getByText("模拟业务列表失败", { exact: true }).waitFor();
    await failurePage.close();

    // Touch uses first tap to open a desktop dropdown and second tap to navigate.
    const touchContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, hasTouch: true });
    const touchPage = await touchContext.newPage();
    await installFixtures(touchPage, state);
    await touchPage.goto(baseURL);
    const touchTodo = touchPage.getByRole("link", { name: "待办", exact: true });
    await touchTodo.tap();
    await touchPage.getByRole("menu", { name: "待办", exact: true }).waitFor();
    assert.equal(new URL(touchPage.url()).pathname, "/");
    await touchTodo.tap();
    await expectURL(touchPage, "/todo");
    await touchContext.close();

    const macContext = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36",
    });
    const macPage = await macContext.newPage();
    await installFixtures(macPage, state);
    await macPage.goto(baseURL);
    const macAI = macPage.getByRole("button", { name: "打开 AI", exact: true });
    assert.equal(await macAI.getAttribute("aria-keyshortcuts"), "Meta+J");
    await macAI.getByText("⌘J", { exact: true }).waitFor();
    await macPage.keyboard.press("Meta+j");
    await macPage.getByRole("dialog", { name: /问 AI/ }).waitFor();
    await macPage.keyboard.press("Meta+j");
    await macPage.getByRole("dialog", { name: /问 AI/ }).waitFor({ state: "detached" });
    await macContext.close();

    // Mobile drawer exposes top-level and nested destinations and closes after navigation.
    await page.setViewportSize({ width: 360, height: 800 });
    await page.goto(baseURL);
    await page.getByRole("button", { name: "打开导航", exact: true }).click();
    const drawer = page.getByRole("dialog", { name: "Workbench", exact: true });
    await drawer.getByRole("link", { name: "持仓", exact: true }).click();
    await expectURL(page, "/investment/positions");
    await drawer.waitFor({ state: "detached" });

    // Every light/dark and palette combination is checked at desktop and the 360px minimum width.
    for (const viewport of [{ width: 1440, height: 900 }, { width: 360, height: 800 }]) {
      await page.setViewportSize(viewport);
      for (const theme of ["light", "dark"]) {
        for (const palette of ["default", "claude"]) {
          await api(page, "/api/settings/appearance", "PUT", { theme, motion: "full", palette });
          const prefix = `${theme}/${palette}/${viewport.width}px`;
          for (const fixture of [
            { path: "/", ready: () => page.getByRole("region", { name: "净值历史", exact: true }).getByRole("img").waitFor() },
            { path: "/todo", ready: () => page.getByLabel("待办标题", { exact: true }).waitFor() },
            { path: "/news", ready: () => page.getByRole("heading", { name: "资讯", exact: true }).waitFor() },
            { path: "/settings?tab=appearance", ready: () => page.getByRole("group", { name: "风格", exact: true }).waitFor() },
            { path: "/notifications", ready: () => page.getByRole("heading", { name: "通知", exact: true }).waitFor() },
            { path: "/investment/positions", ready: () => page.getByText("AAPL", { exact: true }).waitFor() },
            { path: "/investment/orders", ready: () => page.getByText("MSFT", { exact: true }).waitFor() },
          ]) {
            await page.goto(`${baseURL}${fixture.path}`);
            await fixture.ready();
            await page.waitForFunction(
              ({ dark, palette }) => document.documentElement.classList.contains("dark") === dark && document.documentElement.dataset.palette === palette,
              { dark: theme === "dark", palette },
            );
            await expectNoOverflow(page, `${fixture.path} at ${prefix}`);
            if (fixture.path === "/" && palette === "default") {
              await page.screenshot({ path: `/tmp/workbench-redesign-${theme}-${viewport.width}.png`, fullPage: true, animations: "disabled" });
            }
          }
        }
      }
    }

    assert.deepEqual(errors, []);
    console.log("PASS: top navigation pointer/keyboard/touch, repeated module settings and dirty close, notification filters, portfolio subpages/states, module failure, and responsive theme/palette overflow matrix.");
  } finally {
    if (browser) await browser.close();
    if (app.pid && app.exitCode === null) {
      const stopped = new Promise((resolve) => app.once("exit", resolve));
      app.kill("SIGTERM");
      await stopped;
    }
    await rm(directory, { recursive: true, force: true });
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
