// Use only a disposable workspace; this test changes its saved logging settings.
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
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${baseURL}/settings?tab=data`);
    const select = page.getByLabel("日志等级", { exact: true });
    const save = page.getByRole("button", { name: "保存", exact: true });
    let requestArrived;
    let releaseRequest;
    await page.route("**/api/settings/logging", async (route) => {
      if (route.request().method() !== "PUT") return route.continue();
      requestArrived?.();
      await new Promise((resolve) => { releaseRequest = resolve; });
      await route.continue();
    });
    await select.waitFor();
    for (const level of ["debug", "warn", "error", "info"]) {
      await choose(page, select, { debug: "DEBUG · 调试", info: "INFO · 信息", warn: "WARN · 警告", error: "ERROR · 错误" }[level]);
      const arrived = new Promise((resolve) => { requestArrived = resolve; });
      const response = page.waitForResponse((r) => r.url() === `${baseURL}/api/settings/logging` && r.request().method() === "PUT");
      await save.click();
      await arrived;
      const pending = page.getByRole("button", { name: "保存中…", exact: true });
      await pending.waitFor();
      assert(await pending.isDisabled());
      releaseRequest();
      assert.equal((await response).status(), 200);
      requestArrived = undefined;
      releaseRequest = undefined;
      await page.reload();
      await select.waitFor();
      assert.equal(await select.innerText(), { debug: "DEBUG · 调试", info: "INFO · 信息", warn: "WARN · 警告", error: "ERROR · 错误" }[level]);
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
