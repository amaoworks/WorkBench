// Run only against a disposable workspace: this test changes module/layout state and creates tasks.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18086";

// Shared Select is a Radix listbox: open the trigger, then pick the option by its visible label.
async function choose(page, trigger, option) {
  await trigger.click();
  await page.getByRole("option", { name: option, exact: true }).click();
  await page.getByRole("listbox").waitFor({ state: "detached" });
}
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(baseURL);
    await page.getByRole("heading", { name: "今天，一目了然", exact: true }).waitFor();
    // Cards are named by their widget definitions; empty cards stay hidden, 资产快照 always renders.
    await page.getByRole("region", { name: "资产快照", exact: true }).waitFor();
    await page.getByRole("button", { name: "配置卡片", exact: true }).click();
    await page.getByRole("checkbox", { name: "提醒与异动", exact: true }).uncheck();
    await page.getByRole("checkbox", { name: "通知", exact: true }).check();
    await choose(page, page.getByLabel("资产快照尺寸"), "大");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    await page.getByText("总览配置已保存", { exact: true }).waitFor();
    await page.reload();
    await page.getByRole("region", { name: "资产快照", exact: true }).waitFor();
    assert.match(await page.getByRole("region", { name: "资产快照", exact: true }).getAttribute("class"), /widget-large/);
    assert.equal(await page.getByRole("region", { name: "提醒与异动", exact: true }).count(), 0);
    // The platform notifications card is a 今日概况 tile only, never a full card.
    assert.equal(await page.getByRole("region", { name: "通知", exact: true }).count(), 0);
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
    await page.locator(".todo-groups .task-row").nth(50).waitFor();
    assert.equal(await page.locator(".todo-groups .task-row").count(), 51, "加载更多 must append the second page of tasks");
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("button", { name: "投资设置", exact: true }).click();
    const investmentSettings = page.getByRole("dialog", { name: "投资设置", exact: true });
    const schwabForm = investmentSettings.locator("form").filter({ has: page.getByLabel("App Key", { exact: true }) });
    assert.equal(await page.getByLabel("OAuth 回调地址", { exact: true }).inputValue(), baseURL.startsWith("https:") ? `${baseURL}/oauth/schwab` : "");
    await page.getByLabel("App Key", { exact: true }).fill("browser-test-key");
    await page.getByLabel("App Secret", { exact: true }).fill("browser-test-secret");
    await page.getByLabel("OAuth 回调地址", { exact: true }).fill(`${baseURL.replace(/^https:/, "http:")}/oauth/schwab`);
    const invalidCallback = page.waitForResponse(response => response.url() === `${baseURL}/api/modules/investment/schwab` && response.request().method() === "PUT");
    await schwabForm.getByRole("button", { name: "保存", exact: true }).click();
    assert.equal((await invalidCallback).status(), 400);
    await page.getByLabel("OAuth 回调地址", { exact: true }).fill(`${baseURL.replace(/^http:/, "https:")}/oauth/schwab`);
    const saved = page.waitForResponse(response => response.url() === `${baseURL}/api/modules/investment/schwab` && response.request().method() === "PUT");
    await schwabForm.getByRole("button", { name: "保存", exact: true }).click();
    assert.equal((await saved).status(), 200);
    await page.getByText("Schwab 设置已保存", { exact: true }).waitFor();
    await investmentSettings.getByRole("status").filter({ hasText: "未连接 Schwab" }).waitFor();
    await investmentSettings.getByText("请完成 OAuth 授权。", { exact: true }).waitFor();
    assert.equal(await page.getByLabel("App Secret", { exact: true }).inputValue(), "");
    const config = await (await page.request.get(`${baseURL}/api/modules/investment/schwab`)).json();
    assert.equal(config.hasAppSecret, true);
    assert.equal(config.connected, false);
    assert(!JSON.stringify(config).includes("browser-test-secret"));
    await page.getByLabel("App Key", { exact: true }).fill("another-browser-key");
    const rejected = page.waitForResponse(response => response.url() === `${baseURL}/api/modules/investment/schwab` && response.request().method() === "PUT");
    await schwabForm.getByRole("button", { name: "保存", exact: true }).click();
    assert.equal((await rejected).status(), 400);
    await page.getByText("更换 App Key 时请重新填写 App Secret", { exact: true }).waitFor();
    assert(await page.getByRole("button", { name: "登录 Schwab", exact: true }).isDisabled());
    // The rejected edit is still unsaved, so closing asks before discarding it.
    await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
    const discard = page.getByRole("alertdialog", { name: "放弃未保存的修改？", exact: true });
    await discard.getByRole("button", { name: "放弃修改", exact: true }).click();
    await investmentSettings.waitFor({ state: "detached" });
    const moduleSwitch = page.getByRole("switch", { name: "投资", exact: true });
    await moduleSwitch.click();
    await page.waitForFunction(() => document.querySelector('[aria-label="投资"]')?.getAttribute("aria-checked") === "false");
    assert.equal(await page.getByRole("link", { name: "投资", exact: true }).count(), 0);
    assert.equal((await page.request.get(`${baseURL}/api/modules/investment/schwab`)).status(), 503);
    assert.equal((await page.request.get(`${baseURL}/investment/terminal`)).status(), 503);
    await page.goto(baseURL);
    await page.getByRole("heading", { name: "今天，一目了然", exact: true }).waitFor();
    await page.waitForLoadState("networkidle");
    assert.equal(await page.getByRole("region", { name: "资产快照", exact: true }).count(), 0);
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("switch", { name: "投资", exact: true }).click();
    await page.getByRole("link", { name: "投资", exact: true }).click();
    await page.getByRole("region", { name: "投资终端", exact: true }).waitFor();
    await page.getByRole("heading", { name: "未连接 Schwab", exact: true }).waitFor();
    assert.equal(await page.getByRole("button", { name: "生成 AI 摘要", exact: true }).count(), 0);
    await page.goto(baseURL);
    await page.getByRole("button", { name: "配置卡片", exact: true }).click();
    await page.getByRole("checkbox", { name: "提醒与异动", exact: true }).check();
    await page.getByRole("checkbox", { name: "通知", exact: true }).uncheck();
    await choose(page, page.getByLabel("资产快照尺寸"), "中");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    await page.getByText("总览配置已保存", { exact: true }).last().waitFor();
    await page.getByRole("button", { name: "完成", exact: true }).click();
    await page.waitForFunction(() => /widget-medium/.test(document.querySelector('[aria-label="资产快照"]')?.className ?? ""));
    await page.screenshot({ path: "/tmp/workbench-platform-dashboard.png", fullPage: true, animations: "disabled" });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: "打开导航", exact: true }).waitFor();
    await page.screenshot({ path: "/tmp/workbench-platform-mobile.png", fullPage: true, animations: "disabled" });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "mobile overflow");
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("switch", { name: "待办", exact: true }).waitFor();
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "settings mobile overflow");
    await page.getByRole("button", { name: "投资设置", exact: true }).click();
    await page.getByLabel("App Key", { exact: true }).waitFor();
    assert.equal(await page.getByLabel("App Key", { exact: true }).inputValue(), "browser-test-key");
    assert.equal(await page.getByLabel("App Secret", { exact: true }).inputValue(), "");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "Schwab settings mobile overflow");
    await page.screenshot({ path: "/tmp/workbench-schwab-settings-mobile.png", fullPage: true, animations: "disabled" });
    assert.deepEqual(errors, []);
    console.log("PASS: module toggles and API gates, layout persistence, pagination, Schwab settings/secret redaction/credential validation, no demo controls, mobile layout; no page errors");
  } finally { await browser.close(); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
