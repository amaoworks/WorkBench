// Temporary workspace; Schwab state, OAuth and the chart are browser fixtures.
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-reauthorization-"));
  const baseURL = "http://127.0.0.1:18139";
  let app, browser;
  try {
    app = spawn(process.env.WORKBENCH_BIN || join(__dirname, "../workbench"), ["-data", join(directory, "data.db"), "-auth", "local", "-listen", "127.0.0.1:18139"], {
      env: { ...process.env, OPENAI_API_KEY: "", WORKBENCH_ALLOWED_HOSTS: "" }, stdio: "ignore",
    });
    let spawnError;
    app.on("error", error => { spawnError = error; });
    for (let i = 0; i < 100; i++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
      try { if ((await fetch(`${baseURL}/health/ready`)).ok) break; } catch { /* startup */ }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    context.on("page", page => page.on("pageerror", error => errors.push(error.message)));
    const healthy = { appKey: "fixture", callbackUrl: "https://example.test/oauth/schwab", hasAppSecret: true, connected: true, reauthorizationRequired: false, lastError: "" };
    let state = { ...healthy }, loads = 0, authorizations = 0;
    await context.route("**/api/modules/investment/schwab", route => route.fulfill({ json: state }));
    await context.route("**/api/modules/investment/schwab/oauth/refresh", route => {
      assert.equal(route.request().method(), "POST");
      state = { ...healthy };
      return route.fulfill({ json: state });
    });
    await context.route("**/api/modules/investment/schwab/oauth/login", route => {
      authorizations++;
      return route.fulfill({ contentType: "text/html; charset=utf-8", body: '<a href="/oauth/schwab?code=fixture&state=fixture">确认模拟授权</a>' });
    });
    await context.route("**/oauth/schwab?**", route => {
      state = { ...healthy };
      return route.fulfill({ contentType: "text/html; charset=utf-8", body: '<meta http-equiv="refresh" content="0;url=/investment">' });
    });
    await context.route("**/investment/terminal?**", route => {
      loads++;
      return route.fulfill({ contentType: "text/html; charset=utf-8", body: "<p>模拟投资终端</p>" });
    });
    const page = await context.newPage();
    const iframe = page.locator("iframe[title='TradingView 投资终端']");
    const signalStateChanged = async () => {
      const chart = page.frames().find(frame => frame.url().includes("/investment/terminal"));
      assert(chart);
      await chart.evaluate(() => parent.postMessage({ type: "workbench.schwab.status_changed" }, location.origin));
    };
    await page.goto(`${baseURL}/investment`);
    await page.frameLocator("iframe").getByText("模拟投资终端").waitFor();

    state = { ...healthy, lastError: "暂时无法连接 Schwab，请稍后重试。" };
    await signalStateChanged();
    await page.getByRole("alert").filter({ hasText: "Schwab 连接出现问题" }).waitFor({ timeout: 5000 });
    assert.equal(await iframe.count(), 1);
    assert.equal(await page.getByRole("button", { name: "重新授权", exact: true }).count(), 0);
    await page.getByRole("button", { name: "重试连接", exact: true }).click();
    await page.getByRole("alert").filter({ hasText: "Schwab 连接出现问题" }).waitFor({ state: "hidden" });
    assert.equal(loads, 1, "temporary failures should preserve the chart");

    state = { ...healthy, connected: false, reauthorizationRequired: true, lastError: "Schwab 授权已失效，请重新授权。" };
    await signalStateChanged();
    await page.getByRole("heading", { name: "Schwab 授权已失效", exact: true }).waitFor({ timeout: 5000 });
    assert.equal(await iframe.count(), 0, "an invalid authorization must stop the retrying terminal");
    await page.reload();
    await page.getByRole("button", { name: "重新授权", exact: true }).waitFor();
    assert.equal(await iframe.count(), 0);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: "页面铺满", exact: true }).click();
    assert.equal(await page.getByRole("button", { name: "重新授权", exact: true }).isVisible(), true);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    await page.screenshot({ path: join(tmpdir(), "workbench-schwab-reauthorization-mobile.png"), fullPage: true });
    await page.getByRole("button", { name: "返回工作台", exact: true }).click();

    await page.getByRole("link", { name: "设置", exact: true }).click();
    await page.getByRole("button", { name: "投资设置", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("alert").filter({ hasText: "Schwab 授权已失效" }).waitFor();
    assert.equal(await dialog.getByRole("button", { name: "重新授权", exact: true }).isEnabled(), true);
    assert.equal(await dialog.getByRole("button", { name: "刷新令牌", exact: true }).isDisabled(), true);
    assert.equal(await dialog.getByText(/^已连接/).count(), 0);
    await page.getByRole("button", { name: "关闭模块设置", exact: true }).click();

    await page.getByRole("link", { name: "总览", exact: true }).click();
    await page.getByText("Schwab 授权已失效", { exact: true }).waitFor();
    await page.getByRole("button", { name: "重新授权", exact: true }).click();
    await page.waitForURL(`${baseURL}/api/modules/investment/schwab/oauth/login`);
    assert.equal(authorizations, 1);
    await page.getByRole("link", { name: "确认模拟授权", exact: true }).click();
    await page.waitForURL(`${baseURL}/investment`);
    await page.frameLocator("iframe").getByText("模拟投资终端").waitFor();
    assert.equal(authorizations, 1);
    assert.equal(await page.getByRole("button", { name: "重新授权", exact: true }).count(), 0);
    assert.deepEqual(errors, []);
    console.log("PASS: transient retry, immediate authorization failure, reload persistence, mobile/expanded prompt, settings and dashboard actions, OAuth return and terminal recovery; mocked accounts only.");
  } finally {
    await browser?.close();
    if (app && app.exitCode === null && app.pid) {
      const stopped = new Promise(resolve => app.once("exit", resolve));
      app.kill("SIGTERM");
      await stopped;
    }
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
