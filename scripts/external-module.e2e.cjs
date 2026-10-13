// Browser coverage for legacy external module deep link, sidebar navigation, settings, and pending copy.
// Required env: WORKBENCH_TEST_URL, PLAYWRIGHT_MODULE or playwright, optional CHROMIUM_PATH,
// Requires an existing demo_external record from an older workspace; no URL registration.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18086";
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      const page = await browser.newPage({ viewport });
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(`${baseURL}/settings?tab=modules`);
      await page.getByRole("heading", { name: "业务", exact: true }).waitFor();
      assert.equal(await page.getByText("添加外部模块", { exact: true }).count(), 0);
      assert.equal(await page.getByRole("heading", { name: "外部示例", exact: true }).count(), 1, "Requires a workspace with a legacy demo_external fixture");
      await page.getByRole("heading", { name: "外部示例", exact: true }).waitFor();
      await page.getByRole("button", { name: "外部示例设置", exact: true }).waitFor();
      const enable = page.getByRole("switch", { name: "外部示例", exact: true });
      if (await enable.getAttribute("aria-checked") !== "true") {
        await enable.click();
      }
      await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="外部示例"]')?.getAttribute("aria-checked") === "true");
      await page.goto(`${baseURL}/apps/demo_external/overview`);
      await page.locator("iframe.external-module-frame").waitFor();
      await page.reload();
      await page.locator("iframe.external-module-frame").waitFor();
      await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "总览", exact: true }).click();
      await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "外部示例", exact: true }).click();
      await page.locator("iframe.external-module-frame").waitFor();
      await page.goto(`${baseURL}/settings?tab=modules`);
      const moduleSwitch = page.getByRole("switch", { name: "外部示例", exact: true });
      if (await moduleSwitch.getAttribute("aria-checked") === "true") {
        await moduleSwitch.click();
      }
      await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="外部示例"]')?.getAttribute("aria-checked") === "false");
      await page.getByRole("button", { name: "外部示例设置", exact: true }).click();
      const disabledNotice = page.getByRole("status").filter({ hasText: "业务已停用" });
      await disabledNotice.getByText("可修改配置和连接。", { exact: true }).waitFor();
      await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
      assert.equal(errors.length, 0, errors.join("\n"));
      if (viewport.width >= 1440 && process.env.E2E_SCREENSHOT) {
        await page.screenshot({ path: process.env.E2E_SCREENSHOT, fullPage: true });
      }
      await page.close();
    }
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exit(1);
});
