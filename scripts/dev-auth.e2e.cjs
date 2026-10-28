// Disposable HTTPS -> Vite -> Go login and chart regression. Uses temporary credentials and data.
const assert = require("node:assert/strict");
const { mkdtemp, readFile, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const { spawn, execFileSync } = require("node:child_process");
const { randomBytes } = require("node:crypto");
const http = require("node:http");
const https = require("node:https");
const net = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

async function port() {
  const server = net.createServer();
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const value = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return value;
}

async function verifyInvestmentChart(page) {
  const responses = [], fixtureErrors = [];
  let historyCandles = 0;
  const onResponse = response => {
    const path = new URL(response.url()).pathname;
    if (path.startsWith("/investment/terminal") || path.startsWith("/charting_library/")) {
      responses.push({ path, status: response.status() });
    }
  };
  page.on("response", onResponse);
  await page.route("**/api/modules/investment/schwab", route => route.fulfill({ json: {
    appKey: "fixture", callbackUrl: "https://example.test/oauth/schwab", hasAppSecret: true,
    connected: true, reauthorizationRequired: false, lastError: "",
  } }));
  await page.routeWebSocket("**/trader/ws", socket => {
    socket.send(JSON.stringify({ stream: { status: "ready" } }));
  });
  await page.route("**/api/modules/investment/schwab/**", async route => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname;
    try {
      if (path.endsWith("/ws/command") && request.method() === "POST") {
        return await route.fulfill({ json: { success: true } });
      }
      assert.equal(request.method(), "GET", "only subscription commands may write during this test");
      if (path.endsWith("/accountNumbers")) return await route.fulfill({ json: [{ accountNumber: "111", hashValue: "DEV-PROXY" }] });
      if (path.endsWith("/accounts/DEV-PROXY")) return await route.fulfill({ json: { securitiesAccount: { positions: [] } } });
      if (path.endsWith("/orders") || path.endsWith("/transactions")) return await route.fulfill({ json: [] });
      if (path.endsWith("/instruments")) return await route.fulfill({ json: { instruments: [{ symbol: "AAPL", description: "Apple", assetType: "EQUITY", exchange: "NASDAQ" }] } });
      if (path.endsWith("/quotes")) return await route.fulfill({ json: Object.fromEntries((url.searchParams.get("symbols") || "").split(",").map(symbol => [symbol, {
        symbol, assetMainType: "EQUITY",
        quote: { lastPrice: 102, closePrice: 100, bidPrice: 101, askPrice: 103, openPrice: 100, highPrice: 105, lowPrice: 98, totalVolume: 10000, tradeTime: Date.now() },
      }])) });
      if (path.endsWith("/pricehistory")) {
        const start = Number(url.searchParams.get("startDate")), end = Number(url.searchParams.get("endDate"));
        const candles = [];
        for (let time = Math.floor(start / 86400000) * 86400000; time < end && candles.length < 2000; time += 86400000) {
          if ([0, 6].includes(new Date(time).getUTCDay())) continue;
          candles.push({ datetime: time, open: 100, high: 105, low: 98, close: 102, volume: 1000 });
        }
        historyCandles += candles.length;
        return await route.fulfill({ json: { candles } });
      }
      assert.fail(`unexpected broker API: ${path}`);
    } catch (error) {
      fixtureErrors.push(error.message);
      await route.fulfill({ status: 500, json: { message: "dev chart fixture failed" } }).catch(() => {});
    }
  });
  try {
    const firstModule = page.waitForResponse(response => new URL(response.url()).pathname === "/investment/terminal/datafeed.js");
    await page.getByRole("link", { name: "投资", exact: true }).click();
    assert([200, 304].includes((await firstModule).status()), "terminal modules must pass HTTPS origin checks through Vite");
    const terminal = page.frameLocator("iframe[title='投资终端']");
    await terminal.locator("#terminal-loading").waitFor({ state: "hidden", timeout: 90000 });
    await terminal.frameLocator("#tv_chart_container iframe").first().locator("canvas").first().waitFor({ state: "visible", timeout: 90000 });
    assert(historyCandles > 0, "chart must load non-empty historical candles");
    assert(responses.some(response => response.path === "/investment/terminal" && response.status === 200));
    assert(responses.some(response => response.path.endsWith("/charting_library.esm.js") && response.status === 200));
    assert(responses.some(response => response.path.endsWith("/sameorigin.html") && response.status === 200));
    assert.deepEqual(responses.filter(response => response.status >= 400), [], "terminal and chart-library assets must pass the real HTTPS -> Vite -> Go proxy");
    assert.deepEqual(fixtureErrors, []);
    await page.getByRole("link", { name: "资讯", exact: true }).click();
    await page.getByRole("heading", { name: "资讯", exact: true }).waitFor();
  } finally {
    page.off("response", onResponse);
  }
}

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-dev-auth-"));
  const hostname = "dev-login.workbench.test";
  const initialPassword = `Dev-Login-${randomBytes(12).toString("hex")}`;
  const changedPassword = `${initialPassword}-Changed`;
  const backendPort = await port();
  const webPort = await port();
  const root = resolve(__dirname, "..");
  const key = join(directory, "key.pem"), cert = join(directory, "cert.pem");
  let proxy, browser, launcher, launcherDone;
  let logs = "";
  const stop = async () => {
    if (launcher && launcher.exitCode === null && launcher.signalCode === null) launcher.kill("SIGTERM");
    if (launcherDone) await launcherDone;
    launcher = undefined;
    launcherDone = undefined;
  };
  try {
    execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-keyout", key, "-out", cert, "-subj", `/CN=${hostname}`, "-addext", `subjectAltName=DNS:${hostname}`], { stdio: "ignore" });
    proxy = https.createServer({ key: await readFile(key), cert: await readFile(cert) }, (request, response) => {
      const upstream = http.request({ host: "127.0.0.1", port: webPort, method: request.method, path: request.url, headers: request.headers }, res => {
        response.writeHead(res.statusCode, res.headers);
        res.pipe(response);
      });
      upstream.on("error", () => { if (!response.headersSent) response.writeHead(502); response.end(); });
      request.pipe(upstream);
    });
    proxy.on("upgrade", (request, socket, head) => {
      const upstream = http.request({ host: "127.0.0.1", port: webPort, method: request.method, path: request.url, headers: request.headers });
      upstream.on("upgrade", (response, upstreamSocket, upstreamHead) => {
        socket.write(`HTTP/1.1 ${response.statusCode} ${response.statusMessage}\r\n${response.rawHeaders.reduce((lines, value, index) => index % 2 ? lines : `${lines}${value}: ${response.rawHeaders[index + 1]}\r\n`, "")}\r\n`);
        if (head.length) upstreamSocket.write(head);
        if (upstreamHead.length) socket.write(upstreamHead);
        socket.on("error", () => upstreamSocket.destroy());
        upstreamSocket.on("error", () => socket.destroy());
        socket.pipe(upstreamSocket).pipe(socket);
      });
      upstream.on("error", () => socket.destroy());
      upstream.on("response", () => socket.destroy());
      upstream.end();
    });
    await new Promise(resolve => proxy.listen(0, "127.0.0.1", resolve));
    const origin = `https://${hostname}:${proxy.address().port}`;
    const host = new URL(origin).host;
    const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith("WORKBENCH_") && !name.startsWith("OPENAI_")));
    env.WORKBENCH_PASSWORD = initialPassword;
    env.WORKBENCH_FUTU_RUNTIME_DIR = join(directory, "futu-runtime");
    const start = async publicURL => {
      logs = "";
      const args = ["scripts/dev.mjs", "-auth", "password", "-listen", `127.0.0.1:${backendPort}`, "-allowed-host", host, "--web-port", String(webPort), "-data", join(directory, "data.db")];
      if (publicURL) args.push("-public-url", publicURL);
      launcher = spawn(process.execPath, args, { cwd: root, env, stdio: ["ignore", "pipe", "pipe"] });
      launcher.stdout.on("data", chunk => { logs += chunk; });
      launcher.stderr.on("data", chunk => { logs += chunk; });
      launcherDone = new Promise((resolve, reject) => { launcher.once("error", reject); launcher.once("exit", code => resolve(code)); });
      const deadline = Date.now() + 60_000;
      while (true) {
        if (launcher.exitCode !== null || launcher.signalCode !== null) throw new Error(`Dev launcher exited: ${logs}`);
        if (await fetch(`http://127.0.0.1:${webPort}/health/ready`).then(res => res.ok).catch(() => false)) break;
        if (Date.now() > deadline) throw new Error(`Dev launcher did not become ready: ${logs}`);
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    };
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox", "--no-proxy-server", `--host-resolver-rules=MAP ${hostname} 127.0.0.1`] });
    const login = async (page, password) => {
      await page.getByLabel("密码", { exact: true }).fill(password);
      const response = page.waitForResponse(res => res.url().endsWith("/api/auth/login") && res.request().method() === "POST");
      await page.getByRole("button", { name: "登录", exact: true }).click();
      return response;
    };

    await start("");
    assert.match(logs, /For HTTPS access, add -public-url/);
    let context = await browser.newContext({ ignoreHTTPSErrors: true });
    let page = await context.newPage();
    await page.goto(origin);
    const denied = await login(page, initialPassword);
    assert.equal(denied.status(), 403);
    assert.equal((await denied.json()).code, "invalid_origin");
    await context.close();
    await stop();

    await start(origin);
    context = await browser.newContext({ ignoreHTTPSErrors: true });
    page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(origin);
    assert.equal((await login(page, initialPassword)).status(), 200);
    await page.getByRole("heading", { name: "账户安全", exact: true }).waitFor();
    const cookies = await context.cookies();
    for (const name of ["workbench_session", "workbench_csrf"]) {
      const cookie = cookies.find(cookie => cookie.name === name);
      assert(cookie?.secure && cookie.httpOnly, `${name} must be Secure and HttpOnly`);
    }
    await page.getByLabel("当前密码", { exact: true }).fill(initialPassword);
    await page.getByLabel("新密码", { exact: true }).fill(changedPassword);
    await page.getByLabel("确认新密码", { exact: true }).fill(changedPassword);
    await page.getByRole("button", { name: "更新密码", exact: true }).click();
    await page.getByRole("heading", { name: "欢迎回来", exact: true }).waitFor();
    assert.equal((await login(page, changedPassword)).status(), 200);
    await page.getByRole("link", { name: "资讯", exact: true }).click();
    await page.getByRole("heading", { name: "资讯", exact: true }).waitFor();
    await page.reload();
    await page.getByRole("heading", { name: "资讯", exact: true }).waitFor();
    const status = await page.evaluate(async () => {
      const response = await fetch("/api/auth/status");
      return { code: response.status, body: await response.json() };
    });
    assert.equal(status.code, 200);
    assert.equal(status.body.authenticated, true);
    assert.equal(status.body.mustChangePassword, false);
    await verifyInvestmentChart(page);
    assert.deepEqual(errors, []);
    await context.close();
    console.log("PASS: HTTPS -> Vite -> Go reproduces 403 without public URL; configured origin supports login, Secure cookies, first password change, persisted session and the real investment chart with non-empty fixture candles.");
  } finally {
    if (browser) await browser.close();
    await stop();
    if (proxy) await new Promise(resolve => proxy.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
