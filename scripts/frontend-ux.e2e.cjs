// Disposable workspace. External-module discovery, the investment terminal and the one intentional
// Todo deletion failure are browser fixtures; no feed refresh, notification or broker call is made.
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");


// Shared Select is a Radix listbox: open the trigger, then pick the option by its visible label.
async function choose(page, trigger, option) {
  await trigger.click();
  await page.getByRole("option", { name: option, exact: true }).click();
  await page.getByRole("listbox").waitFor({ state: "detached" });
}
async function unusedPort() {
  const listener = createServer();
  await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}

async function api(page, path, method, body) {
  return page.evaluate(async ({ path, method, body }) => {
    const { token } = await (await fetch("/api/auth/csrf")).json();
    const response = await fetch(path, {
      method,
      headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    if (!response.ok) throw new Error(`${method} ${path}: ${response.status} ${await response.text()}`);
    return response.status === 204 ? null : response.json();
  }, { path, method, body });
}

async function expectFocus(locator, message) {
  await locator.waitFor();
  await locator.page().waitForFunction((node) => node === document.activeElement, await locator.elementHandle());
  assert(await locator.evaluate((node) => node === document.activeElement), message);
}

async function expectNoOverflow(page, label) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${label} horizontally overflows`);
}

async function visitWithoutOverflow(page, url, label, ready) {
  await page.goto(url);
  await ready();
  await expectNoOverflow(page, label);
}

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-frontend-ux-"));
  const port = await unusedPort();
  const baseURL = `http://127.0.0.1:${port}`;
  const app = spawn(process.env.WORKBENCH_BIN || join(__dirname, "../workbench"), [
    "-data", join(directory, "data.db"), "-auth", "local", "-listen", `127.0.0.1:${port}`
  ], {
    env: {
      ...process.env,
      OPENAI_API_KEY: "",
      WORKBENCH_PUBLIC_URL: "",
      WORKBENCH_ALLOWED_HOSTS: "",
      WORKBENCH_FUTU_CONFIG_DIR: "",
      WORKBENCH_FUTU_RUNTIME_DIR: join(directory, "futu-runtime")
    },
    stdio: "ignore"
  });
  let browser;
  let spawnError;
  app.on("error", (error) => { spawnError = error; });
  try {
    for (let attempt = 0; ; attempt++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
      if (await fetch(`${baseURL}/health/ready`).then((response) => response.ok).catch(() => false)) break;
      if (attempt > 100) throw new Error("Temporary Workbench did not become ready");
      await new Promise((resolve) => setTimeout(resolve, 100));
    }

    browser = await chromium.launch({
      headless: true,
      executablePath: process.env.CHROMIUM_PATH || undefined,
      args: ["--no-sandbox"]
    });
    const context = await browser.newContext({ viewport: { width: 390, height: 800 }, hasTouch: true });
    const page = await context.newPage();
    const errors = [];
    let intentionalTodoFailureActive = false;
    page.on("pageerror", (error) => errors.push(`pageerror: ${error.message}`));
    page.on("console", (message) => {
      if (message.type() !== "error") return;
      if (intentionalTodoFailureActive && /Failed to load resource: the server responded with a status of 500/.test(message.text())) return;
      errors.push(`console: ${message.text()}`);
    });
    await page.route("**/investment/terminal**", (route) => route.fulfill({
      contentType: "text/html; charset=utf-8",
      body: "<!doctype html><title>模拟投资终端</title><p>模拟投资终端</p>"
    }));

    await page.goto(`${baseURL}/todo`);
    const titleInput = page.getByLabel("待办标题", { exact: true });
    await titleInput.fill("触屏删除验证");
    await titleInput.press("Enter");
    const todoDelete = page.getByRole("button", { name: "删除 触屏删除验证", exact: true });
    await todoDelete.waitFor();
    assert.equal(await todoDelete.isVisible(), true, "Todo delete must stay visible in a touch context");
    assert.equal(await todoDelete.evaluate((node) => getComputedStyle(node).opacity), "1", "Todo delete opacity must be 1");

    await todoDelete.click();
    const todoDialog = page.getByRole("alertdialog", { name: "删除待办", exact: true });
    await todoDialog.waitFor();
    await todoDialog.getByRole("button", { name: "取消", exact: true }).click();
    await expectFocus(todoDelete, "canceling Todo deletion must restore the trigger focus");

    let failedDelete = false;
    await page.route("**/api/modules/todo/tasks/*", async (route) => {
      if (route.request().method() === "DELETE" && !failedDelete) {
        failedDelete = true;
        await route.fulfill({ status: 500, contentType: "application/json", body: '{"code":"fixture","message":"模拟删除失败"}' });
        return;
      }
      await route.continue();
    });
    await todoDelete.click();
    intentionalTodoFailureActive = true;
    await todoDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    await page.getByText("模拟删除失败", { exact: true }).waitFor();
    intentionalTodoFailureActive = false;
    assert.equal(await todoDialog.isVisible(), true, "a failed Todo deletion must retain its dialog");
    await todoDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    await todoDialog.waitFor({ state: "hidden" });
    await expectFocus(titleInput, "successful Todo deletion must move focus to the stable title input");
    assert.equal(await page.locator('input[type="datetime-local"]').count(), 0, "Todo must not expose the browser-locale datetime input");
    const deadlineTrigger = page.getByRole("button", { name: "截止时间", exact: true });
    await deadlineTrigger.click();
    const deadlineDialog = page.getByRole("dialog", { name: "截止时间", exact: true });
    await deadlineDialog.getByRole("button", { name: /^\d{4} 年 \d{1,2} 月 \d{1,2} 日$/ }).first().waitFor();
    await deadlineDialog.getByLabel("小时", { exact: true }).waitFor();
    await deadlineDialog.getByLabel("分钟", { exact: true }).waitFor();
    const now = new Date();
    for (let step = 0; step < 12 - now.getMonth(); step++) {
      await deadlineDialog.getByRole("button", { name: "下个月", exact: true }).click();
    }
    await deadlineDialog.getByText(`${now.getFullYear() + 1} 年 1 月`, { exact: true }).waitFor();
    await deadlineDialog.getByRole("button", { name: `${now.getFullYear() + 1} 年 1 月 5 日`, exact: true }).waitFor();
    await deadlineDialog.getByRole("button", { name: "取消", exact: true }).click();
    await deadlineDialog.waitFor({ state: "hidden" });
    await expectFocus(deadlineTrigger, "closing the deadline dialog must restore its trigger focus");
    await deadlineTrigger.click();
    const dayLabel = `${now.getFullYear()} 年 ${now.getMonth() + 1} 月 15 日`;
    await deadlineDialog.getByRole("button", { name: dayLabel, exact: true }).click();
    await choose(page, deadlineDialog.getByLabel("小时", { exact: true }), "13");
    await choose(page, deadlineDialog.getByLabel("分钟", { exact: true }), "45");
    await deadlineDialog.getByRole("button", { name: "确定", exact: true }).click();
    await page.getByText(/15日 13:45/).waitFor();
    const deadlineValueId = await deadlineTrigger.getAttribute("aria-describedby");
    assert(deadlineValueId, "the deadline trigger must describe its displayed value");
    assert.match(await page.locator(`[id="${deadlineValueId}"]`).innerText(), new RegExp(`${now.getFullYear()}.*15.*13:45`));
    await deadlineTrigger.click();
    await deadlineDialog.getByRole("button", { name: "清除", exact: true }).click();
    await page.getByText("截止时间", { exact: true }).waitFor();
    await deadlineTrigger.click();
    await deadlineDialog.getByRole("button", { name: dayLabel, exact: true }).click();
    await choose(page, deadlineDialog.getByLabel("小时", { exact: true }), "13");
    await choose(page, deadlineDialog.getByLabel("分钟", { exact: true }), "45");
    await deadlineDialog.getByRole("button", { name: "确定", exact: true }).click();
    await titleInput.fill("日期持久化验证");
    await titleInput.press("Enter");
    await page.locator(".todo-groups").getByText("日期持久化验证", { exact: true }).waitFor();
    const datedTask = await page.evaluate(async () => {
      const response = await fetch("/api/modules/todo/tasks?limit=50");
      const data = await response.json();
      return data.items.find((item) => item.title === "日期持久化验证");
    });
    const expectedDueAt = new Date(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-15T13:45`).toISOString();
    assert.equal(Date.parse(datedTask.dueAt), Date.parse(expectedDueAt), "Todo deadline must persist the selected local date and time");

    for (let index = 1; index <= 5; index++) {
      await titleInput.fill(`Toast 上限 ${index}`);
      await titleInput.press("Enter");
      await page.getByText(`Toast 上限 ${index}`, { exact: true }).waitFor();
    }
    const toaster = page.locator("[data-sonner-toaster]");
    assert.equal(await toaster.getAttribute("data-y-position"), "top");
    assert.equal(await toaster.getAttribute("data-x-position"), "right");
    const visibleToasts = page.locator('[data-sonner-toast][data-visible="true"]');
    assert((await visibleToasts.count()) <= 3, "at most three toasts may be visible");
    assert((await visibleToasts.count()) > 0, "the toast limit check requires a visible toast");
    await expectNoOverflow(page, "Todo at 390px");

    await page.goto(`${baseURL}/news`);
    for (const label of ["文章", "订阅源", "关注规则", "简报"]) {
      await page.getByRole("button", { name: label, exact: true }).waitFor();
    }
    await api(page, "/api/modules/news/sources", "POST", {
      name: "UX 订阅源", kind: "rss", url: "https://fixture.invalid/feed.xml", query: "", topic: "测试",
      enabled: false, intervalMinutes: 60
    });
    await page.goto(`${baseURL}/news?tab=sources`);
    await page.getByRole("button", { name: "添加订阅源", exact: true }).waitFor();
    const sourceDelete = page.getByRole("button", { name: "删除 UX 订阅源", exact: true });
    await sourceDelete.click();
    const sourceDialog = page.getByRole("alertdialog", { name: "删除订阅源", exact: true });
    await sourceDialog.getByRole("button", { name: "取消", exact: true }).click();
    await expectFocus(sourceDelete, "canceling source deletion must restore the trigger focus");
    await sourceDelete.click();
    await sourceDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    const addSource = page.getByRole("button", { name: "添加订阅源", exact: true });
    await expectFocus(addSource, "successful source deletion must focus the add button");

    await api(page, "/api/modules/news/rules", "POST", {
      name: "UX 关注规则", includeKeywords: ["测试"], excludeKeywords: [], sourceIds: [], topic: "", enabled: true
    });
    await page.goto(`${baseURL}/news?tab=rules`);
    const ruleDelete = page.getByRole("button", { name: "删除 UX 关注规则", exact: true });
    await ruleDelete.click();
    const ruleDialog = page.getByRole("alertdialog", { name: "删除关注规则", exact: true });
    await ruleDialog.getByRole("button", { name: "取消", exact: true }).click();
    await expectFocus(ruleDelete, "canceling rule deletion must restore the trigger focus");
    await ruleDelete.click();
    await ruleDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    await expectFocus(page.getByRole("button", { name: "添加规则", exact: true }), "successful rule deletion must focus the add button");
    await expectNoOverflow(page, "News at 390px");

    await api(page, "/api/modules/investment/monitor/rules", "POST", {
      symbol: "AAPL", direction: "up", thresholdPercent: 3, enabled: true
    });
    await page.goto(`${baseURL}/investment/monitor`);
    const monitorDelete = page.getByRole("button", { name: "删除 AAPL", exact: true });
    await page.getByRole("button", { name: "编辑", exact: true }).click();
    assert(await monitorDelete.isDisabled(), "monitor deletion must stay disabled while a rule is being edited");
    await page.getByRole("button", { name: "取消", exact: true }).click();
    await monitorDelete.click();
    const monitorDialog = page.getByRole("alertdialog", { name: "删除监控规则", exact: true });
    await monitorDialog.getByRole("button", { name: "取消", exact: true }).click();
    await expectFocus(monitorDelete, "canceling monitor deletion must restore the trigger focus");
    await monitorDelete.click();
    await monitorDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    await expectFocus(page.getByRole("button", { name: "添加规则", exact: true }), "successful monitor deletion must focus the add button");
    await expectNoOverflow(page, "Investment monitor at 390px");

    let externalPresent = true;
    let externalDeletes = 0;
    const externalModule = {
      id: "ux_external", name: "UX 外部业务", version: "1.0.0", icon: "box", navigation: [], enabled: false,
      hasSettings: false, observedEnabled: false, health: "healthy", pending: false, lastError: "", lastCheckedAt: null,
      kind: "external", protocolVersion: 1, pages: [], baseUrl: "http://127.0.0.1:9", allowNonLocal: false,
      hasServiceToken: true, connectionNote: "浏览器夹具", generation: 1, observedGeneration: 1
    };
    await page.route(/\/api\/modules$/, (route) => route.fulfill({ json: { items: externalPresent ? [externalModule] : [] } }));
    await page.route("**/api/modules/external/ux_external", async (route) => {
      assert.equal(route.request().method(), "DELETE");
      externalDeletes++;
      externalPresent = false;
      await route.fulfill({ status: 204, body: "" });
    });
    await page.goto(`${baseURL}/settings?tab=modules`);
    await page.getByRole("heading", { name: "业务", exact: true }).waitFor();
    const detach = page.getByRole("button", { name: "解除接入", exact: true });
    await detach.click();
    const moduleDialog = page.getByRole("alertdialog", { name: "解除外部业务", exact: true });
    await moduleDialog.getByRole("button", { name: "取消", exact: true }).click();
    await expectFocus(detach, "canceling external-business deletion must restore the trigger focus");
    await detach.click();
    await moduleDialog.getByRole("button", { name: "确认删除", exact: true }).click();
    await expectFocus(page.getByRole("heading", { name: "业务", exact: true }), "successful external-business deletion must focus the section heading");
    assert.equal(externalDeletes, 1);
    await page.unroute(/\/api\/modules$/);
    await page.unroute("**/api/modules/external/ux_external");

    const realSettings = await (await page.request.get(`${baseURL}/api/settings`)).json();
    await page.route(/\/api\/settings$/, (route) => {
      if (route.request().method() !== "GET") return route.continue();
      return route.fulfill({ json: { ...realSettings, deployment: { ...realSettings.deployment, authMode: "password" } } });
    });
    await page.goto(`${baseURL}/settings?tab=security`);
    await page.getByLabel("当前密码", { exact: true }).fill("fixture-current");
    await page.getByLabel("新密码", { exact: true }).fill("StrongPass1!");
    const confirmation = page.getByLabel("确认新密码", { exact: true });
    await confirmation.fill("StrongPass2!");
    assert.equal(await confirmation.getAttribute("aria-invalid"), "true");
    const describedBy = (await confirmation.getAttribute("aria-describedby") || "").split(/\s+/).filter(Boolean);
    assert(describedBy.length > 0, "the confirmation error must be associated through aria-describedby");
    for (const id of describedBy) {
      assert.equal(await page.locator(`[id="${id}"]`).count(), 1, `missing described error element #${id}`);
    }
    assert.match(await page.locator(`[id="${describedBy[0]}"]`).innerText(), /两次输入的密码不一致/);
    await page.unroute(/\/api\/settings$/);

    await page.getByRole("link", { name: "通知推送", exact: true }).click();
    const telegramSwitch = page.getByRole("switch", { name: "启用推送", exact: true });
    await telegramSwitch.waitFor();
    const switchBox = await telegramSwitch.boundingBox();
    const trackBox = await telegramSwitch.locator(".studio-switch-track").boundingBox();
    assert.deepEqual({ width: switchBox.width, height: switchBox.height }, { width: 44, height: 44 });
    assert.deepEqual({ width: trackBox.width, height: trackBox.height }, { width: 38, height: 22 });
    await page.getByRole("link", { name: "外观", exact: true }).click();
    await page.getByRole("group", { name: "主题", exact: true }).waitFor();
    for (const theme of ["浅色", "深色", "跟随系统"]) {
      assert.equal(await page.getByRole("button", { name: theme, exact: true }).count(), 1, `missing exact theme card: ${theme}`);
    }

    for (const scenario of [
      { width: 1440, height: 900, theme: "浅色" },
      { width: 1440, height: 900, theme: "深色" },
      { width: 390, height: 800, theme: "浅色" },
      { width: 360, height: 800, theme: "浅色" }
    ]) {
      await page.setViewportSize({ width: scenario.width, height: scenario.height });
      await page.getByRole("button", { name: scenario.theme, exact: true }).click();
      await page.waitForFunction((dark) => document.documentElement.classList.contains("dark") === dark, scenario.theme === "深色");
      const prefix = `${scenario.theme} ${scenario.width}px`;
      await expectNoOverflow(page, `Appearance settings at ${prefix}`);
      await visitWithoutOverflow(page, baseURL, `Dashboard at ${prefix}`, async () => {
        await page.getByRole("heading", { name: "今天，一目了然", exact: true }).waitFor();
        // Overview cards are titled by their widget definitions; the 今日概况 tiles are always present.
        await page.getByRole("list", { name: "今日概况", exact: true }).waitFor();
      });
      await visitWithoutOverflow(page, `${baseURL}/todo`, `Todo at ${prefix}`, async () => {
        await page.getByLabel("待办标题", { exact: true }).waitFor();
        await page.locator(".todo-groups").getByText("日期持久化验证", { exact: true }).waitFor();
      });
      await visitWithoutOverflow(page, `${baseURL}/investment`, `Investment terminal at ${prefix}`, async () => {
        await page.getByRole("region", { name: "投资终端", exact: true }).waitFor();
        await page.getByRole("heading", { name: "未连接 Schwab", exact: true }).waitFor();
      });
      await visitWithoutOverflow(page, `${baseURL}/investment/monitor`, `Investment monitor at ${prefix}`, async () => {
        await page.getByRole("button", { name: "添加规则", exact: true }).waitFor();
        await page.getByText("暂无监控规则", { exact: true }).waitFor();
        await page.getByText("暂无触发记录", { exact: true }).waitFor();
      });
      const newsReady = {
        articles: () => page.getByText("暂无订阅源", { exact: true }).waitFor(),
        sources: () => page.getByText("暂无订阅源", { exact: true }).waitFor(),
        rules: () => page.getByText("暂无关注规则", { exact: true }).waitFor(),
        digests: () => page.getByText("暂无简报", { exact: true }).waitFor()
      };
      for (const tab of ["articles", "sources", "rules", "digests"]) {
        const suffix = tab === "articles" ? "" : `?tab=${tab}`;
        await visitWithoutOverflow(page, `${baseURL}/news${suffix}`, `News ${tab} at ${prefix}`, newsReady[tab]);
      }
      const settingsReady = {
        modules: () => page.getByRole("heading", { name: "业务", exact: true }).waitFor(),
        ai: () => page.getByLabel("接口地址", { exact: true }).waitFor(),
        notifications: () => page.getByRole("switch", { name: "启用推送", exact: true }).waitFor(),
        security: () => page.getByRole("region", { name: "账户安全", exact: true }).getByText("本机免登录", { exact: true }).waitFor(),
        appearance: () => page.getByRole("group", { name: "主题", exact: true }).waitFor(),
        data: () => page.getByRole("region", { name: "运行信息", exact: true }).waitFor()
      };
      for (const tab of ["modules", "ai", "notifications", "security", "appearance", "data"]) {
        await visitWithoutOverflow(page, `${baseURL}/settings?tab=${tab}`, `Settings ${tab} at ${prefix}`, settingsReady[tab]);
      }
      await page.goto(`${baseURL}/settings?tab=modules`);
      await page.getByRole("heading", { name: "业务", exact: true }).waitFor();
      for (const dialog of [
        { module: "投资", ready: () => page.getByLabel("App Key", { exact: true }).waitFor() },
        {
          module: "待办",
          ready: () => page.getByLabel("Wallos 地址", { exact: true }).waitFor(),
          field: () => page.getByLabel("Wallos 地址", { exact: true }),
          draft: "https://discard.example.com",
          saved: "https://wallos.example.com",
          savedToast: "Wallos 设置已保存"
        },
        {
          module: "资讯",
          ready: () => page.getByRole("switch", { name: "自动分析新资讯", exact: true }).waitFor(),
          field: () => page.getByRole("dialog", { name: "资讯设置", exact: true }).getByLabel("主题", { exact: true }),
          draft: "未保存主题",
          saved: "科技",
          savedToast: "资讯设置已保存"
        }
      ]) {
        const trigger = page.getByRole("button", { name: `${dialog.module}设置`, exact: true });
        await trigger.click();
        await dialog.ready();
        await expectNoOverflow(page, `${dialog.module} settings dialog at ${prefix}`);
        if (dialog.field && scenario.width === 1440 && scenario.theme === "浅色") {
          const input = dialog.field();
          const initial = await input.inputValue();
          await input.fill(dialog.draft);
          await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
          const discard = page.getByRole("alertdialog", { name: "放弃未保存的修改？", exact: true });
          await discard.getByRole("button", { name: "继续编辑", exact: true }).click();
          await discard.waitFor({ state: "hidden" });
          assert.equal(await input.inputValue(), dialog.draft);
          await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
          await discard.getByRole("button", { name: "放弃修改", exact: true }).click();
          await page.getByRole("dialog", { name: `${dialog.module}设置`, exact: true }).waitFor({ state: "hidden" });
          await trigger.click();
          await dialog.ready();
          assert.equal(await input.inputValue(), initial);
          await input.fill(dialog.saved);
          await page.getByRole("button", { name: "保存", exact: true }).click();
          await page.getByText(dialog.savedToast, { exact: true }).waitFor();
        }
        await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
        await page.getByRole("dialog", { name: `${dialog.module}设置`, exact: true }).waitFor({ state: "hidden" });
      }
      await page.goto(`${baseURL}/settings?tab=appearance`);
      await page.getByRole("group", { name: "主题", exact: true }).waitFor();
      for (const theme of ["浅色", "深色", "跟随系统"]) {
        await page.getByRole("button", { name: theme, exact: true }).waitFor();
      }
      await page.screenshot({
        path: `/tmp/workbench-frontend-ux-${scenario.theme}-${scenario.width}.png`,
        fullPage: true,
        animations: "disabled"
      });
    }

    assert.deepEqual(errors, []);
    console.log("PASS: touch Todo/deadline behavior, five delete focus flows, Field error association, toast limit, News tabs, shared switch, theme cards, full 1440/390/360 page matrix, and no console/page errors.");
  } finally {
    if (browser) await browser.close();
    if (app.pid && app.exitCode === null) {
      const stopped = new Promise((resolve) => app.once("exit", resolve));
      app.kill("SIGTERM");
      await stopped;
    }
    await rm(directory, { recursive: true, force: true });
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
