// Use a disposable workspace; the SQLite scenario creates a backup.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL || "http://127.0.0.1:18087";
const driver = process.env.WORKBENCH_TEST_DB_DRIVER || "sqlite";
assert(["sqlite", "postgres"].includes(driver));

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const settingsResponse = page.waitForResponse((response) => response.url() === `${baseURL}/api/settings`);
    await page.goto(`${baseURL}/settings?tab=data`);
    const settings = await (await settingsResponse).json();
    assert.equal(settings.deployment.databaseDriver, driver);
    assert.equal(settings.deployment.backupSupported, driver === "sqlite");
    assert.equal(Object.hasOwn(settings.deployment, "databaseUrl"), false);
    await page.getByRole("heading", { name: "运行信息", exact: true }).waitFor();
    await page.getByText(driver === "postgres" ? "PostgreSQL" : "SQLite", { exact: true }).waitFor();
    const backup = page.getByRole("button", { name: "创建备份", exact: true });
    if (driver === "sqlite") {
      const response = page.waitForResponse((result) => result.url() === `${baseURL}/api/system/backup` && result.request().method() === "POST");
      await backup.click();
      assert.equal((await response).status(), 201);
      const notice = page.getByRole("status").filter({ hasText: "备份已创建" });
      await notice.waitFor();
      assert.match(await notice.innerText(), /backups\/.+/);
    } else {
      assert.equal(await backup.count(), 0);
      assert(await page.getByText("由数据库服务管理", { exact: true }).isVisible());
      assert(await page.getByText("本地数据路径", { exact: true }).isVisible());
    }
    await page.reload();
    await page.getByText(driver === "postgres" ? "PostgreSQL" : "SQLite", { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    assert.deepEqual(errors, []);
    console.log(`PASS: ${driver} settings, backup behavior, reload and mobile layout`);
  } finally {
    await browser.close();
  }
})().catch((error) => { console.error(error); process.exit(1); });
