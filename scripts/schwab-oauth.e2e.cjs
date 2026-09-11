// Run through TestSchwabOAuthBrowser: both the workbench and Schwab are local fixtures.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const baseURL = process.env.WORKBENCH_TEST_URL;
  const schwabURL = process.env.SCHWAB_TEST_URL;
  assert(baseURL && schwabURL, "run through TestSchwabOAuthBrowser");
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.goto(baseURL);
    await page.evaluate(async () => {
      const { token } = await (await fetch("/api/auth/csrf")).json();
      const response = await fetch("/api/auth/login", {
        method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
        body: JSON.stringify({ password: "Browser-Test-123" })
      });
      if (!response.ok) throw new Error("test workbench login failed");
    });
    const session = (await context.cookies(baseURL)).find(cookie => cookie.name === "workbench_session");
    assert.equal(session?.sameSite, "Strict");
    assert.equal(session.secure, true);
    await page.getByRole("link", { name: "登录 Schwab", exact: true }).click();
    await page.waitForURL(url => url.origin === schwabURL && url.pathname === "/v1/oauth/authorize");
    await page.getByRole("heading", { name: "Accounts linked to WorkBench", exact: true }).waitFor();
    await page.getByRole("link", { name: "Done", exact: true }).click();
    await page.waitForURL(`${baseURL}/investment`);
    await page.getByRole("heading", { name: "投资", exact: true }).waitFor();
    assert.equal(await page.evaluate(async () => (await (await fetch("/api/auth/status")).json()).authenticated), true);
    assert.deepEqual(errors, []);
    console.log("PASS: cross-site Done callback automatically returns to the workbench, preserves its Strict session, and clears authorization parameters.");
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
