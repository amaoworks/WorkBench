// Use only a disposable workspace; this test changes its saved logging settings.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18086";
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${baseURL}/settings?tab=data`);
    const select = page.getByLabel("日志等级", { exact: true });
    const save = page.getByRole("button", { name: "保存日志等级", exact: true });
    await select.waitFor();
    for (const level of ["debug", "warn", "error", "info"]) {
      await select.selectOption(level);
      const response = page.waitForResponse((r) => r.url() === `${baseURL}/api/settings/logging` && r.request().method() === "PUT");
      await save.click();
      assert.equal((await response).status(), 200);
      await page.waitForFunction(() => [...document.querySelectorAll("button")].find((b) => b.textContent === "保存日志等级")?.disabled);
      await page.reload();
      await select.waitFor();
      assert.equal(await select.inputValue(), level);
      assert(await save.isDisabled());
    }
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await select.isVisible());
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    assert.deepEqual(errors, []);
    console.log("PASS: logging settings switch, save, reload and mobile layout");
  } finally {
    await browser.close();
  }
})().catch((error) => { console.error(error); process.exit(1); });
