// Starts its own disposable workspace and mock Wallos; never uses a live user's database.
const assert = require("node:assert/strict");
const http = require("node:http");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-wallos-"));
  let app, browser;
  const paymentDate = new Date().toISOString().slice(0, 10);
  const wallos = http.createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;
    assert.equal(req.url, "/wallos/api/subscriptions/get_subscriptions.php");
    assert.equal(req.method, "POST");
    assert.equal(new URLSearchParams(body).get("api_key"), "test-key");
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ success: true, subscriptions: [{ id: 1, name: "测试月付订阅", next_payment: paymentDate, inactive: 0 }] }));
  });
  try {
    await new Promise((resolve) => wallos.listen(0, "127.0.0.1", resolve));
    const baseURL = "http://127.0.0.1:18137";
    const env = { ...process.env, OPENAI_API_KEY: "", WORKBENCH_ALLOWED_HOSTS: "" };
    app = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-wallos-test", ["-data", join(directory, "data.db"), "-auth", "local", "-listen", "127.0.0.1:18137"], { env, stdio: "ignore" });
    let spawnError;
    app.on("error", (error) => { spawnError = error; });
    for (let i = 0; i < 100; i++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
      try { if ((await fetch(`${baseURL}/health/ready`)).ok) break; } catch { /* wait for startup */ }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("dialog", async (dialog) => { errors.push("Unexpected native dialog"); await dialog.dismiss(); });
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("button", { name: "待办设置", exact: true }).click();
    await page.getByLabel("Wallos 地址", { exact: true }).fill(`http://127.0.0.1:${wallos.address().port}/wallos`);
    await page.getByLabel("API Key", { exact: true }).fill("test-key");
    await page.getByLabel("启用自动同步", { exact: true }).check();
    await page.getByRole("button", { name: "保存配置", exact: true }).click();
    await page.getByText("Wallos 配置已保存", { exact: true }).waitFor();
    await page.getByRole("button", { name: "立即同步", exact: true }).click();
    await page.getByText("同步完成，新建 1 条提醒待办", { exact: true }).waitFor();
    assert.equal(await page.getByLabel("API Key", { exact: true }).inputValue(), "");
    await page.getByRole("button", { name: "立即同步", exact: true }).click();
    await page.getByText("同步完成，新建 0 条提醒待办", { exact: true }).waitFor();
    await page.getByRole("button", { name: "关闭模块设置", exact: true }).click();
    await page.goto(`${baseURL}/todo`);
    await page.getByText("Wallos续费提醒 - 测试月付订阅", { exact: true }).waitFor();
    assert.match(await page.locator(".task-row").innerText(), new RegExp(paymentDate));
    await page.getByRole("checkbox", { name: "标记为完成", exact: true }).click();
    await page.getByRole("checkbox", { name: "标记为未完成", exact: true }).waitFor();
    assert.equal(await page.getByText("需要订阅续费提醒？", { exact: false }).count(), 0);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 850 });
      await page.getByRole("button", { name: "删除待办", exact: true }).click();
      const confirmation = page.getByRole("alertdialog", { name: "删除待办", exact: true });
      await confirmation.waitFor();
      const box = await confirmation.boundingBox();
      assert(Math.abs(box.x + box.width / 2 - width / 2) < 2);
      assert(Math.abs(box.y + box.height / 2 - 425) < 2);
      assert(await page.getByRole("button", { name: "取消", exact: true }).evaluate(node => node === document.activeElement));
      await page.keyboard.press("Escape");
      await confirmation.waitFor({ state: "hidden" });
      assert.equal(await page.locator(".task-row").count(), 1);
      assert(await page.getByRole("button", { name: "删除待办", exact: true }).evaluate(node => node === document.activeElement));
    }
    await page.getByRole("button", { name: "删除待办", exact: true }).click();
    await page.getByRole("button", { name: "确认删除", exact: true }).click();
    await page.locator(".task-row").waitFor({ state: "detached" });
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("button", { name: "待办设置", exact: true }).click();
    assert.equal(await page.getByLabel("API Key", { exact: true }).inputValue(), "");
    await page.getByRole("button", { name: "立即同步", exact: true }).click();
    await page.getByText("同步完成，新建 1 条提醒待办", { exact: true }).waitFor();
    const recreated = await (await page.request.get(`${baseURL}/api/modules/todo/tasks`)).json();
    assert.equal(recreated.items.length, 1);
    assert(!recreated.items[0].completedAt);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 850 });
      const dialog = page.getByRole("dialog", { name: "待办设置", exact: true });
      const bounds = await dialog.boundingBox();
      assert(bounds.x >= 0 && bounds.x + bounds.width <= width);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `/tmp/wallos-settings-${width}.png` });
    }
    await page.getByRole("button", { name: "关闭模块设置", exact: true }).click();
    await page.getByRole("switch", { name: "待办模块", exact: true }).click();
    await page.waitForFunction(() => document.querySelector('[aria-label="待办模块"]')?.getAttribute("aria-checked") === "false");
    assert.equal((await page.request.get(`${baseURL}/api/modules/todo/wallos`)).status(), 503);
    await page.getByRole("button", { name: "待办设置", exact: true }).click();
    await page.getByText("启用待办模块后可以配置 Wallos 联动。停用模块期间自动同步也会暂停。", { exact: true }).waitFor();
    assert.deepEqual(errors, []);
    console.log("PASS: real API settings/save/sync, secret redaction, deduplication, generated task and completion, desktop/mobile settings, disabled module gate.");
  } finally {
    if (browser) await browser.close();
    if (app && app.exitCode === null && app.pid) { const stopped = new Promise((resolve) => app.once("exit", resolve)); app.kill("SIGTERM"); await stopped; }
    await new Promise((resolve) => wallos.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
