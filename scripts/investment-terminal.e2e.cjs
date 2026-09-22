// Real TradingView library + repository terminal code. Every Schwab request is
// intercepted with fictional data; no workspace credentials or trades are used.
const assert = require("node:assert/strict");
const { readFile, writeFile, mkdir, mkdtemp, rm } = require("node:fs/promises");
const { spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const { join } = require("node:path");
const { tmpdir } = require("node:os");
const { chromium, webkit, devices } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const useWebKit = process.argv.includes("--webkit");
const useApp = process.argv.includes("--app");
const mobile = useApp || useWebKit || process.argv.includes("--mobile");
const origin = useApp ? "http://127.0.0.1:18140" : mobile ? "https://workbench.test" : "http://workbench.test";
const libraryOrigin = "https://trading-terminal.tradingview-widget.com";
const assets = join(__dirname, "../internal/modules/investment/chart");
const cache = join(tmpdir(), "workbench-tv-test-cache");
const order = {
  orderId: 42, orderType: "LIMIT", orderStrategyType: "SINGLE", session: "SEAMLESS", duration: "GOOD_TILL_CANCEL",
  quantity: 2, price: 101, status: "WORKING", enteredTime: new Date().toISOString(),
  orderLegCollection: [{ instruction: "BUY", quantity: 2, instrument: { symbol: "MSFT", assetType: "EQUITY" } }]
};
let liveOrder = order;
let watchlists = { revision: 0, state: null };
let watchlistWriter = '', watchlistBase = 0, watchlistSequence = 0;
let failWatchlistSave = false;
let heldWatchlistSave;
const history = [
  { ...order, orderId: 40, status: "FILLED", filledQuantity: 2 },
  { ...order, orderId: 41, status: "CANCELED" },
];

(async () => {
  await mkdir(cache, { recursive: true });
  let browser, app, directory;
  try {
    if (useApp) {
      directory = await mkdtemp(join(tmpdir(), "workbench-mobile-chart-"));
      app = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-investment-terminal-test", ["-data", join(directory, "data.db"), "-auth", "local", "-listen", "127.0.0.1:18140"], {
        env: { ...process.env, OPENAI_API_KEY: "", WORKBENCH_ALLOWED_HOSTS: "" }, stdio: "ignore",
      });
      let spawnError;
      app.on("error", error => { spawnError = error; });
      let ready = false;
      for (let i = 0; i < 100; i++) {
        if (spawnError) throw spawnError;
        if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
        try { if ((await fetch(`${origin}/health/ready`)).ok) { ready = true; break; } } catch { /* startup */ }
        await new Promise(resolve => setTimeout(resolve, 100));
      }
      assert(ready, "temporary Workbench did not become ready");
    }
    browser = useWebKit ? await webkit.launch({ headless: true }) : await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const context = await browser.newContext(mobile ? devices["iPhone 13"] : { viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const errors = [], calls = [], sockets = [];
    let resizeNotifications = 0;
    page.on("pageerror", error => {
      // WebKit can defer a ResizeObserver notification during layout changes.
      // The sizing and readiness assertions below still require a usable chart.
      if (["ResizeObserver loop completed with undelivered notifications.", "ResizeObserver loop limit exceeded"].includes(error.message)) { resizeNotifications++; return; }
      errors.push(error.message);
    });
    page.on("console", message => {
      if (message.type() !== "error") return;
      if (message.location().url?.endsWith('/api/modules/investment/watchlists') && /503|409/.test(message.text())) return;
      errors.push(message.text());
    });
    await page.routeWebSocket("**/trader/ws", socket => {
      sockets.push(socket);
      socket.send(JSON.stringify({ stream: { status: "ready" } }));
    });
    await page.route(`${origin}/**`, async route => {
      const url = new URL(route.request().url()), path = url.pathname;
      try {
        if (path === "/mobile-fixture") {
          return await route.fulfill({ contentType: "text/html", body: `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
            <body style="margin:0"><iframe name="investment-terminal" title="投资终端" src="/investment/terminal" style="position:fixed;inset:0;width:100%;height:100%;border:0"></iframe></body></html>` });
        }
        if (path.startsWith("/charting_library/")) {
          const file = join(cache, createHash("sha256").update(path).digest("hex") + ".json");
          let asset;
          // Always refresh the entry point. Its hashed bundles can be reused.
          if (/\.[a-f0-9]{12,}\./.test(path)) {
            try { asset = JSON.parse(await readFile(file, "utf8")); } catch { /* first visit */ }
          }
          if (!asset) {
            const response = await context.request.get(libraryOrigin + path);
            assert(response.ok(), `TradingView asset HTTP ${response.status()}: ${path}`);
            asset = { type: response.headers()["content-type"], body: (await response.body()).toString("base64") };
            await writeFile(file, JSON.stringify(asset));
          }
          return await route.fulfill({ contentType: asset.type, body: Buffer.from(asset.body, "base64") });
        }
        if (path === "/investment/terminal" || path.startsWith("/investment/terminal/")) {
          const file = path.slice("/investment/terminal/".length) || "index.html";
          assert(!file.includes("/"), "unexpected terminal asset path");
          let body = await readFile(join(assets, file), "utf8");
          if (file === "index.html") {
            if (mobile) {
              // Install after Playwright's WebSocket routing shim, immediately
              // before the application opens a socket. An init script can be
              // overwritten by that shim and silently miss this regression.
              assert(body.includes("window.ws = new SchwabStream();"));
              body = body.replace("window.ws = new SchwabStream();", `
                const NativeWebSocket = window.WebSocket;
                window.WebSocket = class extends NativeWebSocket {
                  constructor(url, protocols) {
                    if (!/^wss?:\\/\\//.test(String(url))) throw new DOMException("WebSocket URL must be absolute", "SyntaxError");
                    super(url, protocols);
                  }
                };
                window.ws = new SchwabStream();
              `);
            }
            // Capture references in the test document only; constructor timing and
            // all host callbacks still run through the actual TradingView library.
            assert(body.includes("broker = new Broker(host); return broker;"));
            body = body.replace("broker = new Broker(host); return broker;", "window.__host = host; window.__orderUpdates = []; const update = host.orderUpdate.bind(host); host.orderUpdate = order => { window.__orderUpdates.push(order); update(order); }; broker = new Broker(host); window.__broker = broker; return broker;");
            assert(body.includes("unbindTheme = bindTheme(widget, theme);"));
            body = body.replace("unbindTheme = bindTheme(widget, theme);", "unbindTheme = bindTheme(widget, theme); window.__widget = widget;");
          }
          return await route.fulfill({ contentType: file.endsWith(".html") ? "text/html" : "text/javascript", body,
            // Exercise the nested iframe path without allowing blob navigation.
            headers: mobile && file === "index.html" ? { "Content-Security-Policy": "frame-src 'self'" } : {},
          });
        }
        if (path === "/api/auth/csrf") return await route.fulfill({ json: { token: "test-csrf" } });
        if (path === "/api/modules/investment/watchlists") {
          if (route.request().method() === "PUT") {
            if (failWatchlistSave) return await route.fulfill({ status: 503, json: { message: '自选表保存暂时失败' } });
            assert.equal(route.request().headers()['x-csrf-token'], "test-csrf");
            const input = route.request().postDataJSON();
            const sameWriter = input.writer === watchlistWriter && input.revision === watchlistBase;
            if (input.revision !== watchlists.revision && !sameWriter) return await route.fulfill({ status: 409, json: { code: "watchlists_conflict", message: "自选表已在其他窗口更新，本页改动尚未保存" } });
            if (!sameWriter || input.sequence > watchlistSequence) {
              watchlists = { revision: watchlists.revision + 1, state: input.state };
              watchlistWriter = input.writer; watchlistBase = input.revision; watchlistSequence = input.sequence;
            }
            if (heldWatchlistSave && !heldWatchlistSave.claimed) {
              heldWatchlistSave.claimed = true;
              heldWatchlistSave.started.resolve();
              await heldWatchlistSave.release.promise;
              // The page may already be gone when this intentionally delayed
              // response is released; the newer keepalive request is separate.
              return await route.fulfill({ json: { revision: watchlists.revision, sequence: watchlistSequence } }).catch(() => {});
            }
            return await route.fulfill({ json: { revision: watchlists.revision, sequence: watchlistSequence } });
          }
          return await route.fulfill({ json: watchlists });
        }
        if (useApp && path === "/api/modules/investment/schwab") {
          return await route.fulfill({ json: { appKey: "fixture", callbackUrl: "https://example.test/oauth/schwab", hasAppSecret: true, connected: true, reauthorizationRequired: false, lastError: "" } });
        }
        if (path === "/api/modules/investment/futu") {
          assert.equal(route.request().method(), "GET");
          return await route.fulfill({ json: { host: "127.0.0.1", port: 11111, enabled: false, allowNonLocal: false, connected: false, qotLogined: false, lastError: "" } });
        }
        if (useApp && !path.startsWith("/api/modules/investment/")) return await route.continue();
        calls.push({ path, method: route.request().method() });
        assert(path.startsWith("/api/modules/investment/schwab/"), "unexpected API route");
        if (path.endsWith("/ws/command")) {
          const subscription = route.request().postDataJSON();
          sockets.at(-1)?.send(JSON.stringify({ data: [{ service: subscription.service, timestamp: Date.now(), content: subscription.keys.split(",").map(key => ({
            key, "1": 101, "2": 103, "3": 102, "8": 10000, "10": 105, "11": 98, "12": 100, "17": 100, "29": 102, "31": 2, "43": 2
          })) }] }));
          return await route.fulfill({ json: { success: true } });
        }
        assert.equal(route.request().method(), "GET", "no trade may be submitted during this test");
        if (path.endsWith("/accountNumbers")) return await route.fulfill({ json: [{ accountNumber: "111", hashValue: "A" }, { accountNumber: "222", hashValue: "B" }] });
        if (/\/accounts\/[AB]$/.test(path)) {
          const first = path.endsWith("/A");
          return await route.fulfill({ json: { securitiesAccount: { positions: [{ instrument: { symbol: first ? "AAPL" : "MSFT" }, longQuantity: first ? 7 : 0, shortQuantity: first ? 0 : 3, averagePrice: 123 }] } } });
        }
        if (path.endsWith("/orders")) return await route.fulfill({ json: path.includes("/A/") ? [liveOrder, ...history] : [] });
        if (path.endsWith("/transactions")) return await route.fulfill({ json: [] });
        if (path.endsWith("/instruments")) return await route.fulfill({ json: { instruments: [{ symbol: "AAPL", description: "Apple", assetType: "EQUITY", exchange: "NASDAQ" }] } });
        if (path.endsWith("/pricehistory")) {
          const candles = [], start = Number(url.searchParams.get("startDate")), end = Number(url.searchParams.get("endDate"));
          for (let time = Math.floor(start / 86400000) * 86400000; time < end && candles.length < 2000; time += 86400000) {
            if ([0, 6].includes(new Date(time).getUTCDay())) continue;
            candles.push({ datetime: time, open: 100, high: 105, low: 98, close: 102, volume: 1000 });
          }
          return await route.fulfill({ json: { candles } });
        }
        throw new Error(`unexpected fixture API: ${path}`);
      } catch (error) {
        errors.push(error.message);
        await route.fulfill({ status: 500, json: { error: "browser fixture failed" } }).catch(() => {});
      }
    });

    await page.goto(`${origin}${useApp ? "/investment" : mobile ? "/mobile-fixture" : "/investment/terminal"}`);
    if (useApp) await page.locator("iframe[title='TradingView 投资终端']").waitFor();
    const terminal = useApp ? await (await page.locator("iframe[title='TradingView 投资终端']").elementHandle()).contentFrame() : mobile ? page.frame("investment-terminal") : page.mainFrame();
    assert(terminal, "terminal iframe missing");
    await terminal.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
    assert(calls.some(call => call.path.endsWith("/accountNumbers")), "account initialization must reach Schwab");
    await terminal.evaluate(() => window.__widget.chartReady());
    await terminal.locator("#terminal-loading").waitFor({ state: "hidden" });
    const chart = terminal.childFrames()[0];
    assert(chart, "TradingView chart iframe missing");
    assert.equal(new URL(chart.url()).pathname, "/charting_library/sameorigin.html", "chart must use a real same-origin document");
    if (mobile) {
      await terminal.waitForFunction(() => window.__widget.activeChart().dataReady());
      assert(calls.some(call => call.path.endsWith("/pricehistory")), "mobile chart must load historical bars");
      await chart.locator("canvas").first().waitFor({ state: "visible" });
      await terminal.evaluate(() => { window.__originalWidget = window.__widget; });
      if (useApp) {
        await page.screenshot({ path: join(tmpdir(), `workbench-investment-app-${useWebKit ? "webkit" : "chromium"}.png`), animations: "disabled", fullPage: true, scale: "css" });
        await page.getByRole("button", { name: "页面铺满", exact: true }).click();
      }
      for (const viewport of [{ width: 390, height: 844 }, { width: 844, height: 390 }, { width: 320, height: 568 }]) {
        await page.setViewportSize(viewport);
        const iframe = terminal.locator("#tv_chart_container iframe");
        await terminal.waitForFunction(expectedWidth => {
          const bounds = document.querySelector("#tv_chart_container iframe")?.getBoundingClientRect();
          // Mobile WebKit can retain browser chrome space in the CSS layout
          // viewport while innerHeight already reports the expanded viewport.
          const root = document.documentElement;
          return bounds && bounds.height > 200 && Math.abs(bounds.width - expectedWidth) < 2 && Math.abs(bounds.height - root.clientHeight) < 2;
        }, viewport.width);
        const bounds = await iframe.boundingBox();
        await page.touchscreen.tap(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
        assert.equal(await terminal.evaluate(() => window.__widget === window.__originalWidget), true);
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().symbol()), "AAPL");
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), "1D");
        await page.screenshot({ path: join(tmpdir(), `workbench-investment-chart-mobile-${useWebKit ? "webkit-" : ""}${viewport.width}.png`), animations: "disabled", scale: "css" });
      }
      if (useApp) {
        await page.getByRole("button", { name: "返回工作台", exact: true }).click();
        await page.getByRole("heading", { name: "投资", exact: true }).waitFor();
        assert.equal(await terminal.evaluate(() => window.__widget === window.__originalWidget), true);
        assert.equal(await terminal.locator("#terminal-loading").isHidden(), true);
        assert((await page.locator("iframe[title='TradingView 投资终端']").boundingBox()).height > 200);
      }
      const nextPaints = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await nextPaints();
      const settledNotifications = resizeNotifications;
      await nextPaints();
      assert.equal(resizeNotifications, settledNotifications, "resizing must settle instead of repeating every frame");
      assert.deepEqual(errors, []);
      console.log(`PASS (${useWebKit ? "WebKit" : "Chromium"}${useApp ? ", Workbench app" : ", HTTPS"}): actual TradingView mobile startup, same-origin iframe, historical bars, touch input and portrait/landscape sizing; no unexpected browser errors or trades.`);
      return;
    }
    await page.evaluate(() => window.__host.setAccountManagerVisibilityMode("normal"));
    await chart.getByRole("row").filter({ hasText: "AAPL" }).waitFor();
    await page.evaluate(() => {
      window.__originalWidget = window.__widget;
      window.__originalSymbol = window.__widget.activeChart().symbol();
      window.__originalResolution = window.__widget.activeChart().resolution();
    });
    for (const theme of ["dark", "light", "dark", "light", "dark", "light"]) {
      await page.evaluate(value => window.postMessage({ type: "workbench.theme", theme: value }, location.origin), theme);
      await page.waitForFunction(value => window.__widget.getTheme() === value && document.documentElement.style.colorScheme === value, theme);
      assert.equal(await chart.evaluate(() => document.documentElement.classList.contains("theme-dark")), theme === "dark");
      assert.equal(await page.evaluate(() => window.__widget === window.__originalWidget), true);
      assert.equal(await page.evaluate(() => window.__widget.activeChart().symbol() === window.__originalSymbol), true);
      assert.equal(await page.evaluate(() => window.__widget.activeChart().resolution() === window.__originalResolution), true);
    }
    await page.evaluate(() => window.__broker._refresh());
    assert.deepEqual(await page.evaluate(() => window.__orderUpdates), [], "initial history must not generate order notifications");
    assert.match(await chart.getByRole("row").filter({ hasText: "AAPL" }).innerText(), /7/);
    await chart.getByRole("tab", { name: /^订单(?:\s|$)/ }).click();
    const orderRow = chart.getByRole("row").filter({ hasText: "MSFT" });
    await orderRow.waitFor();
    await orderRow.getByText("MSFT", { exact: true }).click();
    await page.waitForFunction(() => window.__widget.activeChart().symbol() === "MSFT");
    await chart.getByRole("tab", { name: /^持仓/ }).click();
    await page.evaluate(() => window.__broker.setCurrentAccount("B"));
    await chart.getByRole("row").filter({ hasText: "MSFT" }).waitFor();
    await page.evaluate(() => window.__broker._refresh());
    assert.deepEqual(await page.evaluate(() => window.__orderUpdates), [], "switching accounts must not replay order notifications");
    assert.equal(await chart.getByRole("row").filter({ hasText: "AAPL" }).count(), 0);
    assert.match(await chart.getByRole("row").filter({ hasText: "MSFT" }).innerText(), /3/);
    const before = calls.filter(call => call.path.endsWith("/accountNumbers")).length;
    sockets.at(-1).close();
    await page.waitForFunction(() => window.__broker.connectionStatus() === 2);
    await page.waitForFunction(() => window.__broker.connectionStatus() === 1, null, { timeout: 15000 });
    assert.equal(await page.evaluate(() => window.__broker.currentAccount()), "B");
    assert(calls.filter(call => call.path.endsWith("/accountNumbers")).length > before);
    await chart.getByRole("row").filter({ hasText: "MSFT" }).waitFor();
    assert.deepEqual(errors, []);
    await page.screenshot({ path: join(tmpdir(), "workbench-investment-account-manager.png"), fullPage: true, animations: "disabled" });

    // Open the native order ticket without submitting it. Verify the actual UI
    // uses our translations and only offers the supported request enums.
    await page.evaluate(() => { void window.__host.showOrderDialog({ symbol: "MSFT", type: 1, side: 1, qty: 2, limitPrice: 101 }); });
    const duration = chart.getByRole("combobox", { name: "有效期", exact: true });
    const session = chart.getByRole("combobox", { name: "交易时段", exact: true });
    await duration.filter({ hasText: "DAY(当天有效)" }).waitFor();
    await session.waitFor();
    assert.equal(await chart.getByText("有效时间", { exact: true }).count(), 0);
    assert.equal(await duration.innerText(), "DAY(当天有效)");
    assert.equal(await session.inputValue(), "常规(9:30-16:00 ET)");
    await duration.click();
    await chart.getByRole("option", { name: "IOC(立即成交，否则取消)", exact: true }).waitFor();
    assert.deepEqual((await chart.getByRole("option").allTextContents()).map(text => text.trim()), ["DAY(当天有效)", "GTC(取消前有效)", "FOK(立即全部成交，否则取消)", "IOC(立即成交，否则取消)"]);
    await chart.getByRole("option", { name: "IOC(立即成交，否则取消)", exact: true }).click();
    await session.click();
    await chart.getByRole("option", { name: "盘后(16:05-20:00 ET)", exact: true }).waitFor();
    assert.deepEqual((await chart.getByRole("option").allTextContents()).map(text => text.trim()), ["常规(9:30-16:00 ET)", "盘前(7:00-9:25 ET)", "盘后(16:05-20:00 ET)", "延长时段(7:00-20:00 ET)"]);
    await chart.getByRole("option", { name: "盘后(16:05-20:00 ET)", exact: true }).click();
    await chart.getByRole("button", { name: "关闭按钮", exact: true }).click();

    // An existing order must override the previously selected session.
    await page.evaluate(() => window.__broker.setCurrentAccount("A"));
    await chart.getByRole("row").filter({ hasText: "AAPL" }).waitFor();
    await page.evaluate(async () => {
      const [existing] = await window.__broker.orders();
      void window.__host.showOrderDialog(existing);
    });
    await session.waitFor();
    assert.equal(await session.inputValue(), "延长时段(7:00-20:00 ET)");
    assert.deepEqual(errors, []);
    await page.screenshot({ path: join(tmpdir(), "workbench-investment-order-ticket.png"), fullPage: true, animations: "disabled" });
    await chart.getByRole("button", { name: "关闭按钮", exact: true }).click();
    liveOrder = { ...order, status: "FILLED", filledQuantity: 2 };
    await page.evaluate(() => window.dispatchEvent(new CustomEvent("ACCT_ACTIVITY", { detail: { content: [{}] } })));
    await page.waitForFunction(() => window.__orderUpdates.some(order => order.id === "42" && order.status === 2));
    await page.evaluate(() => window.__broker._refresh());
    assert.deepEqual(await page.evaluate(() => window.__orderUpdates.map(order => [order.id, order.status])), [["42", 2]], "a new fill must be announced exactly once");
    const watchlistSaved = page.waitForResponse(response => response.url().endsWith('/watchlists') && response.request().method() === 'PUT' && response.request().postDataJSON().state.lists.length === 2 && response.ok());
    const selectedList = await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      const first = api.getActiveListId();
      api.renameList(first, '长期关注');
      api.updateList(first, ['###科技', 'MSFT', 'AAPL']);
      const second = api.createList('观察中', ['SPY']);
      await new Promise(resolve => setTimeout(resolve, 0));
      api.setActiveList(second.id);
      return second.id;
    });
    await watchlistSaved;
    await page.waitForFunction(() => document.getElementById('watchlist-status').hidden);
    // A new browser/device starts without TradingView's local settings. Clear
    // them before reload so this verifies the workspace copy, not localStorage.
    await page.evaluate(() => localStorage.clear());
    await page.reload();
    await page.waitForFunction(() => window.__broker?.connectionStatus() === 1);
    await page.evaluate(async () => { await window.__widget.chartReady(); await window.__broker._refresh(); });
    assert.equal(await page.evaluate(async () => (await window.__broker.ordersHistory()).length), 3);
    assert.deepEqual(await page.evaluate(() => window.__orderUpdates), [], "reloading must not replay previous fills");
    await page.locator('#terminal-loading').waitFor({ state: 'hidden' });
    assert.deepEqual(await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      return { activeId: api.getActiveListId(), lists: Object.values(api.getAllLists()).map(({ title, symbols }) => ({ title, symbols })) };
    }), { activeId: selectedList, lists: [{ title: '长期关注', symbols: ['###科技', 'MSFT', 'AAPL'] }, { title: '观察中', symbols: ['SPY'] }] });
    const deletionSaved = page.waitForResponse(response => response.url().endsWith('/watchlists') && response.request().method() === 'PUT' && response.request().postDataJSON().state.lists.length === 1 && response.ok());
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      const inactive = Object.keys(api.getAllLists()).find(id => id !== api.getActiveListId());
      api.deleteList(inactive);
    });
    await deletionSaved;
    assert.equal(watchlists.state.lists.length, 1, 'deleting an inactive list must reach workspace storage');
    failWatchlistSave = true;
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      api.updateList(api.getActiveListId(), []);
    });
    await page.getByRole('alert').filter({ hasText: '自选表保存暂时失败' }).waitFor();
    failWatchlistSave = false;
    const retrySaved = page.waitForResponse(response => response.url().endsWith('/watchlists') && response.request().method() === 'PUT' && response.ok());
    await page.getByRole('button', { name: '重试保存', exact: true }).click();
    await retrySaved;
    await page.locator('#watchlist-status').waitFor({ state: 'hidden' });
    await page.reload();
    await page.locator('#terminal-loading').waitFor({ state: 'hidden' });
    assert.deepEqual(await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      return Object.values(api.getAllLists()).map(list => list.symbols);
    }), [[]], 'saved empty lists must survive refresh without reviving deleted symbols');
    heldWatchlistSave = { started: Promise.withResolvers(), release: Promise.withResolvers(), claimed: false };
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      api.updateList(api.getActiveListId(), ['GOOG']);
    });
    await heldWatchlistSave.started.promise;
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      api.updateList(api.getActiveListId(), ['NVDA']);
    });
    await page.reload();
    await page.locator('#terminal-loading').waitFor({ state: 'hidden' });
    heldWatchlistSave.release.resolve();
    assert.deepEqual(await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      return api.getList(api.getActiveListId());
    }), ['NVDA'], 'refresh during an older save must recover the latest edit');
    assert.deepEqual(watchlists.state.lists[0].symbols, ['NVDA']);
    watchlistBase = watchlists.revision;
    watchlists = { revision: watchlists.revision + 1, state: { ...watchlists.state, lists: watchlists.state.lists.map(list => ({ ...list, symbols: ['IBM'] })) } };
    watchlistWriter = 'another-device'; watchlistSequence = 1;
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      api.updateList(api.getActiveListId(), ['AAPL']);
    });
    await page.getByRole('button', { name: '加载工作台版本', exact: true }).waitFor();
    await page.reload();
    await page.locator('#terminal-loading').waitFor({ state: 'hidden' });
    await page.getByRole('button', { name: '加载工作台版本', exact: true }).waitFor();
    assert.deepEqual(await page.evaluate(async () => (await window.__widget.watchList()).getList()), ['AAPL'], 'conflicting local edits must remain recoverable');
    assert.deepEqual(watchlists.state.lists[0].symbols, ['IBM'], 'the old page must not replace the other device state');
    await page.getByRole('button', { name: '加载工作台版本', exact: true }).click();
    await page.waitForFunction(() => document.getElementById('terminal-loading').hidden && document.getElementById('watchlist-status').hidden);
    assert.deepEqual(await page.evaluate(async () => (await window.__widget.watchList()).getList()), ['IBM']);
    assert.deepEqual(errors, []);
    console.log("PASS: actual TradingView themes, account manager, account switch/reconnect, order ticket, history without replay, live fill notification and workspace watchlists restored without localStorage; no browser errors or trades.");
  } finally {
    await browser?.close();
    if (app && app.exitCode === null && app.pid) {
      const stopped = new Promise(resolve => app.once("exit", resolve));
      app.kill("SIGTERM");
      await stopped;
    }
    if (directory) await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
