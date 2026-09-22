// Disposable workspace, fictional credentials and an intercepted test-send API.
// No broker connection or real Telegram message is made by this UI regression.
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-monitor-"));
  const listener = createServer();
  await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const baseURL = `http://127.0.0.1:${port}`;
  const app = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-monitor-e2e-bin", ["-data", join(directory, "data.db"), "-auth", "local", "-listen", `127.0.0.1:${port}`], {
    env: { ...process.env, OPENAI_API_KEY: "", WORKBENCH_PUBLIC_URL: "", WORKBENCH_ALLOWED_HOSTS: "", WORKBENCH_FUTU_CONFIG_DIR: "", WORKBENCH_FUTU_RUNTIME_DIR: join(directory, "futu-runtime") }, stdio: "ignore"
  });
  let browser, spawnError;
  app.on("error", error => { spawnError = error; });
  try {
    for (let i = 0; ; i++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error("Temporary Workbench exited");
      if (await fetch(`${baseURL}/health/ready`).then(r => r.ok).catch(() => false)) break;
      if (i > 100) throw new Error("Temporary Workbench did not start");
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(`${baseURL}/settings?tab=notifications`);
    await page.getByLabel(/^Bot Token/).fill("123:browser-fixture-token");
    await page.getByLabel("Chat ID", { exact: true }).fill("12345");
    await page.getByLabel("启用 Telegram 投资预警", { exact: true }).check();
    const saved = page.waitForResponse(r => r.url().endsWith("/api/settings/telegram") && r.request().method() === "PUT");
    await page.getByRole("button", { name: "保存配置", exact: true }).click();
    assert.equal((await saved).status(), 200);
    await page.getByText("Telegram 配置已保存", { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('input[type="password"][maxlength="512"]')?.value === "");
    await page.getByLabel("Chat ID", { exact: true }).fill("54321");
    await page.getByRole("tab", { name: "AI 配置", exact: true }).click();
    await page.getByRole("tab", { name: "通知推送", exact: true }).click();
    assert.equal(await page.getByLabel("Chat ID", { exact: true }).inputValue(), "54321");
    await page.reload();
    assert.equal(await page.getByLabel("Chat ID", { exact: true }).inputValue(), "12345");
    assert.equal(await page.getByLabel(/^Bot Token/).inputValue(), "");
    const settings = await (await page.request.get(`${baseURL}/api/settings/telegram`)).json();
    assert.equal(settings.hasBotToken, true);
    assert.equal(settings.botToken, undefined);
    let tests = 0;
    await page.route("**/api/settings/telegram/test", route => { tests++; return route.fulfill({ status: 200, contentType: "application/json", body: '{"ok":true}' }); });
    await page.getByRole("button", { name: "发送测试消息", exact: true }).click();
    await page.getByText("测试消息已发送，请在 Telegram 中查看。", { exact: true }).waitFor();
    assert.equal(tests, 1);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/tmp/workbench-telegram-${width}.png`, fullPage: true, animations: "disabled" });
    }
    await page.goto(`${baseURL}/investment?tab=monitor`);
    await page.getByRole("button", { name: "添加规则", exact: true }).click();
    await page.getByLabel("标的代码", { exact: true }).fill("AAPL");
    await page.getByLabel("阈值 %", { exact: true }).fill("3");
    await page.getByRole("button", { name: "保存规则", exact: true }).click();
    await page.getByRole("heading", { name: "AAPL 上涨达到 3%", exact: false }).waitFor();
    let monitor = await (await page.request.get(`${baseURL}/api/modules/investment/monitor`)).json();
    assert.equal(monitor.items.length, 1);
    assert.equal(monitor.items[0].enabled, true);
    await page.getByRole("button", { name: "编辑", exact: true }).click();
    await page.getByLabel("涨跌方向", { exact: true }).selectOption("down");
    await page.getByLabel("阈值 %", { exact: true }).fill("5");
    await page.getByRole("button", { name: "保存规则", exact: true }).click();
    await page.getByRole("heading", { name: "AAPL 下跌达到 5%", exact: false }).waitFor();
    await page.getByRole("button", { name: "暂停", exact: true }).click();
    await page.getByRole("button", { name: "启用", exact: true }).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "启用", exact: true }).waitFor();
    monitor = await (await page.request.get(`${baseURL}/api/modules/investment/monitor`)).json();
    assert.equal(monitor.items[0].thresholdPercent, 5);
    assert.equal(monitor.items[0].enabled, false);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/tmp/workbench-price-monitor-${width}.png`, fullPage: true, animations: "disabled" });
    }
    await page.getByRole("button", { name: "删除", exact: true }).click();
    await page.getByRole("alertdialog", { name: "删除监控规则", exact: true }).waitFor();
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "删除", exact: true }).click();
    await page.getByRole("button", { name: "确认删除", exact: true }).click();
    await page.getByText("尚无监控标的", { exact: true }).waitFor();
    assert.deepEqual(errors, []);
    console.log("PASS: Telegram settings/secret redaction/test UI, draft retention, monitor create/edit/pause/persistence/delete, desktop/mobile layout; no real outbound messages.");
  } finally {
    if (browser) await browser.close();
    if (app.pid && app.exitCode === null) { const stopped = new Promise(resolve => app.once("exit", resolve)); app.kill("SIGTERM"); await stopped; }
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
