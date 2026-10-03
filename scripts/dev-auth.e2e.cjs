// Disposable HTTPS -> Vite -> Go login regression. Uses temporary credentials and data.
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
    assert.deepEqual(errors, []);
    await context.close();
    console.log("PASS: HTTPS -> Vite -> Go reproduces 403 without public URL; configured origin supports login, Secure cookies, first password change, news navigation and persisted browser session.");
  } finally {
    if (browser) await browser.close();
    await stop();
    if (proxy) await new Promise(resolve => proxy.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
