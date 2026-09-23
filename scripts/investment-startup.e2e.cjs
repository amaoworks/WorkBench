// Measure the real React investment page and Go TradingView proxy. Only broker,
// watchlist, and market-data APIs are fixtures; the terminal and chart library
// are served unchanged by Workbench. This script never submits an order.
const assert = require("node:assert/strict");
const { mkdtemp, rm, writeFile } = require("node:fs/promises");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { join } = require("node:path");
const { tmpdir } = require("node:os");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const warmReopens = 3;
const startupTimeout = Number(process.env.WORKBENCH_STARTUP_TIMEOUT_MS) || 90000;
const futuDelayMs = Math.max(0, Math.min(30000, Number(process.env.WORKBENCH_TEST_FUTU_DELAY_MS) || 0));
const fixtureErrors = [];
const browserErrors = [];
const requestFailures = [];
const chartResponses = [];
const historyResults = [];
const futuSettingsRequests = [];
let activeVisit = "unassigned";

function deferred() {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
}

async function freePort() {
  const server = createServer();
  await new Promise((resolve, reject) => server.once("error", reject).listen(0, "127.0.0.1", resolve));
  const { port } = server.address();
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
  return port;
}

function safeWorkbenchEnv() {
  const env = { ...process.env };
  for (const key of Object.keys(env)) {
    if (/^(OPENAI_|WORKBENCH_|SCHWAB_|FUTU_)/.test(key)) delete env[key];
  }
  // Do not let this disposable local run inherit service credentials or a
  // configured external Futu endpoint from the invoking shell.
  env.OPENAI_API_KEY = "";
  env.OPENAI_BASE_URL = "";
  env.WORKBENCH_ALLOWED_HOSTS = "";
  env.WORKBENCH_FUTU_OPEND_ADDRESS = "127.0.0.1:11111";
  env.WORKBENCH_FUTU_ALLOW_NON_LOCAL = "false";
  return env;
}

function dailyCandles(url) {
  const start = Number(url.searchParams.get("startDate"));
  const end = Number(url.searchParams.get("endDate"));
  const firstDay = Number.isFinite(start) ? Math.floor(start / 86400000) * 86400000 : Date.now() - 365 * 86400000;
  const lastDay = Number.isFinite(end) ? end : Date.now();
  const candles = [];
  for (let time = firstDay, index = 0; time < lastDay && candles.length < 2000; time += 86400000, index++) {
    const day = new Date(time).getUTCDay();
    if (day === 0 || day === 6) continue;
    const close = 100 + (index % 19) / 10;
    candles.push({ datetime: time, open: close - 0.4, high: close + 1, low: close - 1, close, volume: 10000 + index });
  }
  return candles;
}

async function fixtureInvestmentAPI(route) {
  const request = route.request();
  const url = new URL(request.url());
  const path = url.pathname;
  const method = request.method();
  const visit = activeVisit;
  try {
    if (path === "/api/modules/investment/schwab" && method === "GET") {
      return await route.fulfill({ json: { appKey: "fixture", callbackUrl: "https://example.test/oauth/schwab", hasAppSecret: true, connected: true, reauthorizationRequired: false, lastError: "" } });
    }
    if (path === "/api/modules/investment/watchlists" && method === "GET") {
      return await route.fulfill({ json: { revision: 0, state: { lists: [{ id: "startup-test", title: "自选表", symbols: [] }], activeId: "startup-test" } } });
    }
    if (path === "/api/modules/investment/watchlists" && method === "PUT") {
      assert.equal(request.headers()["x-csrf-token"], "startup-test-csrf");
      const body = request.postDataJSON();
      return await route.fulfill({ json: { revision: 1, sequence: body.sequence } });
    }
    if (path === "/api/modules/investment/futu" && method === "GET") {
      futuSettingsRequests.push({ visit, delayMs: futuDelayMs });
      // This configuration endpoint remains real in the baseline: a fresh
      // temporary database keeps Futu disabled, so it cannot contact OpenD.
      // An optional delay models a slow Futu health check without changing the
      // endpoint response or delaying /overnight.
      if (futuDelayMs) await new Promise(resolve => setTimeout(resolve, futuDelayMs));
      return await route.continue();
    }
    if (path === "/api/modules/investment/overnight" && method === "GET") {
      return await route.continue();
    }
    if (path === "/api/modules/investment/schwab/trader/v1/accounts/accountNumbers" && method === "GET") {
      return await route.fulfill({ json: [{ accountNumber: "111", hashValue: "STARTUP" }] });
    }
    if (/\/schwab\/trader\/v1\/accounts\/STARTUP$/.test(path) && method === "GET") {
      return await route.fulfill({ json: { securitiesAccount: { positions: [] } } });
    }
    if (/\/schwab\/trader\/v1\/accounts\/STARTUP\/orders$/.test(path) && method === "GET") {
      return await route.fulfill({ json: [] });
    }
    if (/\/schwab\/trader\/v1\/accounts\/STARTUP\/transactions$/.test(path) && method === "GET") {
      return await route.fulfill({ json: [] });
    }
    if (path === "/api/modules/investment/schwab/marketdata/v1/pricehistory" && method === "GET") {
      const candles = dailyCandles(url);
      historyResults.push({ visit, symbol: url.searchParams.get("symbol"), candles: candles.length, path: url.pathname });
      return await route.fulfill({ json: { candles } });
    }
    if (path === "/api/modules/investment/schwab/marketdata/ws/command" && method === "POST") {
      const command = request.postDataJSON();
      assert.equal(command.command, "ADD");
      const keys = String(command.keys || "").split(",").filter(Boolean);
      sockets.at(-1)?.send(JSON.stringify({ data: [{ service: command.service, timestamp: Date.now(), content: keys.map(key => ({ key, "1": 101, "2": 103, "3": 102, "8": 10000, "10": 105, "11": 98, "12": 100, "17": 100, "29": 102, "31": 2, "43": 2 })) }] }));
      return await route.fulfill({ json: { success: true } });
    }
    if (path.startsWith("/api/modules/investment/") && method === "GET" && path.includes("/instruments")) {
      return await route.fulfill({ json: { instruments: [{ symbol: "AAPL", description: "Apple", assetType: "EQUITY", exchange: "NASDAQ" }] } });
    }
    if (path.startsWith("/api/modules/investment/futu/") && method === "GET") {
      return await route.fulfill({ json: { candles: [], quote: null } });
    }
    assert.fail(`unexpected investment API request: ${method} ${path}`);
  } catch (error) {
    fixtureErrors.push(`${method} ${path}: ${error.message}`);
    await route.fulfill({ status: 500, json: { error: "startup test fixture failed" } }).catch(() => {});
  }
}

const sockets = [];

async function waitForWorkbench(origin, app) {
  let spawnError;
  app.once("error", error => { spawnError = error; });
  for (let attempt = 0; attempt < 120; attempt++) {
    if (spawnError) throw spawnError;
    if (app.exitCode !== null) throw new Error(`Temporary Workbench exited with ${app.exitCode}`);
    try {
      const response = await fetch(`${origin}/health/ready`);
      if (response.ok) return;
    } catch { /* server is still starting */ }
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error("Temporary Workbench did not become ready within 12 seconds");
}

async function waitForHistory(visit, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = historyResults.find(item => item.visit === visit && item.candles > 0);
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`${visit}: no non-empty Schwab pricehistory fixture response`);
}

async function visitResourceTimings(page, visit) {
  const all = [];
  for (const frame of page.frames()) {
    try {
      all.push(...await frame.evaluate(() => (window.__startupResourceTimings || []).filter(item => item.visit === window.__startupVisitId)));
    } catch { /* detached or cross-origin frame */ }
  }
  // The shared top-level collector survives terminal iframe navigation and also
  // retains Resource Timing entries from the deliberately abandoned load.
  const shared = await page.evaluate(id => (window.__startupResourceTimings || []).filter(item => item.visit === id), visit).catch(() => []);
  return [...new Map([...all, ...shared].map(item => [`${item.frame}|${item.name}|${item.startTime}`, item])).values()]
    .sort((a, b) => a.startTime - b.startTime);
}

async function run() {
  const directory = await mkdtemp(join(tmpdir(), "workbench-investment-startup-"));
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const binary = process.env.WORKBENCH_BIN || "/tmp/workbench-investment-startup-test";
  let app, browser;
  try {
    app = spawn(binary, ["-data", join(directory, "data.db"), "-auth", "local", "-listen", `127.0.0.1:${port}`], {
      env: safeWorkbenchEnv(), stdio: "ignore",
    });
    await waitForWorkbench(origin, app);
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    await page.addInitScript(() => {
      if (window === window.top) {
        window.__startupResourceTimings = [];
        window.__startupVisitId = null;
      }
      let visit;
      try { visit = window.top.__startupVisitId; } catch { visit = null; }
      const collect = entry => {
        const name = String(entry.name || "");
        if (!name.includes("/charting_library/")) return;
        const item = {
          visit, frame: location.pathname, name: new URL(name, location.href).pathname + new URL(name, location.href).search,
          startTime: Number(entry.startTime.toFixed(1)), duration: Number(entry.duration.toFixed(1)),
          responseEnd: Number(entry.responseEnd.toFixed(1)), transferSize: entry.transferSize,
          encodedBodySize: entry.encodedBodySize, initiatorType: entry.initiatorType,
        };
        try { window.top.__startupResourceTimings.push(item); } catch { /* cross-origin */ }
        if (window !== window.top) window.parent.postMessage({ type: "investment-startup-resource", item }, location.origin);
      };
      if (window === window.top) {
        window.addEventListener("message", event => {
          if (event.origin === location.origin && event.data?.type === "investment-startup-resource") window.__startupResourceTimings.push(event.data.item);
        });
      }
      try { new PerformanceObserver(list => list.getEntries().forEach(collect)).observe({ type: "resource", buffered: true }); } catch { /* older Chromium */ }
    });
    page.on("pageerror", error => browserErrors.push({ type: "pageerror", message: error.message }));
    page.on("console", message => {
      if (message.type() === "error") browserErrors.push({ type: "console", message: message.text() });
    });
    page.on("requestfailed", request => requestFailures.push({ url: request.url(), error: request.failure()?.errorText || "unknown" }));
    page.on("response", response => {
      if (new URL(response.url()).pathname.startsWith("/charting_library/")) {
        chartResponses.push({ status: response.status(), path: new URL(response.url()).pathname });
      }
    });
    await page.route("**/api/auth/csrf", route => route.fulfill({ json: { token: "startup-test-csrf" } }));
    await page.route("**/api/modules/investment/**", fixtureInvestmentAPI);
    await page.routeWebSocket("**/trader/ws", socket => {
      sockets.push(socket);
      socket.send(JSON.stringify({ stream: { status: "ready" } }));
    });

    await page.goto(origin, { waitUntil: "domcontentloaded" });
    await page.getByRole("heading", { name: "总览", exact: true }).waitFor({ timeout: startupTimeout });

    const results = [];
    const fullResourceTimings = [];
    async function openInvestment(visit, label) {
      activeVisit = visit;
      await page.evaluate(id => { window.__startupVisitId = id; }, visit);
      const started = process.hrtime.bigint();
      await page.getByRole("link", { name: "投资", exact: true }).click();
      const terminal = page.frameLocator("iframe[title='TradingView 投资终端']");
      await terminal.locator("#terminal-loading").waitFor({ state: "hidden", timeout: startupTimeout });
      await terminal.locator("#tv_chart_container iframe").first().waitFor({ state: "attached", timeout: startupTimeout });
      const chart = terminal.frameLocator("#tv_chart_container iframe").first();
      await chart.locator("canvas").first().waitFor({ state: "visible", timeout: startupTimeout });
      const history = await waitForHistory(visit, startupTimeout);
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const elapsedMs = Number((Number(process.hrtime.bigint() - started) / 1e6).toFixed(1));
      const resources = await visitResourceTimings(page, visit);
      fullResourceTimings.push({ visit, resources });
      assert(resources.some(item => item.name.endsWith("/charting_library.esm.js")), `${visit}: real TradingView loader was not requested through Workbench`);
      assert(resources.some(item => item.name.endsWith("/sameorigin.html")), `${visit}: TradingView same-origin chart frame was not requested through Workbench`);
      assert(history.candles > 0, `${visit}: no actual historical K-line fixture data completed`);
      const quietUntil = Date.now() + 250;
      let lastCount = historyResults.filter(item => item.visit === visit).length;
      let lastChange = Date.now();
      while (Date.now() < quietUntil && Date.now() - lastChange < 150) {
        await new Promise(resolve => setTimeout(resolve, 25));
        const count = historyResults.filter(item => item.visit === visit).length;
        if (count !== lastCount) { lastCount = count; lastChange = Date.now(); }
      }
      const historyRequests = historyResults.filter(item => item.visit === visit);
      const slowResources = [...resources].sort((a, b) => b.duration - a.duration).slice(0, 10);
      const result = {
        visit, label, elapsedMs, historyCandles: history.candles,
        priceHistoryRequests: historyRequests.length,
        chartResourceCount: resources.length,
        loader: resources.find(item => item.name.endsWith("/charting_library.esm.js")) || null,
        sameOrigin: resources.find(item => item.name.endsWith("/sameorigin.html")) || null,
        slowestChartResources: slowResources,
      };
      results.push(result);
      console.log(`TIMING ${JSON.stringify(result)}`);
      await page.getByRole("heading", { name: "投资", exact: true }).waitFor();
      return result;
    }

    async function goHome() {
      await page.getByRole("link", { name: "总览", exact: true }).click();
      await page.getByRole("heading", { name: "总览", exact: true }).waitFor({ timeout: startupTimeout });
      await page.locator("iframe[title='TradingView 投资终端']").waitFor({ state: "detached", timeout: 10000 });
    }

    await openInvestment("cold-first", "冷首次打开");
    for (let index = 1; index <= warmReopens; index++) {
      await goHome();
      await openInvestment(`warm-${index}`, `暖重开 ${index}`);
    }

    // Leave during a fresh terminal navigation, then reopen from the home page.
    // This exercises iframe teardown while its real library requests are active.
    await goHome();
    activeVisit = "quick-abandon";
    await page.evaluate(() => { window.__startupVisitId = "quick-abandon"; });
    const terminalRequest = page.waitForRequest(request => {
      try { return new URL(request.url()).pathname === "/investment/terminal"; } catch { return false; }
    }, { timeout: startupTimeout });
    await page.getByRole("link", { name: "投资", exact: true }).click();
    await terminalRequest;
    await page.getByRole("link", { name: "总览", exact: true }).click();
    await page.getByRole("heading", { name: "总览", exact: true }).waitFor({ timeout: startupTimeout });
    await page.locator("iframe[title='TradingView 投资终端']").waitFor({ state: "detached", timeout: 10000 });
    fullResourceTimings.push({ visit: "quick-abandon", resources: await visitResourceTimings(page, "quick-abandon") });
    await openInvestment("quick-reopen", "快速离开后重开");

    const failedChartResponses = chartResponses.filter(response => response.status >= 400);
    assert(chartResponses.some(response => response.path.endsWith("/charting_library.esm.js")), "the real Go chart-library proxy was not used");
    assert(chartResponses.some(response => response.path.endsWith("/sameorigin.html")), "the real same-origin TradingView frame was not used");
    assert.deepEqual(failedChartResponses, [], "chart-library proxy returned HTTP errors");
    assert.deepEqual(fixtureErrors, [], "business-data fixture rejected an unexpected request");
    assert.deepEqual(browserErrors, [], "browser reported page or console errors");
    const reportPath = process.env.WORKBENCH_STARTUP_REPORT || join(tmpdir(), `workbench-investment-startup-${Date.now()}.json`);
    const responseSummary = chartResponses.reduce((summary, response) => {
      const key = String(response.status);
      summary.statuses[key] = (summary.statuses[key] || 0) + 1;
      if (response.path.endsWith("/charting_library.esm.js")) summary.loader++;
      if (response.path.endsWith("/sameorigin.html")) summary.sameOrigin++;
      if (response.path.includes("/bundles/")) summary.bundles++;
      return summary;
    }, { statuses: {}, loader: 0, sameOrigin: 0, bundles: 0 });
    await writeFile(reportPath, JSON.stringify({
      binary, origin, futuDelayMs, results,
      chartResourceTimings: fullResourceTimings,
      historyRequests: historyResults,
      futuSettingsRequests,
      chartResponses,
      requestFailures,
      browserErrors,
      fixtureErrors,
    }, null, 2));
    console.log(`PASS: real Workbench + real React investment page + real Go /charting_library/ proxy; ${results.length} ready visits, ${warmReopens} warm home-to-investment reopens, quick-leave recovery; no order requests.`);
    console.log(`BROWSER_ERRORS ${JSON.stringify(browserErrors)}`);
    console.log(`NAVIGATION_REQUEST_FAILURES ${JSON.stringify({ total: requestFailures.length, aborted: requestFailures.filter(item => item.error.includes("ABORTED")).length })}`);
    console.log(`FUTU_SETTINGS_REQUESTS ${JSON.stringify(futuSettingsRequests)}`);
    console.log(`CHART_PROXY_RESPONSES ${JSON.stringify(responseSummary)}`);
    console.log(`RESOURCE_TIMING_REPORT ${reportPath}`);
  } finally {
    await browser?.close();
    if (app && app.exitCode === null && app.pid) {
      const stopped = deferred();
      app.once("exit", stopped.resolve);
      app.kill("SIGTERM");
      await Promise.race([stopped.promise, new Promise(resolve => setTimeout(resolve, 5000))]);
    }
    await rm(directory, { recursive: true, force: true });
  }
}

run().catch(error => { console.error(error); process.exitCode = 1; });
