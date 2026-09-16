// Uses a temporary workspace and fictional credentials; no real OpenD login.
const assert = require("node:assert/strict");
const { mkdtemp, rm, readFile, writeFile } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const managed = process.env.WORKBENCH_TEST_MANAGED !== "false";
  const directory = await mkdtemp(join(tmpdir(), "workbench-futu-e2e-"));
  const listener = createServer();
  await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const baseURL = `http://127.0.0.1:${port}`;
  const fixtureBinary = join(directory, "fixture-opend");
  await writeFile(fixtureBinary, '#!/bin/sh\nprintf "%s" "$$" > "$HOME/../fixture.pid"\nexec /bin/sleep 120\n', { mode: 0o700 });
  await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
  const openDPort = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const server = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-futu-e2e-bin", ["-listen", `127.0.0.1:${port}`, "-auth", "local", "-data", join(directory, "data.db")], {
    env: { ...process.env, OPENAI_API_KEY: "", WORKBENCH_PUBLIC_URL: "", WORKBENCH_ALLOWED_HOSTS: "", WORKBENCH_FUTU_CONFIG_DIR: managed ? join(directory, "futu") : "", WORKBENCH_FUTU_RUNTIME_DIR: join(directory, "runtime"), WORKBENCH_FUTU_OPEND_BINARY: fixtureBinary, WORKBENCH_FUTU_OPEND_ADDRESS: `127.0.0.1:${openDPort}`, WORKBENCH_FUTU_ALLOW_NON_LOCAL: "false" },
    stdio: "ignore"
  });
  let browser;
  try {
    for (let attempt = 0; ; attempt++) {
      if (server.exitCode !== null || server.signalCode !== null) throw new Error("Test server exited");
      if (await fetch(baseURL + "/health/ready").then(r => r.ok).catch(() => false)) break;
      if (attempt > 100) throw new Error("Test server did not start");
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(baseURL + "/settings?tab=modules");
    await page.getByRole("heading", { name: "业务模块", exact: true }).waitFor();
    assert.equal(await page.getByText("添加外部模块", { exact: true }).count(), 0);
    const { token } = await (await page.request.get(baseURL + "/api/auth/csrf")).json();
    const put = (path, data) => page.request.put(baseURL + path, { data, headers: { "X-CSRF-Token": token, Origin: baseURL } });
    assert.equal((await put("/api/modules/investment/overnight", { enabled: true })).status(), 409);
    const noAttach = await page.request.post(baseURL + "/api/modules/external", { data: { baseUrl: "http://127.0.0.1:9", serviceToken: "fixture" }, headers: { "X-CSRF-Token": token, Origin: baseURL } });
    assert.equal(noAttach.status(), 404);
    await page.getByRole("button", { name: "投资设置", exact: true }).click();
    assert.equal(await page.getByRole("checkbox", { name: "启用夜盘行情", exact: true }).isDisabled(), true);
    await page.getByRole("heading", { name: "富途牛牛", exact: true }).waitFor();
    assert.equal(await page.getByText("为投资模块提供行情，启用后可单独开启下方的夜盘功能。", { exact: true }).count(), 0);
    await page.getByLabel("富途牛牛账号", { exact: true }).fill("fixture@example.invalid");
    await page.getByLabel(/^登录密码/).fill("futu-browser-fixture-password");
    assert.equal(await page.getByRole("tab", { name: "富途牛牛", exact: true }).count(), 0);
    assert.equal(await page.getByLabel("OpenD 主机", { exact: true }).count(), 0);
    assert.equal(await page.getByLabel("API 端口", { exact: true }).count(), 0);
    assert.equal(await page.getByRole("checkbox", { name: "允许非本机地址" }).count(), 0);
    const save = async () => {
      const response = page.waitForResponse(r => r.url() === baseURL + "/api/modules/investment/futu" && r.request().method() === "PUT");
      await page.getByRole("button", { name: "保存富途牛牛配置", exact: true }).click();
      assert.equal((await response).status(), 200);
      await page.getByRole("button", { name: "保存富途牛牛配置", exact: true }).waitFor();
      await page.waitForFunction(() => [...document.querySelectorAll("button")].some(b => b.textContent === "保存富途牛牛配置" && b.disabled));
    };
    await save();
    assert.equal(await page.getByLabel(/^登录密码/).inputValue(), "");
    const settings = await (await page.request.get(baseURL + "/api/modules/investment/futu")).json();
    assert.equal(settings.hasPassword, true);
    assert.equal(settings.password, undefined);
    assert.equal(settings.passwordMD5, undefined);
    assert.equal(settings.host, undefined);
    assert.equal(settings.port, undefined);
    if (managed) {
    const login = JSON.parse(await readFile(join(directory, "futu", "login.json"), "utf8"));
    assert.equal(login.account, "fixture@example.invalid");
    assert.match(login.passwordMD5, /^[0-9a-f]{32}$/);
    }
    await page.getByRole("checkbox", { name: "启用富途牛牛", exact: true }).check();
    await save();
    if (!managed) {
      await page.getByText("富途牛牛服务已启动，正在连接行情…", { exact: true }).waitFor();
      const pid = Number(await readFile(join(directory, "runtime", "fixture.pid"), "utf8"));
      process.kill(pid, 0);
    }
    await page.getByRole("checkbox", { name: "启用夜盘行情", exact: true }).check();
    const savedNight = page.waitForResponse(r => r.url().endsWith("/investment/overnight") && r.request().method() === "PUT");
    await page.getByRole("button", { name: "保存夜盘设置", exact: true }).click();
    assert.equal((await savedNight).status(), 200);
    await page.getByRole("checkbox", { name: "启用富途牛牛", exact: true }).uncheck();
    const previousPID = managed ? null : Number(await readFile(join(directory, "runtime", "fixture.pid"), "utf8"));
    await save();
    if (previousPID) assert.throws(() => process.kill(previousPID, 0), /ESRCH/);
    assert.equal((await (await page.request.get(baseURL + "/api/modules/investment/overnight")).json()).enabled, false);
    await page.getByRole("checkbox", { name: "启用富途牛牛", exact: true }).check();
    await save();
    assert.equal((await (await page.request.get(baseURL + "/api/modules/investment/overnight")).json()).enabled, false);
    await put("/api/modules/investment/enabled", { enabled: false });
    assert.equal((await page.request.get(baseURL + "/api/modules/investment/futu")).status(), 503);
    await put("/api/modules/investment/enabled", { enabled: true });
    await page.reload();
    await page.getByRole("button", { name: "投资设置", exact: true }).click();
    await page.getByLabel("富途牛牛账号", { exact: true }).waitFor();
    assert.equal(await page.getByLabel("富途牛牛账号", { exact: true }).inputValue(), "fixture@example.invalid");
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
      await page.getByLabel("富途牛牛账号", { exact: true }).scrollIntoViewIfNeeded();
      await page.screenshot({ path: `/tmp/workbench-investment-futu-${managed ? "managed" : "native"}-${width}.png`, fullPage: true });
    }
    assert.deepEqual(errors, []);
    if (!managed) {
      const pid = Number(await readFile(join(directory, "runtime", "fixture.pid"), "utf8"));
      process.kill(pid, 0);
      server.kill("SIGKILL");
      await new Promise(resolve => server.once("exit", resolve));
      for (let attempt = 0; ; attempt++) {
        const stat = await readFile(`/proc/${pid}/stat`, "utf8").catch(() => "");
        if (!stat || stat.slice(stat.lastIndexOf(")") + 2).startsWith("Z ")) break;
        if (attempt > 50) throw new Error("OpenD survived the Workbench parent process");
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    }
    console.log(`Investment富途牛牛settings browser checks passed (managed=${managed})`);
  } finally {
    await browser?.close();
    server.kill("SIGTERM");
    await new Promise(resolve => { if (server.exitCode !== null || server.signalCode !== null) resolve(); else server.once("exit", resolve); });
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
