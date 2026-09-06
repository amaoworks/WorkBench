// Run only against a disposable workspace: this test changes module/layout state and creates tasks.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18086";
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(baseURL);
    await page.getByRole("heading", { name: "总览", exact: true }).waitFor();
    await page.getByRole("region", { name: "待办概览" }).waitFor();
    await page.getByRole("region", { name: "模拟行情" }).waitFor();
    await page.getByRole("button", { name: "配置总览", exact: true }).click();
    await page.getByRole("checkbox", { name: "待办概览", exact: true }).uncheck();
    await page.getByLabel("模拟行情尺寸").selectOption("large");
    await page.getByRole("button", { name: "模拟行情上移", exact: true }).click();
    await page.getByRole("button", { name: "保存布局", exact: true }).click();
    await page.getByRole("region", { name: "待办概览" }).waitFor({ state: "detached" });
    await page.reload();
    await page.getByRole("region", { name: "模拟行情" }).waitFor();
    assert.match(await page.getByRole("region", { name: "模拟行情" }).getAttribute("class"), /widget-large/);
    assert.equal(await page.getByRole("region", { name: "待办概览" }).count(), 0);
    await page.getByRole("link", { name: "待办", exact: true }).click();
    await page.getByRole("heading", { name: "待办", exact: true }).waitFor();
    // More than one page proves the UI consumes the backend cursor instead of silently truncating tasks.
    await page.evaluate(async () => {
      const { token } = await (await fetch("/api/auth/csrf")).json();
      for (let i = 0; i < 51; i++) {
        const response = await fetch("/api/modules/todo/tasks", { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify({ title: `分页验证 ${i}` }) });
        if (!response.ok) throw new Error(`create failed: ${response.status}`);
      }
    });
    await page.reload();
    await page.getByRole("button", { name: "加载更多", exact: true }).click();
    await page.getByText("已加载 51 项", { exact: false }).waitFor();
    await page.goto(`${baseURL}/settings?tab=modules`);
    const moduleSwitch = page.getByRole("switch", { name: "模拟行情模块", exact: true });
    await moduleSwitch.click();
    await page.waitForFunction(() => document.querySelector('[aria-label="模拟行情模块"]')?.getAttribute("aria-checked") === "false");
    assert.equal(await page.getByRole("link", { name: "模拟行情", exact: true }).count(), 0);
    await page.goto(baseURL);
    await page.getByText("总览暂时没有卡片", { exact: true }).waitFor();
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("switch", { name: "模拟行情模块", exact: true }).click();
    await page.getByRole("link", { name: "模拟行情", exact: true }).click();
    await page.getByRole("button", { name: "同步模拟行情", exact: true }).click();
    await page.getByText("DEMO-A", { exact: true }).waitFor();
    await page.getByRole("button", { name: "生成 AI 摘要", exact: true }).click();
    await page.getByText("AI 未启用，请在设置中配置；行情浏览与同步仍可使用", { exact: true }).waitFor();
    await page.goto(baseURL);
    await page.getByRole("button", { name: "配置总览", exact: true }).click();
    await page.getByRole("checkbox", { name: "待办概览", exact: true }).check();
    await page.getByLabel("待办概览尺寸").selectOption("small");
    await page.getByLabel("模拟行情尺寸").selectOption("medium");
    await page.getByRole("button", { name: "保存布局", exact: true }).click();
    await page.getByRole("region", { name: "待办概览" }).waitFor();
    await page.getByRole("button", { name: "关闭配置", exact: true }).click();
    assert.deepEqual(await page.locator(".dashboard-widget").evaluateAll((nodes) => nodes.map((node) => node.getAttribute("aria-label"))), ["模拟行情", "待办概览"]);
    await page.screenshot({ path: "/tmp/workbench-platform-dashboard.png", fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: "/tmp/workbench-platform-mobile.png", fullPage: true });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "mobile overflow");
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("switch", { name: "待办模块", exact: true }).waitFor();
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "settings mobile overflow");
    assert.deepEqual(errors, []);
    console.log("PASS: module toggles, hidden widgets, order/size persistence, pagination, mock sync, AI unavailable, mobile layout; no page errors");
  } finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
