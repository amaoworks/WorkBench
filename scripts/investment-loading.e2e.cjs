// Exercise startup failures with the real terminal document and adapters. Only
// the external chart library and Schwab socket are fixtures; no trades are sent.
const assert = require("node:assert/strict");
const { readFile } = require("node:fs/promises");
const { join } = require("node:path");
const { chromium, webkit, devices } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const useWebKit = process.argv.includes("--webkit");
  const browser = useWebKit ? await webkit.launch({ headless: true }) : await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const context = await browser.newContext(devices["iPhone 13"]);
    const page = await context.newPage();
    await page.clock.install();
    let mode = "module-error";
    const unexpected = [];
    await page.routeWebSocket("**/trader/ws", socket => socket.send(JSON.stringify({ stream: { status: "ready" } })));
    await page.route("https://workbench.test/**", async route => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/auth/csrf") return route.fulfill({ json: { token: "fixture" } });
      if (path === "/api/modules/investment/preferences") {
        if (route.request().method() === "GET") return route.fulfill({ json: { entries: {} } });
        const input = route.request().postDataJSON();
        return route.fulfill({ json: { revision: input.sequence, sequence: input.sequence } });
      }
      if (path === "/api/modules/investment/watchlists") return route.fulfill({ json: { revision: 1, state: { lists: [{ id: "test", title: "自选表", symbols: [] }], activeId: "test" } } });
      if (path === "/charting_library/charting_library.esm.js") {
        if (mode === "module-error") return route.fulfill({ status: 503, contentType: "text/plain", body: "chart library unavailable" });
        // Control chart readiness independently of the already connected stream.
        return route.fulfill({ contentType: "text/javascript", body: `
          const ready = ${mode === "timeout" ? "new Promise(resolve => { window.resolveFixtureChart = resolve; })" : "Promise.resolve()"};
          export class widget {
            constructor() { ${mode === "exception" ? "throw new Error('fixture initialization failed');" : ""} }
            chartReady() { return ready; }
            activeChart() { return {
              symbol: () => 'AAPL', resolution: () => '1D', symbolExt: () => ({ subsession_id: 'regular' }),
              ...Object.fromEntries(['onIntervalChanged', 'onSymbolChanged'].map(name => [name, () => ({ subscribe() {}, unsubscribe() {} })])),
            }; }
            headerReady() { return ready; }
            createDropdown() { return Promise.resolve({ applyOptions() {}, remove() {} }); }
            showNoticeDialog() { return Promise.resolve(); }
            applyOverrides() {}
            subscribe() {}
            unsubscribe() {}
            save(callback) { callback({ charts: [{ panes: [] }] }); return Promise.resolve(); }
            changeTheme() { return Promise.resolve(); }
            resetCache() {}
            chartsCount() { return 0; }
            watchList() { return Promise.resolve({
              getAllLists: () => ({ test: { id: 'test', title: '自选表', symbols: [] } }), getActiveListId: () => 'test',
              createList() {}, renameList() {}, setActiveList() {}, deleteList() {},
              ...Object.fromEntries(['onListAdded', 'onListChanged', 'onListRemoved', 'onListRenamed', 'onActiveListChanged'].map(name => [name, () => ({ subscribe() {}, unsubscribe() {} })])),
            }); }
          }
        ` });
      }
      if (path === "/investment/terminal" || path.startsWith("/investment/terminal/")) {
        const name = path.slice("/investment/terminal/".length) || "index.html";
        assert(!name.includes("/"));
        return route.fulfill({ contentType: name.endsWith(".html") ? "text/html" : "text/javascript", body: await readFile(join(__dirname, "../internal/modules/investment/chart", name)) });
      }
      unexpected.push(path);
      return route.abort();
    });
    const panel = page.locator("#terminal-loading");
    const retry = page.getByRole("button", { name: "重新加载", exact: true });
    for (const failure of ["module-error", "exception"]) {
      mode = failure;
      await page.goto("https://workbench.test/investment/terminal");
      await page.getByRole("alert").filter({ hasText: "图表加载失败" }).waitFor();
      assert.equal(await retry.isVisible(), true);
      mode = "ready";
      await retry.click();
      await panel.waitFor({ state: "hidden" });
    }
    mode = "timeout";
    await page.goto("https://workbench.test/investment/terminal");
    await page.waitForFunction(() => window.ws?.ready && !!window.resolveFixtureChart);
    assert.equal(await page.locator("#stream-status").isHidden(), true);
    assert.equal(await panel.isVisible(), true, "a ready stream must not hide a still-loading chart");
    await page.evaluate(() => window.dispatchEvent(new ErrorEvent("error", { message: "ResizeObserver loop completed with undelivered notifications." })));
    assert.equal(await panel.getAttribute("role"), "status", "a deferred resize must not be reported as a failed chart");
    assert.equal(await retry.isVisible(), false);
    assert.match(await page.locator("#terminal-loading-step").textContent(), /初始化图表/, "the cover names the current startup step");
    // A slow start is not a failure: after ~20 s the cover offers a reload but keeps waiting.
    await page.clock.fastForward(21000);
    await page.locator("#terminal-loading-message").filter({ hasText: "还在加载" }).waitFor();
    assert.equal(await panel.getAttribute("role"), "status", "a slow start must not be reported as a failure");
    assert.equal(await retry.isVisible(), true);
    await page.evaluate(() => window.resolveFixtureChart());
    await panel.waitFor({ state: "hidden" });
    // Only ~90 s without any startup progress fails the chart; a late success still recovers.
    await page.goto("https://workbench.test/investment/terminal");
    await page.waitForFunction(() => !!window.resolveFixtureChart);
    await page.clock.fastForward(91000);
    await page.getByRole("alert").filter({ hasText: "图表加载失败" }).filter({ hasText: "没有进展" }).waitFor();
    assert.equal(await retry.isVisible(), true);
    await page.evaluate(() => window.resolveFixtureChart());
    await panel.waitFor({ state: "hidden" });
    assert.deepEqual(unexpected, []);
    console.log(`PASS (${useWebKit ? "WebKit" : "Chromium"}): module failure, initialization exception, retry recovery, slow-start hint after stream readiness, no-progress failure at 90 s and late chart recovery; no trades.`);
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
