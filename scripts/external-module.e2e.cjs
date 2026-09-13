// Browser coverage for external module attach, deep link, command palette, settings, and pending copy.
// Required env: WORKBENCH_TEST_URL, PLAYWRIGHT_MODULE or playwright, optional CHROMIUM_PATH,
// EXAMPLE_URL (running examples/external-module), EXAMPLE_TOKEN.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18086";
const exampleURL = process.env.EXAMPLE_URL || "http://127.0.0.1:8091";
const exampleToken = process.env.EXAMPLE_TOKEN || "example-token";
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      const page = await browser.newPage({ viewport });
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(`${baseURL}/settings?tab=modules`);
      await page.getByRole("heading", { name: "业务模块" }).waitFor();
      if (await page.getByRole("heading", { name: "外部示例", exact: true }).count() === 0) {
        await page.getByPlaceholder("http://127.0.0.1:8091").fill(exampleURL);
        await page.locator('input[type="password"]').first().fill(exampleToken);
        await page.getByRole("button", { name: "接入", exact: true }).click();
      }
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
      await page.getByRole("button", { name: "搜索页面" }).click();
      await page.getByRole("button", { name: "外部示例", exact: true }).waitFor();
      await page.keyboard.press("Escape");
      await page.goto(`${baseURL}/settings?tab=modules`);
      const moduleSwitch = page.getByRole("switch", { name: "外部示例", exact: true });
      if (await moduleSwitch.getAttribute("aria-checked") === "true") {
        await moduleSwitch.click();
      }
      await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="外部示例"]')?.getAttribute("aria-checked") === "false");
      await page.getByRole("button", { name: "外部示例设置", exact: true }).click();
      await page.getByText("业务已停用，仍可修改配置和连接。").waitFor();
      await page.getByRole("button", { name: "关闭模块设置", exact: true }).click();
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
