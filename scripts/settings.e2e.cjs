// Read-only settings regression against an explicitly supplied disposable password-mode workspace.
// Both accounts must have completed their initial password change. No provider login or message is sent.
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const baseURL = process.env.WORKBENCH_TEST_URL;
const accounts = [
  { role: "admin", username: process.env.WORKBENCH_TEST_ADMIN_USERNAME || "admin", password: process.env.WORKBENCH_TEST_ADMIN_PASSWORD },
  { role: "user", username: process.env.WORKBENCH_TEST_USER_USERNAME, password: process.env.WORKBENCH_TEST_USER_PASSWORD },
];

async function expectURL(page, path, params = {}) {
  await page.waitForFunction(({ path, params }) => {
    const url = new URL(location.href);
    return url.pathname === path && Object.entries(params).every(([key, value]) => url.searchParams.get(key) === value);
  }, { path, params });
}

async function expectNoOverflow(page) {
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `horizontal overflow at ${page.url()}`);
}

(async () => {
  assert(baseURL, "WORKBENCH_TEST_URL must identify a disposable workspace");
  for (const account of accounts) assert(account.username && account.password, `Missing ${account.role} fixture credentials`);
  const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
  try {
    for (const account of accounts) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, ignoreHTTPSErrors: true, hasTouch: true });
      try {
        const page = await context.newPage();
        const errors = [];
        page.on("pageerror", (error) => errors.push(error.message));
        await page.goto(baseURL);
        await page.getByLabel("用户名", { exact: true }).fill(account.username);
        await page.getByLabel("密码", { exact: true }).fill(account.password);
        const login = page.waitForResponse((response) => response.url().endsWith("/api/auth/login") && response.request().method() === "POST");
        await page.getByRole("button", { name: "登录", exact: true }).click();
        assert.equal((await login).status(), 200);
        await page.getByRole("button", { name: /^账户菜单/ }).waitFor();
        const status = await (await page.request.get(`${baseURL}/api/auth/status`)).json();
        assert.equal(status.user.role, account.role);
        assert.equal(status.mustChangePassword, false, "Fixture accounts must finish initial password changes before this regression");
        await page.getByRole("button", { name: /^账户菜单/ }).click();
        const menu = page.getByRole("menu", { name: "账户菜单", exact: true });
        assert.deepEqual((await menu.getByRole("menuitem").allInnerTexts()).map((text) => text.trim()),
          account.role === "admin" ? ["个人设置", "后台设置", "退出登录"] : ["个人设置", "退出登录"]);
        assert.equal(await menu.getByRole("menuitem", { name: "个人设置", exact: true }).getAttribute("href"), "/settings");
        if (account.role === "admin") assert.equal(await menu.getByRole("menuitem", { name: "后台设置", exact: true }).getAttribute("href"), "/admin/settings");
        await page.keyboard.press("End");
        assert.equal(await menu.getByRole("menuitem", { name: "退出登录", exact: true }).evaluate((item) => item === document.activeElement), true);
        await page.keyboard.press("Home");
        assert.equal(await menu.getByRole("menuitem", { name: "个人设置", exact: true }).evaluate((item) => item === document.activeElement), true);
        await page.keyboard.press("Escape");
        const avatar = page.getByRole("button", { name: /^账户菜单/ });
        assert.equal(await avatar.evaluate((item) => item === document.activeElement), true);
        await page.keyboard.press("ArrowDown");
        await menu.waitFor();
        await menu.getByRole("menuitem", { name: "个人设置", exact: true }).click();
        await expectURL(page, "/settings");
        await page.getByRole("heading", { name: "个人设置", exact: true }).waitFor();
        await page.getByRole("heading", { name: "个人推送", exact: true }).waitFor();
        assert.equal(await page.getByLabel("Bot Token", { exact: true }).count(), 0);
        assert.equal(await page.getByRole("switch", { name: "投资", exact: true }).count(), 0);
        assert.equal(await page.locator(".workspace-avatar.is-active").count(), 1);
        assert.equal(await page.getByRole("navigation", { name: "主导航", exact: true }).getByRole("link", { name: /设置/ }).count(), 0);

        const administrationRequests = [];
        const observeAdministration = (request) => {
          const path = new URL(request.url()).pathname;
          if (path.startsWith("/api/admin/") || path === "/api/settings/telegram") administrationRequests.push(path);
        };
        page.on("request", observeAdministration);
        for (const tab of ["ai", "users", "data"]) {
          await page.goto(`${baseURL}/settings?tab=${tab}`);
          if (account.role === "admin") {
            await expectURL(page, "/admin/settings", { tab });
            await page.getByRole("heading", { name: "后台设置", exact: true }).waitFor();
          } else {
            await page.getByRole("heading", { name: "个人设置", exact: true }).waitFor();
            assert.equal(new URL(page.url()).pathname, "/settings");
          }
        }
        if (account.role === "admin") {
          // A slow user list above the target must not invalidate initial section positioning.
          await page.route("**/api/admin/users", async (route) => {
            const response = await route.fetch();
            await new Promise((resolve) => setTimeout(resolve, 150));
            await route.fulfill({ response });
          });
          await page.goto(`${baseURL}/admin/settings?tab=modules`);
          await page.getByRole("switch", { name: "投资", exact: true }).waitFor();
          await page.waitForFunction(() => {
            const section = document.getElementById("settings-modules");
            return section && Math.abs(section.getBoundingClientRect().top - parseFloat(getComputedStyle(section).scrollMarginTop)) < 2;
          }).catch(async (error) => {
            const position = await page.locator("#settings-modules").evaluate((section) => ({
              top: section.getBoundingClientRect().top, margin: getComputedStyle(section).scrollMarginTop, scrollY,
              userHeight: document.getElementById("settings-users").getBoundingClientRect().height,
            }));
            throw new Error(`Business deep link did not settle: ${JSON.stringify(position)}`, { cause: error });
          });
          await page.unroute("**/api/admin/users");
        }
        await page.goto(`${baseURL}/admin/settings?tab=notifications`);
        if (account.role === "admin") {
          await page.getByLabel("Bot Token", { exact: true }).waitFor();
          assert.equal(await page.getByLabel("Chat ID", { exact: true }).count(), 0);
          assert.equal(await page.getByRole("heading", { name: "个人推送", exact: true }).count(), 0);
          assert.equal(await page.locator(".workspace-avatar.is-active").count(), 1);
        } else {
          await page.getByRole("heading", { name: "无权访问后台设置", exact: true }).waitFor();
          assert.deepEqual(administrationRequests, [], "ordinary settings visits must not mount administrator queries");
        }
        page.off("request", observeAdministration);

        // Browser-only responses exercise connection guidance without touching shared application credentials.
        await page.route("**/api/modules/investment/schwab", (route) => route.fulfill({ json: {
          configured: false, appKey: "", hasAppSecret: false, callbackUrl: "", connected: false,
          reauthorizationRequired: false, lastError: "",
        } }));
        await page.goto(`${baseURL}/settings?tab=modules&module=investment`);
        const personal = page.getByRole("dialog", { name: "投资设置", exact: true });
        await personal.getByRole("heading", { name: "Schwab 应用未配置", exact: true }).waitFor();
        await expectURL(page, "/settings", { tab: "modules", module: "investment" });
        assert.equal(await personal.getByLabel("App Key", { exact: true }).count(), 0);
        assert.equal(await personal.getByRole("button", { name: "登录 Schwab", exact: true }).isDisabled(), true);
        if (account.role === "admin") {
          assert.equal(await personal.getByRole("link", { name: "后台设置", exact: true }).getAttribute("href"), "/admin/settings?tab=modules&module=investment");
          await page.goto(`${baseURL}/admin/settings?tab=modules&module=investment`);
          const shared = page.getByRole("dialog", { name: "投资设置", exact: true });
          await shared.getByLabel("App Key", { exact: true }).waitFor();
          assert.equal(await shared.getByRole("button", { name: "登录 Schwab", exact: true }).count(), 0);
          assert.equal(await shared.getByRole("button", { name: "断开", exact: true }).count(), 0);
        } else {
          await personal.getByText("联系管理员配置应用。", { exact: true }).waitFor();
          assert.equal(await personal.getByRole("link", { name: "后台设置", exact: true }).count(), 0);
        }
        await page.unroute("**/api/modules/investment/schwab");

        const modules = await (await page.request.get(`${baseURL}/api/modules`)).json();
        assert(modules.items.some((module) => module.id === "investment" && module.enabled), "Fixture workspace must have investment enabled");
        await page.route(/\/api\/modules$/, (route) => route.fulfill({ json: {
          items: modules.items.map((module) => module.id === "investment" ? { ...module, enabled: false, observedEnabled: false, pending: false, lastError: "" } : module),
        } }));
        await page.goto(`${baseURL}/settings?tab=modules`);
        const investment = page.locator(".module-row").filter({ has: page.getByRole("heading", { name: "投资", exact: true }) });
        await investment.waitFor();
        assert.equal(await investment.getByRole("button", { name: "投资设置", exact: true }).count(), 0);
        await investment.getByText(account.role === "admin" ? "在后台设置中启用。" : "联系管理员启用。", { exact: true }).waitFor();
        await page.goto(`${baseURL}/investment`);
        await page.getByRole("heading", { name: "投资未启用", exact: true }).waitFor();
        if (account.role === "admin") await page.getByRole("link", { name: "业务管理", exact: true }).waitFor();
        else {
          await page.getByText("联系管理员启用。", { exact: true }).waitFor();
          assert.equal(await page.getByRole("link", { name: "业务管理", exact: true }).count(), 0);
        }
        await page.unroute(/\/api\/modules$/);

        const settings = await (await page.request.get(`${baseURL}/api/settings`)).json();
        await page.route(/\/api\/settings$/, (route) => route.fulfill({ json: {
          ...settings, appearance: { ...settings.appearance, theme: "system" },
        } }));
        for (const colorScheme of ["light", "dark"]) {
          await page.emulateMedia({ colorScheme });
          for (const width of [1440, 390, 360]) {
            await page.setViewportSize({ width, height: 1000 });
            await page.goto(`${baseURL}/settings?tab=notifications`);
            await page.getByRole("heading", { name: "个人推送", exact: true }).waitFor();
            await expectNoOverflow(page);
            assert.equal(await page.locator("html").evaluate((root) => root.classList.contains("dark")), colorScheme === "dark");
            if (account.role === "admin") {
              await page.goto(`${baseURL}/admin/settings?tab=notifications`);
              await page.getByLabel("Bot Token", { exact: true }).waitFor();
              await expectNoOverflow(page);
            }
            if (width === 360) {
              await avatar.tap();
              await menu.waitFor();
              assert.equal(await menu.getByRole("menuitem", { name: "后台设置", exact: true }).count(), account.role === "admin" ? 1 : 0);
              await page.keyboard.press("Escape");
            }
          }
        }
        assert.deepEqual(errors, []);
      } finally {
        await context.close();
      }
    }
    console.log("PASS: personal/admin menus, legacy section links, role-guarded administration without queries, scoped investment settings, delayed section positioning, disabled-business guidance, keyboard/touch menus and light/dark responsive layouts; no shared configuration changes.");
  } finally {
    await browser.close();
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
