// Real TradingView library + repository terminal code. All Schwab/Futu requests
// are intercepted with fictional data; no workspace credentials or trades are used.
const assert = require("node:assert/strict");
const { readFile, writeFile, mkdir, mkdtemp, rm } = require("node:fs/promises");
const { spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const { join } = require("node:path");
const { tmpdir } = require("node:os");
const { chromium, webkit, devices } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const useWebKit = process.argv.includes("--webkit");
const useApp = process.argv.includes("--app");
const testNight = process.argv.includes("--night");
const test24h = process.argv.includes("--24h") || testNight;
const testSessions = testNight || test24h;
const testChartState = process.argv.includes("--state");
const testPreferences = process.argv.includes("--preferences");
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
let preferences = { entries: {} };
const preferenceWriters = new Map();
const history = [
  { ...order, orderId: 40, status: "FILLED", filledQuantity: 2 },
  { ...order, orderId: 41, status: "CANCELED" },
];
let overnightEnabled = testSessions || testChartState || testPreferences;
let fullDayFixtures = false;

const etFixtureClock = new Intl.DateTimeFormat("en-US", {
  timeZone: "America/New_York", weekday: "short", hour: "numeric", minute: "numeric", hourCycle: "h23",
});
function minuteParts(time) {
  const parts = Object.fromEntries(etFixtureClock.formatToParts(new Date(time)).map(part => [part.type, part.value]));
  return { weekday: parts.weekday, minute: Number(parts.hour) * 60 + Number(parts.minute) };
}
function minuteHistoryFixture(fromMs, toMs, resolution, source, poisonBoundaries = false) {
  const step = Math.max(1, Number(resolution) || 1) * 60_000;
  const lookback = Math.min(5 * 86_400_000, step * 2500);
  const start = Math.max(Number(fromMs) || 0, toMs - lookback);
  const first = Math.ceil(start / step) * step;
  const candles = [];
  for (let time = first; time < toMs && candles.length < 3000; time += step) {
    const { weekday, minute } = minuteParts(time);
    const weekdaySession = ["Mon", "Tue", "Wed", "Thu", "Fri"].includes(weekday);
    let include = false, poison = false;
    if (source === "schwab") {
      include = weekdaySession && minute >= 4 * 60 && minute < 20 * 60;
      poison = poisonBoundaries && weekdaySession && (minute === 3 * 60 + 55 || minute === 20 * 60);
    } else {
      const nightWindow = minute >= 20 * 60 || minute < 4 * 60;
      const nightWeekday = minute >= 20 * 60
        ? ["Sun", "Mon", "Tue", "Wed", "Thu"].includes(weekday)
        : weekdaySession;
      include = nightWindow && nightWeekday;
      poison = poisonBoundaries && weekdaySession && (minute === 4 * 60 || minute === 19 * 60 + 55);
    }
    if (!include && !poison) continue;
    const close = poison ? (source === "schwab" ? 990 : 880) : (source === "schwab" ? 100 : 200);
    const candle = { open: close, high: close + 0.5, low: close - 0.5, close, volume: 25 };
    candles.push(source === "schwab" ? { datetime: time, ...candle } : { time, ...candle });
  }
  return candles;
}

async function fulfillPreferences(route) {
  if (route.request().method() === 'GET') return route.fulfill({ json: preferences });
  assert.equal(route.request().method(), 'PUT');
  assert.equal(route.request().headers()['x-csrf-token'], 'test-csrf');
  const input = route.request().postDataJSON();
  assert.equal(typeof input.name, 'string');
  assert.equal(typeof input.writer, 'string');
  assert(Number.isSafeInteger(input.revision) && input.revision >= 0);
  assert(Number.isSafeInteger(input.sequence) && input.sequence > 0);
  const current = preferences.entries[input.name];
  const revision = current?.revision || 0;
  const previous = preferenceWriters.get(input.name);
  const sameWriter = previous?.writer === input.writer && previous.base === input.revision;
  if (input.revision !== revision && !sameWriter) {
    return route.fulfill({ status: 409, json: { code: 'preferences_conflict', message: '投资设置已在其他窗口更新' } });
  }
  if (!sameWriter || input.sequence > previous.sequence) {
    const nextRevision = revision + 1;
    preferences.entries[input.name] = { revision: nextRevision, value: input.value };
    preferenceWriters.set(input.name, {
      writer: input.writer,
      base: sameWriter ? previous.base : input.revision,
      sequence: input.sequence,
    });
  }
  const accepted = preferenceWriters.get(input.name);
  return route.fulfill({ json: { revision: preferences.entries[input.name].revision, sequence: accepted.sequence } });
}

async function pollUntil(check, message, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const result = await check();
      if (result) return result;
    } catch (error) {
      lastError = error;
    }
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`${message}${lastError ? `: ${lastError.message}` : ''}`);
}

(async () => {
  assert(!testPreferences || useApp, '--preferences requires --app so restoration uses one temporary workspace database');
  await mkdir(cache, { recursive: true });
  const quoteCompat = JSON.parse(await readFile(join(assets, 'tv-quotes-compat.json'), 'utf8'));
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
    let context = await browser.newContext(mobile ? devices["iPhone 13"] : { viewport: { width: 1440, height: 1000 } });
    let page = await context.newPage();
    const errors = [], calls = [], sockets = [];
    let resizeNotifications = 0;
    const installPageRoutes = async (targetContext, targetPage) => {
    targetPage.on("pageerror", error => {
      // WebKit can defer a ResizeObserver notification during layout changes.
      // The sizing and readiness assertions below still require a usable chart.
      if (["ResizeObserver loop completed with undelivered notifications.", "ResizeObserver loop limit exceeded"].includes(error.message)) { resizeNotifications++; return; }
      errors.push(error.message);
    });
    targetPage.on("console", message => {
      if (message.type() !== "error") return;
      if (message.location().url?.endsWith('/api/modules/investment/watchlists') && /503|409/.test(message.text())) return;
      errors.push(message.text());
    });
    await targetPage.routeWebSocket("**/trader/ws", socket => {
      sockets.push(socket);
      socket.send(JSON.stringify({ stream: { status: "ready" } }));
    });
    if (testSessions || testChartState || testPreferences) await targetPage.routeWebSocket("**/futu/quote/ws", socket => {
      socket.send(JSON.stringify({ stream: { status: "ready", generation: "fixture-futu-1" } }));
    });
    await targetPage.route(`${origin}/**`, async route => {
      const url = new URL(route.request().url()), path = url.pathname;
      try {
        if (testChartState && path === '/state-away') return await route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>总览</title>' });
        if (path === "/mobile-fixture") {
          return await route.fulfill({ contentType: "text/html", body: `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
            <body style="margin:0"><iframe name="investment-terminal" title="投资终端" src="/investment/terminal" style="position:fixed;inset:0;width:100%;height:100%;border:0"></iframe></body></html>` });
        }
        if (path.startsWith("/charting_library/")) {
          const libraryPath = path.replace('/charting_library/quotes-v1/', '/charting_library/');
          const file = join(cache, createHash("sha256").update(libraryPath).digest("hex") + ".json");
          let asset;
          // Always refresh the entry point. Its hashed bundles can be reused.
          if (/\.[a-f0-9]{12,}\./.test(path)) {
            try { asset = JSON.parse(await readFile(file, "utf8")); } catch { /* first visit */ }
          }
          if (!asset) {
            const response = await targetContext.request.get(libraryOrigin + libraryPath);
            assert(response.ok(), `TradingView asset HTTP ${response.status()}: ${path}`);
            asset = { type: response.headers()["content-type"], body: (await response.body()).toString("base64") };
            await writeFile(file, JSON.stringify(asset));
          }
          let body = Buffer.from(asset.body, "base64");
          if (libraryPath.endsWith('/' + quoteCompat.bundle)) {
            const source = body.toString('utf8');
            assert.equal(source.split(quoteCompat.from).length, 2, 'known TV Details quote-copy patch must match once');
            body = Buffer.from(source.replace(quoteCompat.from, quoteCompat.to));
          }
          return await route.fulfill({ contentType: asset.type, body });
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
            const brokerFactory = `      broker_factory: function (host) {
        broker = new Broker(host, {
          startAfter: brokerStart,
          preferredAccount: preferences.get('account') || '',
          onAccountChanged: account => preferences.set('account', account),
        });
        return broker;
      },`;
            assert(body.includes(brokerFactory));
            body = body.replace(brokerFactory, `      broker_factory: function (host) {
        window.__host = host;
        window.__orderUpdates = [];
        const update = host.orderUpdate.bind(host);
        host.orderUpdate = order => { window.__orderUpdates.push(order); update(order); };
        broker = new Broker(host, {
          startAfter: brokerStart,
          preferredAccount: preferences.get('account') || '',
          onAccountChanged: account => preferences.set('account', account),
        });
        window.__broker = broker;
        return broker;
      },`);
            const themeBinding = "unbindTheme = bindTheme(widget, theme, window, () => layoutSync?.schedule());";
            assert(body.includes(themeBinding));
            body = body.replace(themeBinding, `${themeBinding} window.__widget = widget;`);
            assert(body.includes('if (disposed) watchlistSync.dispose();'));
            body = body.replace('if (disposed) watchlistSync.dispose();', 'window.__watchlistRestored = true; if (disposed) watchlistSync.dispose();');
            const pagehide = "    window.addEventListener('pagehide', () => {";
            assert(body.includes(pagehide));
            body = body.replace(pagehide, `
              window.__datafeed = datafeed;
              window.__barResults = [];
              const getBars = datafeed.getBars.bind(datafeed);
              datafeed.getBars = (info, resolution, params, onResult, onError) => getBars(info, resolution, params, (bars, metadata) => {
                window.__barResults.push({
                  symbol: info.name, session: info.subsession_id || 'regular', resolution,
                  count: bars.length, times: bars.map(bar => bar.time), hasPoison: bars.some(bar => bar.close >= 800), noData: metadata?.noData === true,
                });
                onResult(bars, metadata);
              }, onError);
              ${pagehide}
            `);
          }
          return await route.fulfill({ contentType: file.endsWith(".html") ? "text/html" : file.endsWith(".css") ? "text/css" : "text/javascript", body,
            // Exercise the nested iframe path without allowing blob navigation.
            headers: mobile && file === "index.html" ? { "Content-Security-Policy": "frame-src 'self'" } : {},
          });
        }
        if (path === "/api/auth/csrf") {
          if (useApp) return await route.continue();
          return await route.fulfill({ json: { token: "test-csrf" } });
        }
        if (path === "/api/modules/investment/preferences") {
          if (useApp) return await route.continue();
          return await fulfillPreferences(route);
        }
        if (path === "/api/modules/investment/watchlists") {
          if (useApp && testPreferences) return await route.continue();
          if (route.request().method() === "PUT") {
            if (failWatchlistSave) return await route.fulfill({ status: 503, json: { message: '自选表保存暂时失败' } });
            if (useApp) assert(route.request().headers()['x-csrf-token']);
            else assert.equal(route.request().headers()['x-csrf-token'], "test-csrf");
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
        if (path === "/api/modules/investment/overnight") {
          assert.equal(route.request().method(), "GET");
          return await route.fulfill({ json: { enabled: overnightEnabled, provider: "futu", providerEnabled: overnightEnabled } });
        }
        if ((testSessions || testChartState || testPreferences) && path.startsWith("/api/modules/investment/futu/")) {
          if (path.endsWith('/kline')) {
            const from = Number(url.searchParams.get('from')) * 1000;
            const to = Number(url.searchParams.get('to')) * 1000;
            const resolution = url.searchParams.get('resolution') || '5';
            const strict = url.searchParams.get('requireHistory') === 'true';
            const candles = minuteHistoryFixture(from, to, resolution, 'futu', strict && fullDayFixtures);
            calls.push({ path, method: route.request().method(), search: url.search, source: 'futu', barCount: candles.length });
            return await route.fulfill({ json: { candles } });
          }
          calls.push({ path, method: route.request().method(), search: url.search, source: 'futu' });
          const command = route.request().postDataJSON();
          assert(command.generation === 'fixture-futu-1' || (command.command === 'ADD' && command.generation === undefined), 'only a bootstrap ADD may omit the Futu generation');
          return await route.fulfill({ json: { success: true, applied: true, generation: 'fixture-futu-1' } });
        }
        if (path === "/api/modules/investment/futu") {
          assert.equal(route.request().method(), "GET");
          return await route.fulfill({ json: { host: "127.0.0.1", port: 11111, enabled: false, allowNonLocal: false, connected: false, qotLogined: false, lastError: "" } });
        }
        if (useApp && !path.startsWith("/api/modules/investment/")) return await route.continue();
        calls.push({ path, method: route.request().method(), search: url.search });
        assert(path.startsWith("/api/modules/investment/schwab/"), "unexpected API route");
        if (path.endsWith("/ws/command")) {
          const subscription = route.request().postDataJSON();
          sockets.at(-1)?.send(JSON.stringify({ data: [{ service: subscription.service, timestamp: Date.now(), content: subscription.keys.split(",").map(key => ({
            key, "1": 101, "2": 103, "3": 102, "8": 10000, "10": 105, "11": 98, "12": 100, "17": 100, "29": 102, "31": 2, "35": Date.now(), "36": Date.now(), "43": 2
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
        if (path.endsWith("/quotes")) return await route.fulfill({ json: Object.fromEntries((url.searchParams.get('symbols') || '').split(',').map(symbol => [symbol, {
          symbol, assetMainType: 'EQUITY',
          quote: { lastPrice: 102, closePrice: 100, bidPrice: 101, askPrice: 103, openPrice: 100, highPrice: 105, lowPrice: 98, totalVolume: 10000, tradeTime: Date.now() },
          regular: { regularMarketLastPrice: 102, regularMarketNetChange: 2, regularMarketPercentChange: 2 },
        }])) });
        if (path.endsWith("/pricehistory")) {
          const start = Number(url.searchParams.get("startDate")), end = Number(url.searchParams.get("endDate"));
          if ((testSessions || testChartState || testPreferences) && url.searchParams.get('frequencyType') === 'minute') {
            const candles = minuteHistoryFixture(start, end, url.searchParams.get('frequency'), 'schwab', fullDayFixtures);
            calls.at(-1).source = 'schwab';
            calls.at(-1).barCount = candles.length;
            return await route.fulfill({ json: { candles } });
          }
          const candles = [];
          for (let time = Math.floor(start / 86400000) * 86400000; time < end && candles.length < 2000; time += 86400000) {
            if ([0, 6].includes(new Date(time).getUTCDay())) continue;
            candles.push({ datetime: time, open: 100, high: 105, low: 98, close: 102, volume: 1000 });
          }
          calls.at(-1).source = 'schwab';
          calls.at(-1).barCount = candles.length;
          return await route.fulfill({ json: { candles } });
        }
        throw new Error(`unexpected fixture API: ${path}`);
      } catch (error) {
        errors.push(error.message);
        await route.fulfill({ status: 500, json: { error: "browser fixture failed" } }).catch(() => {});
      }
    });
    };
    await installPageRoutes(context, page);

    await page.goto(`${origin}${useApp ? "/investment" : mobile ? "/mobile-fixture" : "/investment/terminal"}`);
    if (useApp) await page.locator("iframe[title='投资终端']").waitFor();
    let terminal = useApp ? await (await page.locator("iframe[title='投资终端']").elementHandle()).contentFrame() : mobile ? page.frame("investment-terminal") : page.mainFrame();
    assert(terminal, "terminal iframe missing");
    await terminal.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
    assert(calls.some(call => call.path.endsWith("/accountNumbers")), "account initialization must reach Schwab");
    await terminal.evaluate(() => window.__widget.chartReady());
    await terminal.locator("#terminal-loading").waitFor({ state: "hidden" });
    let chart = terminal.childFrames()[0];
    assert(chart, "TradingView chart iframe missing");
    assert.equal(new URL(chart.url()).pathname, "/charting_library/quotes-v1/sameorigin.html", "chart must use a real same-origin document");
    const resolutions = await terminal.evaluate(() => new Promise(resolve => window.__datafeed.onReady(config => resolve(config.supported_resolutions))));
    assert.deepEqual(resolutions, ['1', '5', '15', '30', '1D', '1W', '1M'], '10-minute bars must be absent from the TV datafeed');
    const readPreferences = () => terminal.evaluate(async () => {
      const response = await fetch('/api/modules/investment/preferences');
      if (!response.ok) throw new Error(`preferences HTTP ${response.status}`);
      return (await response.json()).entries;
    });
    const waitForPreferences = async (check, message) => {
      let last;
      try {
        return await pollUntil(async () => {
          last = await readPreferences();
          return check(last) ? last : null;
        }, message);
      } catch (error) {
        const sources = last?.layout?.value?.charts?.flatMap(chart => chart.panes?.flatMap(pane => pane.sources?.map(({ id, type }) => ({ id, type })) || []) || []);
        const concise = Object.fromEntries(Object.entries(last || {}).filter(([name]) => name !== 'layout').map(([name, entry]) => [name, {
          revision: entry.revision, value: typeof entry.value === 'string' && entry.value.length > 160 ? `${entry.value.slice(0, 160)}…` : entry.value,
        }]));
        console.error('Saved preferences:', JSON.stringify({ ...concise, layout: { revision: last?.layout?.revision, sources } }));
        throw error;
      }
    };
    if (testPreferences) {
      const sessionButton = label => chart.getByRole('button', { name: new RegExp(`^行情时段：${label}。`) });
      const chooseSession = async (label, title, nextLabel) => {
        await sessionButton(label).click();
        await chart.getByText(title, { exact: true }).click();
        await sessionButton(nextLabel).waitFor();
      };
      await terminal.evaluate(() => window.__widget.setSymbol('MSFT', '15'));
      await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'MSFT' && window.__widget.activeChart().resolution() === '15' && window.__widget.activeChart().dataReady());
      await chooseSession('常规', '扩展 (ETH)', '扩展');
      await waitForPreferences(entries => entries.chart?.value?.symbol === 'MSFT' && entries.chart.value.resolution === '15' &&
        entries.chart.value.session === 'extended', 'chart selections were not saved');
      await sessionButton('扩展').waitFor();
      assert.equal(await terminal.evaluate(() => window.__widget.activeChart().symbol()), 'MSFT');
      assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), '15');

      await chooseSession('扩展', '常规 (RTH)', '常规');
      await terminal.evaluate(() => window.__widget.setSymbol('AAPL', '5'));
      await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'AAPL' && window.__widget.activeChart().resolution() === '5' && window.__widget.activeChart().dataReady());
      await terminal.waitForFunction(() => window.__watchlistRestored);
      const workspaceListSaved = page.waitForResponse(response => response.url().endsWith('/watchlists') &&
        response.request().method() === 'PUT' && response.request().postDataJSON().state.lists.some(list =>
          list.title === '服务端关注' && JSON.stringify(list.symbols) === JSON.stringify(['###科技', 'MSFT', 'AAPL'])) && response.ok());
      const created = await terminal.evaluate(async () => {
        const active = window.__widget.activeChart();
        const studyId = await active.createStudy('Moving Average', false, false, { length: 9 });
        const range = active.getVisibleRange();
        const span = Math.max(120, Math.floor((range.to - range.from) / 4));
        const shapeId = await active.createMultipointShape([
          { time: range.from + span, price: 99 },
          { time: range.from + 2 * span, price: 104 },
        ], { shape: 'trend_line', overrides: { linecolor: '#ef5350', linewidth: 2 } });
        window.__widget.dateFormat().setValue('dd-MM-yyyy');
        window.__widget.timeHoursFormat().setValue('12-hours');
        window.__broker.setCurrentAccount('B');
        const lists = await window.__widget.watchList();
        const watchlistId = lists.getActiveListId();
        lists.renameList(watchlistId, '服务端关注');
        lists.updateList(watchlistId, ['###科技', 'MSFT', 'AAPL']);
        return { studyId, shapeId, watchlistId };
      });
      await workspaceListSaved;
      assert(created.studyId != null && created.shapeId != null, 'real TradingView indicator and drawing must be created');
      await waitForPreferences(entries => {
        const values = Object.entries(entries).filter(([name]) => name.startsWith('tv:')).map(([, entry]) => entry.value);
        const savedLayout = JSON.stringify(entries.layout?.value);
        return entries.chart?.value?.symbol === 'AAPL' && entries.chart.value.resolution === '5' && entries.chart.value.session === 'regular' &&
          entries.layout?.value?.charts?.length && savedLayout.includes(String(created.studyId)) && savedLayout.includes(String(created.shapeId)) &&
          entries.account?.value === 'B' &&
          values.includes('dd-MM-yyyy') && values.includes('12-hours');
      }, 'created chart objects and preferences were not saved');
      const firstEntries = await readPreferences();
      const firstLayoutRevision = firstEntries.layout.revision;
      assert(JSON.stringify(firstEntries.layout.value).includes(String(created.studyId)), 'saved layout must contain the created indicator ID');
      assert(JSON.stringify(firstEntries.layout.value).includes(String(created.shapeId)), 'saved layout must contain the created drawing ID');
      assert(Object.keys(firstEntries).some(name => name.startsWith('tv:')), 'TradingView settings must reach workspace preferences');

      await context.close();
      context = await browser.newContext(mobile ? devices['iPhone 13'] : { viewport: { width: 1440, height: 1000 } });
      page = await context.newPage();
      await installPageRoutes(context, page);
      await page.goto(`${origin}/investment`);
      await page.locator("iframe[title='投资终端']").waitFor();
      terminal = await (await page.locator("iframe[title='投资终端']").elementHandle()).contentFrame();
      await terminal.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
      await terminal.evaluate(() => window.__widget.chartReady());
      await terminal.locator('#terminal-loading').waitFor({ state: 'hidden' });
      chart = terminal.childFrames()[0];
      assert(chart, 'TradingView chart iframe missing after clean-context restore');
      await terminal.waitForFunction(() => window.__widget.activeChart().dataReady());
      await terminal.waitForFunction(() => window.__watchlistRestored);
      assert.deepEqual(await terminal.evaluate(async () => {
        const lists = await window.__widget.watchList();
        return { activeId: lists.getActiveListId(), title: lists.getAllLists()[lists.getActiveListId()].title, symbols: lists.getList() };
      }), { activeId: created.watchlistId, title: '服务端关注', symbols: ['###科技', 'MSFT', 'AAPL'] }, 'workspace watchlists must restore from the real database in a clean context');
      await sessionButton('常规').waitFor();
      assert.deepEqual(await terminal.evaluate(() => ({
        symbol: window.__widget.activeChart().symbol(),
        resolution: String(window.__widget.activeChart().resolution()),
        account: window.__broker.currentAccount(),
        dateFormat: window.__widget.dateFormat().value(),
        timeHoursFormat: window.__widget.timeHoursFormat().value(),
      })), { symbol: 'AAPL', resolution: '5', account: 'B', dateFormat: 'dd-MM-yyyy', timeHoursFormat: '12-hours' });
      const restoredObjects = await terminal.evaluate(() => ({
        studies: window.__widget.activeChart().getAllStudies(),
        shapes: window.__widget.activeChart().getAllShapes(),
      }));
      const restoredStudy = restoredObjects.studies.find(study => /moving average/i.test(study.name));
      const restoredShape = restoredObjects.shapes.find(shape => shape.id === created.shapeId) || restoredObjects.shapes[0];
      assert(restoredStudy, 'saved indicator must restore in a clean browser context');
      assert(restoredShape, 'saved drawing must restore in a clean browser context');

      const removed = { studyId: restoredStudy.id, shapeId: restoredShape.id };
      await terminal.evaluate(({ studyId, shapeId }) => {
        const active = window.__widget.activeChart();
        const study = active.getAllStudies().find(item => item.id === studyId);
        const shape = active.getAllShapes().find(item => item.id === shapeId);
        if (!study || !shape) throw new Error('restored chart objects are missing');
        active.removeEntity(study.id);
        active.removeEntity(shape.id);
        window.__widget.dateFormat().setValue('yyyy-MM-dd');
        window.__widget.timeHoursFormat().setValue('24-hours');
        window.__broker.setCurrentAccount('A');
      }, removed);
      await terminal.evaluate(() => window.__widget.setSymbol('MSFT', '15'));
      await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'MSFT' && window.__widget.activeChart().resolution() === '15' && window.__widget.activeChart().dataReady());
      await chooseSession('常规', '扩展 (ETH)', '扩展');
      await waitForPreferences(entries => {
        const values = Object.entries(entries).filter(([name]) => name.startsWith('tv:')).map(([, entry]) => entry.value);
        const savedLayout = JSON.stringify(entries.layout?.value);
        return entries.layout?.revision > firstLayoutRevision && !savedLayout.includes(String(removed.studyId)) && !savedLayout.includes(String(removed.shapeId)) &&
          entries.chart?.value?.symbol === 'MSFT' &&
          entries.chart.value.resolution === '15' && entries.chart.value.session === 'extended' && entries.account?.value === 'A' &&
          values.includes('yyyy-MM-dd') && values.includes('24-hours');
      }, 'deleted chart objects and later preferences were not saved');
      await page.reload();
      await page.locator("iframe[title='投资终端']").waitFor();
      terminal = await (await page.locator("iframe[title='投资终端']").elementHandle()).contentFrame();
      await terminal.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
      await terminal.evaluate(() => window.__widget.chartReady());
      await terminal.locator('#terminal-loading').waitFor({ state: 'hidden' });
      chart = terminal.childFrames()[0];
      await terminal.waitForFunction(() => window.__widget.activeChart().dataReady());
      await sessionButton('扩展').waitFor();
      assert.deepEqual(await terminal.evaluate(() => ({
        symbol: window.__widget.activeChart().symbol(),
        resolution: String(window.__widget.activeChart().resolution()),
        account: window.__broker.currentAccount(),
        dateFormat: window.__widget.dateFormat().value(),
        timeHoursFormat: window.__widget.timeHoursFormat().value(),
        studies: window.__widget.activeChart().getAllStudies().filter(study => /moving average/i.test(study.name)).length,
        shapes: window.__widget.activeChart().getAllShapes().length,
      })), { symbol: 'MSFT', resolution: '15', account: 'A', dateFormat: 'yyyy-MM-dd', timeHoursFormat: '24-hours', studies: 0, shapes: 0 });
      await page.goto(`${origin}/investment/terminal?symbol=AAPL`);
      terminal = page.mainFrame();
      await terminal.waitForFunction(() => window.__broker?.connectionStatus() === 1, null, { timeout: 30000 });
      await terminal.evaluate(() => window.__widget.chartReady());
      await terminal.locator('#terminal-loading').waitFor({ state: 'hidden' });
      await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'AAPL' && window.__widget.activeChart().dataReady());
      await waitForPreferences(entries => entries.chart?.value?.symbol === 'AAPL' && entries.chart.value.resolution === '15' &&
        entries.chart.value.session === 'extended', 'URL symbol did not override the saved layout');
      assert.deepEqual(errors, []);
      console.log('PASS (Chromium, Workbench app): workspace chart layout, watchlists, account, indicator, drawing and user settings survive a storage-empty browser context; later edits and deletions survive refresh.');
      return;
    }
    if (testChartState) {
      const sessionButton = label => chart.getByRole('button', { name: new RegExp(`^行情时段：${label}。`) });
      const chooseSession = async (label, title, nextLabel) => {
        await sessionButton(label).click();
        await chart.getByText(title, { exact: true }).click();
        await sessionButton(nextLabel).waitFor();
      };
      const storedState = async expected => {
        const entries = await waitForPreferences(current => JSON.stringify(current.chart?.value) === JSON.stringify(expected),
          `chart preference was not saved as ${JSON.stringify(expected)}`);
        return entries.chart.value;
      };
      const reopen = async (reload = false) => {
        if (reload) await page.reload();
        else {
          await page.goto(`${origin}/state-away`);
          await page.goto(`${origin}${useApp ? '/investment?view=terminal' : mobile ? '/mobile-fixture' : '/investment/terminal'}`);
        }
        if (useApp) await page.locator("iframe[title='投资终端']").waitFor();
        terminal = useApp ? await (await page.locator("iframe[title='投资终端']").elementHandle()).contentFrame() : mobile ? page.frame('investment-terminal') : page.mainFrame();
        await terminal.waitForFunction(() => Boolean(window.__widget));
        await terminal.evaluate(() => window.__widget.chartReady());
        try { await terminal.locator('#terminal-loading').waitFor({ state: 'hidden' }); }
        catch (error) {
          console.error('Terminal restore failure:', await terminal.locator('#terminal-loading').innerText(), errors);
          throw error;
        }
        chart = terminal.childFrames()[0];
      };
      assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), '1D');
      await chooseSession('常规', '扩展 (ETH)', '扩展');
      await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5' && window.__barResults.some(result => result.session === 'extended' && result.count > 0));
      await terminal.locator('#session-toast').filter({ hasText: '扩展行情已切换到 5 分钟' }).waitFor();
      const watchlistSaved = page.waitForResponse(response => response.url().endsWith('/watchlists') && response.request().method() === 'PUT' && response.request().postDataJSON().state.lists.some(list => list.title === '状态恢复' && list.symbols.includes('SPY')) && response.ok());
      await terminal.evaluate(async () => {
        const api = await window.__widget.watchList();
        const list = api.createList('状态恢复', ['SPY']);
        api.setActiveList(list.id);
      });
      await watchlistSaved;
      await terminal.evaluate(() => window.__widget.setSymbol('MSFT', '15'));
      await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'MSFT' && window.__widget.activeChart().resolution() === '15' && window.__widget.activeChart().dataReady());
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '15', session: 'extended' }), { symbol: 'MSFT', resolution: '15', session: 'extended' });
      await terminal.evaluate(() => new Promise((resolve, reject) => window.__datafeed.resolveSymbol('MSFT', resolve, reject)));
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '15', session: 'extended' }), { symbol: 'MSFT', resolution: '15', session: 'extended' }, 'a regular-hours watchlist resolution of the same symbol must not overwrite chart state');
      for (const reload of [false, true]) {
        await reopen(reload);
        await sessionButton('扩展').waitFor();
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().symbol()), 'MSFT');
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), '15');
        await terminal.waitForFunction(() => window.__barResults.some(result => result.symbol === 'MSFT' && result.session === 'extended' && result.count > 0));
        await terminal.waitForFunction(async () => window.__watchlistRestored && (await window.__widget.watchList()).getList().includes('SPY'));
        assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '15', session: 'extended' }), { symbol: 'MSFT', resolution: '15', session: 'extended' });
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().symbol()), 'MSFT', 'watchlist restoration must preserve the selected chart symbol');
      }
      await chooseSession('扩展', '夜盘 (Night)', '夜盘');
      await terminal.waitForFunction(() => window.__barResults.some(result => result.session === 'night' && result.count > 0));
      await reopen(true);
      await sessionButton('夜盘').waitFor();
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '15', session: 'night' }), { symbol: 'MSFT', resolution: '15', session: 'night' });
      overnightEnabled = false;
      await reopen(true);
      await sessionButton('常规').waitFor();
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '15', session: 'regular' }), { symbol: 'MSFT', resolution: '15', session: 'regular' });
      await sessionButton('常规').click();
      assert.equal(await chart.getByText('夜盘 (Night)', { exact: true }).count(), 0);
      assert.equal(await chart.getByText('全天 (24H)', { exact: true }).count(), 0);
      await page.keyboard.press('Escape');
      await terminal.evaluate(() => window.__widget.activeChart().setResolution('1W'));
      await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '1W' && window.__widget.activeChart().dataReady());
      await reopen(true);
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '1W', session: 'regular' }), { symbol: 'MSFT', resolution: '1W', session: 'regular' });
      assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), '1W');
      overnightEnabled = true;
      fullDayFixtures = true;
      await reopen(true);
      await chooseSession('常规', '全天 (24H)', '全天');
      await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5' && window.__barResults.some(result => result.session === 'equity24h' && result.count > 0));
      await reopen(true);
      await sessionButton('全天').waitFor();
      assert.deepEqual(await storedState({ symbol: 'MSFT', resolution: '5', session: 'equity24h' }), { symbol: 'MSFT', resolution: '5', session: 'equity24h' });
      await terminal.waitForFunction(() => window.__barResults.some(result => result.session === 'equity24h' && result.count > 0));
      assert.deepEqual(errors, []);
      console.log(`PASS (${useWebKit ? 'WebKit' : 'Chromium'}, ${mobile ? 'mobile' : 'desktop'}): first RTH → ETH on daily bars, symbol/interval/session restoration alongside workspace watchlists after leaving and reload, Night/24H restoration and disabled-coverage fallback.`);
      return;
    }
    if (testSessions) {
      if (!mobile) {
        const details = chart.locator('[data-test-id-widget-type="detail"] .widgetbar-widgetbody');
        const sessionStats = details.locator('[data-qa-id="details-element status"]');
        await terminal.waitForFunction(() => window.__datafeed.quotesListeners.size > 0);
        const emitQuote = async (time, price) => terminal.evaluate(({ time, price }) => {
          window.__datafeed.now = () => Date.parse(time);
          window.dispatchEvent(new CustomEvent('LEVELONE_ANY', { detail: {
            service: 'LEVELONE_EQUITIES', timestamp: Date.now(), content: [{
              key: 'AAPL', '3': price, '29': 102, '31': 2, '43': 2, '12': 100,
              '17': 100, '10': 105, '11': 98, '8': 10000, '35': Date.parse(time),
            }],
          } }));
        }, { time, price });
        await emitQuote('2026-09-09T12:00:00Z', 104); // 08:00 ET
        await sessionStats.getByText('盘前', { exact: true }).waitFor();
        await sessionStats.getByText(/104\.00/).waitFor();
        assert.match(await sessionStats.innerText(), /1\.96%/, 'pre-market change is relative to 102 regular close, not 100 previous close');
        await emitQuote('2026-09-09T15:00:00Z', 102); // 11:00 ET
        await sessionStats.getByText('盘中', { exact: true }).waitFor();
        await emitQuote('2026-09-09T20:05:00Z', 105); // 16:05 ET
        await sessionStats.getByText('盘后', { exact: true }).waitFor();
        await sessionStats.getByText(/105\.00/).waitFor();
        assert.match(await sessionStats.innerText(), /2\.94%/, 'post-market fields must update the actual details panel');
        await terminal.evaluate(() => {
          window.__datafeed.now = () => Date.parse('2026-09-10T01:00:00Z'); // 21:00 ET
          window.dispatchEvent(new CustomEvent('FUTU_QUOTE', { detail: {
            symbol: 'AAPL', quote: { lp: 102, ch: 0, chp: 0, high: 999, low: 1, volume: 5 },
          } }));
        });
        await sessionStats.getByText('夜盘', { exact: true }).waitFor();
        assert.doesNotMatch(await sessionStats.innerText(), /BOATS/i, 'night session label must not imply a specific upstream source');
        await sessionStats.getByText(/102\.00/).waitFor().catch(async error => {
          console.error({ quote: await terminal.evaluate(() => window.__datafeed.quote_cache.get('AAPL')), stats: await sessionStats.innerText(), errors });
          throw error;
        });
        assert.match(await sessionStats.innerText(), /0\.00%/, 'zero overnight change must render, rather than disappear');
        const quote = await terminal.evaluate(() => window.__datafeed.quote_cache.get('AAPL'));
        assert.equal(quote.cs, 'night');
        assert.equal(quote.ntc_time, null, 'missing overnight trade time must stay missing');
        assert.deepEqual([quote.lp, quote.high_price, quote.low_price, quote.volume], [102, 105, 98, 10000], 'night statistics must not replace the main daily values');
        await terminal.evaluate(() => { window.__datafeed.now = Date.now; window.__datafeed.publishQuote('AAPL'); });
      }
      const sessionButton = label => chart.getByRole('button', { name: new RegExp(`^行情时段：${label}。`) });
      const chooseSession = async (currentLabel, itemTitle, nextLabel) => {
        await sessionButton(currentLabel).click();
        await chart.getByText(itemTitle, { exact: true }).click();
        await sessionButton(nextLabel).waitFor({ state: 'visible' });
      };
      await sessionButton('常规').waitFor({ state: 'visible' }).catch(async error => {
        console.error({ errors, buttons: await chart.getByRole('button').evaluateAll(buttons => buttons.map(button => ({ text: button.textContent, label: button.getAttribute('aria-label'), title: button.getAttribute('title') }))) });
        throw error;
      });
      await sessionButton('常规').click();
      for (const item of ['✓ 常规 (RTH)', '扩展 (ETH)', '夜盘 (Night)', '全天 (24H)']) {
        await chart.getByText(item, { exact: true }).waitFor({ state: 'visible' });
      }
      assert.equal(await chart.getByText(/夜盘周期说明/).count(), 0, 'the menu must not add a Night explanation item');
      assert.equal(await chart.getByText(/24H说明/).count(), 0, 'the menu must not add a 24H explanation item');
      await page.keyboard.press('Escape');
      assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), '1D');
      await chooseSession('常规', '扩展 (ETH)', '扩展');
      await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5' && window.__barResults.some(result => result.session === 'extended' && result.count > 0));
      assert(calls.some(call => call.path.endsWith('/pricehistory') && call.search.includes('needExtendedHoursData=true')), 'first RTH → ETH must load extended history');
      await chooseSession('扩展', '常规 (RTH)', '常规');
      await terminal.evaluate(() => window.__widget.activeChart().setResolution('1D'));
      if (testNight) {
        await sessionButton('常规').click();
        await chart.getByText('夜盘 (Night)', { exact: true }).click();
        await sessionButton('夜盘').waitFor({ state: 'visible' });
        await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5');
        await terminal.locator('#session-toast').filter({ hasText: '已切换到 5 分钟' }).waitFor({ state: 'visible' });
        await terminal.waitForFunction(() => window.__barResults.some(result => result.session === 'night' && result.count > 0));
        assert(calls.some(call => call.path.endsWith('/futu/kline') && call.barCount > 0 && !call.search.includes('requireHistory=true')), 'Night must load non-empty fictional Futu bars');
      }
      if (mobile) {
        const currentLabel = testNight ? '夜盘' : '常规';
        const bounds = await sessionButton(currentLabel).boundingBox();
        await page.screenshot({ path: join(tmpdir(), 'workbench-investment-session-mobile.png'), animations: 'disabled', scale: 'css' });
        assert(bounds && bounds.x >= 0 && bounds.x + bounds.width <= page.viewportSize().width, `compact session button must fit the mobile header: ${JSON.stringify({ bounds, viewport: page.viewportSize() })}`);
        assert(await terminal.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), 'terminal must not gain horizontal overflow');
      }

      if (testNight) {
        const schwabBeforeNight = calls.filter(call => call.path.endsWith('/pricehistory')).length;
        await page.waitForTimeout(500);
        assert.equal(calls.filter(call => call.path.endsWith('/pricehistory')).length, schwabBeforeNight, 'Night must not request Schwab K lines');
        await terminal.evaluate(() => { void window.__widget.activeChart().setResolution('1D'); });
        await terminal.locator('#session-toast').filter({ hasText: '已切换到 5 分钟' }).waitFor({ state: 'visible' });
        await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5');
        await chooseSession('夜盘', '扩展 (ETH)', '扩展');
        const nativeSession = chart.locator('button[data-name="session-menu"]');
        await nativeSession.waitFor({ state: 'attached' });
        assert.equal(await nativeSession.evaluate(el => getComputedStyle(el).display), 'none', 'do not expose two conflicting session controls');
        await terminal.waitForFunction(() => window.__barResults.some(result => result.session === 'extended' && result.count > 0));
        assert(calls.some(call => call.path.endsWith('/pricehistory') && call.search.includes('needExtendedHoursData=true')), 'ETH must return to Schwab history');
        await chooseSession('扩展', '常规 (RTH)', '常规');
        await terminal.waitForFunction(() => window.__barResults.some(result => result.session === 'regular' && result.count > 0), null, { timeout: 30000 });
      }
      const waitForSessionBars = async (session, resolution = '5') => {
        await terminal.waitForFunction(({ session, resolution }) => window.__barResults.some(result => result.session === session && result.resolution === resolution && result.count > 0), { session, resolution });
      };
      const etClock = new Intl.DateTimeFormat('en-US', { timeZone: 'America/New_York', hour: 'numeric', minute: 'numeric', hourCycle: 'h23' });
      if (test24h) {
        fullDayFixtures = true;
        const beforeFullDay = calls.length;
        const currentLabel = '常规';
        await chooseSession(currentLabel, '全天 (24H)', '全天');
        await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5');
        await waitForSessionBars('equity24h', '5');
        await terminal.waitForFunction(() => window.__widget.activeChart().dataReady());
        const fullDayBars = await terminal.evaluate(() => window.__barResults.findLast(result => result.session === 'equity24h' && result.resolution === '5' && result.count > 0));
        assert(fullDayBars && fullDayBars.count > 20, `TradingView must receive non-empty merged 24H bars: ${JSON.stringify(fullDayBars)}`);
        assert.equal(fullDayBars.hasPoison, false, 'out-of-session Schwab/Futu boundary fixtures must be filtered');
        const etMinutes = fullDayBars.times.map(time => {
          const parts = Object.fromEntries(etClock.formatToParts(new Date(time)).map(part => [part.type, part.value]));
          return Number(parts.hour) * 60 + Number(parts.minute);
        });
        assert(etMinutes.some(minute => minute === 4 * 60), '24H merged data must contain the 04:00 ET Schwab boundary');
        assert(etMinutes.some(minute => minute === 20 * 60), '24H merged data must contain the 20:00 ET Futu boundary');
        assert(etMinutes.some(minute => minute >= 4 * 60 && minute < 20 * 60), '24H data must include Schwab daytime candles');
        assert(etMinutes.some(minute => minute >= 20 * 60 || minute < 4 * 60), '24H data must include Futu overnight candles');
        const visible = await terminal.evaluate(() => window.__widget.activeChart().getVisibleRange());
        assert(fullDayBars.times.some(time => time / 1000 >= visible.from && time / 1000 <= visible.to), 'non-empty merged bars must intersect the actual TradingView visible range');
        const dualSourceCalls = calls.slice(beforeFullDay);
        assert(dualSourceCalls.some(call => call.source === 'schwab' && call.barCount > 0 && call.search.includes('needExtendedHoursData=true')), '24H must fetch non-empty extended Schwab history');
        assert(dualSourceCalls.some(call => call.source === 'futu' && call.path.endsWith('/kline') && call.barCount > 0 && call.search.includes('requireHistory=true')), '24H must fetch non-empty strict Futu history');

        for (const resolution of ['1', '5', '15', '30']) {
          await terminal.evaluate(value => window.__widget.activeChart().setResolution(value), resolution);
          await terminal.waitForFunction(value => window.__widget.activeChart().resolution() === value, resolution);
          await waitForSessionBars('equity24h', resolution);
        }
        await terminal.evaluate(() => window.__widget.activeChart().setResolution('1D'));
        await terminal.locator('#session-toast').filter({ hasText: '全天行情仅支持 1、5、15、30 分钟，已切换到 5 分钟' }).waitFor({ state: 'visible' });
        await terminal.waitForFunction(() => window.__widget.activeChart().resolution() === '5');

        await chooseSession('全天', '常规 (RTH)', '常规');
        await waitForSessionBars('regular', '5');
        const cachedRegularBars = await terminal.evaluate(() => window.__barResults.findLast(result => result.session === 'regular' && result.resolution === '5' && result.count > 0));
        assert(cachedRegularBars, 'cached 24H → RTH switch must restore regular-session bars');
        await chooseSession('常规', '全天 (24H)', '全天');
        await waitForSessionBars('equity24h', '5');
        const cached24Bars = await terminal.evaluate(() => window.__barResults.findLast(result => result.session === 'equity24h' && result.resolution === '5' && result.count > 0));
        assert(cached24Bars && cached24Bars.count > 20, 'cached RTH → 24H switch must restore the full-day session and its merged bars without rebuilding the chart');

        overnightEnabled = false;
        const futuBeforeDisabledSymbol = calls.filter(call => call.path.endsWith('/futu/kline')).length;
        await terminal.evaluate(() => { window.__datafeed.overlayCachedAt = 0; window.__widget.setSymbol('MSFT', '5'); });
        await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'MSFT' && window.__barResults.some(result => result.symbol === 'MSFT' && result.session === 'regular'));
        await sessionButton('常规').click();
        assert.equal(await chart.getByText('夜盘 (Night)', { exact: true }).count(), 0, 'disabled overnight coverage must hide Night');
        assert.equal(await chart.getByText('全天 (24H)', { exact: true }).count(), 0, 'disabled overnight coverage must hide 24H');
        await page.keyboard.press('Escape');
        assert.equal(calls.filter(call => call.path.endsWith('/futu/kline')).length, futuBeforeDisabledSymbol, 'disabled full-day coverage must not query Futu or degrade to Schwab-only bars');

        overnightEnabled = true;
        await terminal.evaluate(() => { window.__datafeed.overlayCachedAt = 0; window.__widget.setSymbol('AAPL', '5'); });
        await terminal.waitForFunction(() => window.__widget.activeChart().symbol() === 'AAPL' && window.__barResults.some(result => result.symbol === 'AAPL'));
        await chooseSession('常规', '全天 (24H)', '全天');
        await waitForSessionBars('equity24h', '5');
        await terminal.evaluate(() => window.__widget.setSymbol('AAPL 270115C00200000', '5'));
        await terminal.waitForFunction(() => window.__widget.activeChart().symbol().includes('AAPL 270115C00200000'));
        await sessionButton('常规').waitFor({ state: 'visible' });
        await sessionButton('常规').click();
        assert.equal(await chart.getByText('全天 (24H)', { exact: true }).count(), 0, 'non-equity symbols must not offer 24H');
        assert.equal(await chart.getByText('夜盘 (Night)', { exact: true }).count(), 0, 'non-equity symbols must not offer Night');
        await page.keyboard.press('Escape');
      }
      assert.equal(await terminal.locator('#session-toolbar, #session-select, #night-interval-hint').count(), 0, 'no standalone session row may remain');
      assert(await terminal.evaluate(() => {
        const bounds = document.getElementById('tv_chart_container').getBoundingClientRect();
        return bounds.top === 0 && Math.abs(bounds.height - document.documentElement.clientHeight) < 2;
      }), 'the chart must recover the full terminal height');
      await terminal.evaluate(() => { window.__originalWidget = window.__widget; });
      for (const theme of ['dark', 'light']) {
        await page.evaluate(value => {
          const frame = document.querySelector('iframe[title="投资终端"], iframe[name="investment-terminal"]');
          (frame?.contentWindow || window).postMessage({ type: 'workbench.theme', theme: value }, location.origin);
        }, theme);
        await terminal.waitForFunction(value => window.__widget.getTheme() === value && document.documentElement.style.colorScheme === value, theme);
        await sessionButton('常规').click();
        await chart.getByText('✓ 常规 (RTH)', { exact: true }).waitFor({ state: 'visible' });
        await page.keyboard.press('Escape');
      }
      if (mobile) {
        for (const viewport of [{ width: 320, height: 568 }, { width: 844, height: 390 }, { width: 390, height: 844 }]) {
          await page.setViewportSize(viewport);
          await sessionButton('常规').filter({ hasText: viewport.width <= 600 ? /^常规$/ : /^行情：常规$/ }).waitFor({ state: 'visible' });
          await sessionButton('常规').click();
          await chart.getByText('✓ 常规 (RTH)', { exact: true }).waitFor({ state: 'visible' });
          const bounds = await sessionButton('常规').boundingBox();
          assert(bounds && bounds.x >= 0 && bounds.x + bounds.width <= viewport.width, `session menu must stay reachable at ${viewport.width}px`);
          assert(await terminal.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), 'resizing must not add page overflow');
          await page.screenshot({ path: join(tmpdir(), `workbench-investment-session-${useWebKit ? 'webkit-' : ''}${viewport.width}.png`), animations: 'disabled', scale: 'css' });
          await page.keyboard.press('Escape');
        }
      } else {
        await sessionButton('常规').click();
        await page.screenshot({ path: join(tmpdir(), 'workbench-investment-session-desktop.png'), animations: 'disabled', scale: 'css' });
        await page.keyboard.press('Escape');
      }
      assert.equal(await terminal.evaluate(() => window.__widget === window.__originalWidget), true, 'theme and responsive menu changes must preserve the chart');
      assert.equal(await chart.getByText(/夜盘周期说明|全天行情说明|24H说明/).count(), 0, 'the session menu must have no extra explanation items');
      assert.deepEqual(errors, []);
      console.log(`PASS (${useWebKit ? 'WebKit' : 'Chromium'}, ${mobile ? 'mobile' : 'desktop'}): four-option header menu, ${testNight ? 'Night Futu-only bars and ' : ''}non-empty ET-filtered 24H Schwab/Futu merge, interval limits, cached session switches, coverage/symbol gating, full chart height, themes and responsive sizing.`);
      return;
    }
    if (mobile) {
      await terminal.waitForFunction(() => window.__widget.activeChart().dataReady());
      assert(calls.some(call => call.path.endsWith("/pricehistory")), "mobile chart must load historical bars");
      await chart.locator("canvas").first().waitFor({ state: "visible" });
      await terminal.evaluate(() => { window.__originalWidget = window.__widget; });
      if (useApp) {
        await page.screenshot({ path: join(tmpdir(), `workbench-investment-app-${useWebKit ? "webkit" : "chromium"}.png`), animations: "disabled", fullPage: true, scale: "css" });
      }
      for (const viewport of [{ width: 390, height: 844 }, { width: 844, height: 390 }, { width: 320, height: 568 }]) {
        await page.setViewportSize(viewport);
        const iframe = terminal.locator("#tv_chart_container iframe");
        await terminal.waitForFunction(expectedWidth => {
          const bounds = document.querySelector("#tv_chart_container iframe")?.getBoundingClientRect();
          // Mobile WebKit can retain browser chrome space in the CSS layout
          // viewport while innerHeight already reports the expanded viewport.
          const container = document.getElementById('tv_chart_container');
          return bounds && bounds.height > 200 && Math.abs(bounds.width - expectedWidth) < 2 && Math.abs(bounds.height - container.clientHeight) < 2;
        }, viewport.width);
        const bounds = await iframe.boundingBox();
        await page.touchscreen.tap(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2);
        assert.equal(await terminal.evaluate(() => window.__widget === window.__originalWidget), true);
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().symbol()), "AAPL");
        assert.equal(await terminal.evaluate(() => window.__widget.activeChart().resolution()), "1D");
        await page.screenshot({ path: join(tmpdir(), `workbench-investment-chart-mobile-${useWebKit ? "webkit-" : ""}${viewport.width}.png`), animations: "disabled", scale: "css" });
      }
      if (useApp) {
        await page.getByRole("heading", { name: "投资", exact: true }).waitFor();
        assert.equal(await terminal.evaluate(() => window.__widget === window.__originalWidget), true);
        assert.equal(await terminal.locator("#terminal-loading").isHidden(), true);
        assert((await page.locator("iframe[title='投资终端']").boundingBox()).height > 200);
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
    // The Details panel renders bid/ask under the main price. custom.css hides
    // that row while the quote data and the rest of the panel stay intact.
    const detailBody = chart.locator('[data-test-id-widget-type="detail"] .widgetbar-widgetbody');
    const bidAskRow = detailBody.locator('[class*="container-hO37ndqM"]');
    await bidAskRow.waitFor({ state: "attached" });
    assert.match(await detailBody.locator('[class*="bid-hO37ndqM"]').innerText(), /\d/, "details panel must still receive bid quotes");
    assert.match(await detailBody.locator('[class*="ask-hO37ndqM"]').innerText(), /\d/, "details panel must still receive ask quotes");
    assert.equal(await bidAskRow.evaluate(el => getComputedStyle(el).display), "none", "custom.css must hide the details bid/ask row");
    assert.match(await detailBody.innerText(), /当日价格范围/, "the rest of the details panel must stay visible");
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
    const latestSaved = page.waitForResponse(response => response.url().endsWith('/watchlists') &&
      response.request().method() === 'PUT' && response.request().postDataJSON().state.lists[0].symbols.includes('NVDA') && response.ok());
    heldWatchlistSave.release.resolve();
    await latestSaved;
    await page.reload();
    await page.locator('#terminal-loading').waitFor({ state: 'hidden' });
    assert.deepEqual(await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      return api.getList(api.getActiveListId());
    }), ['NVDA'], 'refresh must restore the latest edit confirmed by the server');
    assert.deepEqual(watchlists.state.lists[0].symbols, ['NVDA']);
    watchlistBase = watchlists.revision;
    watchlists = { revision: watchlists.revision + 1, state: { ...watchlists.state, lists: watchlists.state.lists.map(list => ({ ...list, symbols: ['IBM'] })) } };
    watchlistWriter = 'another-device'; watchlistSequence = 1;
    await page.evaluate(async () => {
      const api = await window.__widget.watchList();
      api.updateList(api.getActiveListId(), ['AAPL']);
    });
    await page.getByRole('button', { name: '加载工作台版本', exact: true }).waitFor();
    assert.deepEqual(await page.evaluate(async () => (await window.__widget.watchList()).getList()), ['AAPL'], 'conflicting edits remain in this page until the user loads the server state');
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
