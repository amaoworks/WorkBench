// Real TradingView library + repository terminal code. Every Schwab request is
// intercepted with fictional data; no workspace credentials or trades are used.
const assert = require("node:assert/strict");
const { readFile, writeFile, mkdir } = require("node:fs/promises");
const { createHash } = require("node:crypto");
const { join } = require("node:path");
const { tmpdir } = require("node:os");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const origin = "http://workbench.test";
const libraryOrigin = "https://trading-terminal.tradingview-widget.com";
const assets = join(__dirname, "../internal/modules/investment/chart");
const cache = join(tmpdir(), "workbench-tv-test-cache");
const order = {
  orderId: 42, orderType: "LIMIT", orderStrategyType: "SINGLE", session: "SEAMLESS", duration: "GOOD_TILL_CANCEL",
  quantity: 2, price: 101, status: "WORKING", enteredTime: new Date().toISOString(),
  orderLegCollection: [{ instruction: "BUY", quantity: 2, instrument: { symbol: "MSFT", assetType: "EQUITY" } }]
};
let liveOrder = order;
const history = [
  { ...order, orderId: 40, status: "FILLED", filledQuantity: 2 },
  { ...order, orderId: 41, status: "CANCELED" },
];

(async () => {
  await mkdir(cache, { recursive: true });
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const errors = [], calls = [], sockets = [];
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    await page.routeWebSocket("**/trader/ws", socket => {
      sockets.push(socket);
      socket.send(JSON.stringify({ stream: { status: "ready" } }));
    });
    await page.route(`${origin}/**`, async route => {
      const url = new URL(route.request().url()), path = url.pathname;
      try {
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
            // Capture references in the test document only; constructor timing and
            // all host callbacks still run through the actual TradingView library.
            assert(body.includes("broker = new Broker(host); return broker;"));
            body = body.replace("broker = new Broker(host); return broker;", "window.__host = host; window.__orderUpdates = []; const update = host.orderUpdate.bind(host); host.orderUpdate = order => { window.__orderUpdates.push(order); update(order); }; broker = new Broker(host); window.__broker = broker; return broker;");
            assert(body.includes("widget.chartReady().then(function () {});"));
            body = body.replace("widget.chartReady().then(function () {});", "window.__widget = widget;");
          }
          return await route.fulfill({ contentType: file.endsWith(".html") ? "text/html" : "text/javascript", body });
        }
        if (path === "/api/auth/csrf") return await route.fulfill({ json: { token: "test-csrf" } });
        if (path === "/api/modules/investment/futu") {
          assert.equal(route.request().method(), "GET");
          return await route.fulfill({ json: { host: "127.0.0.1", port: 11111, enabled: false, allowNonLocal: false, connected: false, qotLogined: false, lastError: "" } });
        }
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

    await page.goto(`${origin}/investment/terminal`);
    await page.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
    assert(calls.some(call => call.path.endsWith("/accountNumbers")), "account initialization must reach Schwab");
    await page.evaluate(async () => { await window.__widget.chartReady(); window.__host.setAccountManagerVisibilityMode("normal"); });
    const chart = page.frames().find(frame => frame !== page.mainFrame());
    assert(chart, "TradingView chart iframe missing");
    await chart.getByRole("row").filter({ hasText: "AAPL" }).waitFor();
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
    await page.reload();
    await page.waitForFunction(() => window.__broker?.connectionStatus() === 1);
    await page.evaluate(async () => { await window.__widget.chartReady(); await window.__broker._refresh(); });
    assert.equal(await page.evaluate(async () => (await window.__broker.ordersHistory()).length), 3);
    assert.deepEqual(await page.evaluate(() => window.__orderUpdates), [], "reloading must not replay previous fills");
    assert.deepEqual(errors, []);
    console.log("PASS: actual TradingView account manager, account switch/reconnect, order ticket, history without replay and live fill notification; no browser errors or trades.");
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
