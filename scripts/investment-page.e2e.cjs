// Disposable Workbench + an instrumented iframe: verify presentation changes
// keep the same chart document alive. Actual TradingView has a separate test.
const assert = require("node:assert/strict");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-investment-page-"));
  const baseURL = "http://127.0.0.1:18138";
  let app, browser;
  try {
    app = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-investment-page-test", ["-data", join(directory, "data.db"), "-auth", "local", "-listen", "127.0.0.1:18138"], {
      env: { ...process.env, OPENAI_API_KEY: "", WORKBENCH_ALLOWED_HOSTS: "" }, stdio: "ignore",
    });
    let spawnError;
    app.on("error", error => { spawnError = error; });
    for (let i = 0; i < 100; i++) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited: ${app.exitCode}`);
      try { if ((await fetch(`${baseURL}/health/ready`)).ok) break; } catch { /* wait for startup */ }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox"] });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const errors = [];
    context.on("page", page => page.on("pageerror", error => errors.push(error.message)));
    await context.route("**/api/modules/investment/schwab", route => route.fulfill({ json: {
      appKey: "fixture", callbackUrl: "https://example.test/oauth/schwab", hasAppSecret: true, connected: true, lastError: "",
    } }));
    let loads = 0;
    let releaseFirstTerminal, notifyFirstTerminal;
    const firstTerminalGate = new Promise(resolve => { releaseFirstTerminal = resolve; });
    const firstTerminalRequested = new Promise(resolve => { notifyFirstTerminal = resolve; });
    await context.route("**/investment/terminal?**", async route => {
      loads++;
      if (loads === 1) { notifyFirstTerminal(); await firstTerminalGate; }
      return route.fulfill({ contentType: "text/html", body: `<html><body style="margin:0"><p>终端布局测试</p><script>
        window.chartState = { symbol: 'MSFT', interval: '1H', instance: ${loads} };
        const setTheme = theme => {
          window.chartTheme = theme;
          document.body.style.background = theme === 'dark' ? '#131722' : '#fff';
          document.body.style.color = theme === 'dark' ? '#d1d4dc' : '#131722';
        };
        setTheme(new URLSearchParams(location.search).get('theme'));
        window.addEventListener('message', event => {
          if (event.source === parent && event.origin === location.origin && event.data?.type === 'workbench.theme') setTheme(event.data.theme);
        });
      </script></body></html>` });
    });
    const page = await context.newPage();
    await page.goto(`${baseURL}/investment`, { waitUntil: "domcontentloaded" });
    const iframe = page.locator("iframe[title='TradingView 投资终端']");
    await iframe.waitFor();
    await firstTerminalRequested;
    // Messages sent before the terminal document loads must be recovered on load.
    let loadingTheme;
    for (let i = 0; i < 3; i++) {
      loadingTheme = await page.evaluate(() => document.documentElement.classList.contains("dark") ? "light" : "dark");
      await page.getByRole("button", { name: "切换主题", exact: true }).click();
      await page.waitForFunction(expected => document.documentElement.classList.contains("dark") === (expected === "dark"), loadingTheme);
    }
    releaseFirstTerminal();
    const chart = page.frameLocator("iframe[title='TradingView 投资终端']");
    await chart.getByText("终端布局测试").waitFor();
    await iframe.evaluate(element => { window.originalChart = element; element.contentWindow.chartState.interval = "5m"; });
    let original = await iframe.evaluate(element => element.contentWindow.chartState);
    const checkChartPreserved = async () => {
      assert.equal(await iframe.evaluate(element => element === window.originalChart), true);
      assert.deepEqual(await iframe.evaluate(element => element.contentWindow.chartState), original);
    };
    const checkTheme = async theme => {
      await page.waitForFunction(expected => document.documentElement.classList.contains("dark") === (expected === "dark"), theme);
      await page.waitForFunction(expected => document.querySelector("iframe[title='TradingView 投资终端']")?.contentWindow.chartTheme === expected, theme, { timeout: 5000 });
      await checkChartPreserved();
    };
    await checkTheme(loadingTheme);
    for (let i = 0; i < 8; i++) {
      const next = await page.evaluate(() => document.documentElement.classList.contains("dark") ? "light" : "dark");
      await page.getByRole("button", { name: "切换主题", exact: true }).click();
      await checkTheme(next);
    }
    assert.equal(loads, 1, "changing theme must preserve the chart document and selected interval");

    const checkExpandedBounds = async target => {
      const bounds = await target.getByRole("region", { name: "投资终端", exact: true }).boundingBox();
      const viewport = target.viewportSize();
      assert.equal(bounds.x, 0); assert.equal(bounds.y, 0);
      assert.equal(bounds.width, viewport.width); assert.equal(bounds.height, viewport.height);
      assert.equal(await target.getByRole("navigation", { name: "主导航" }).isVisible(), false);
      assert.equal(await target.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      const chartBounds = await target.locator("iframe").boundingBox();
      assert(Math.abs(chartBounds.y + chartBounds.height - viewport.height) < 2);
    };
    await page.getByRole("button", { name: "页面铺满", exact: true }).click();
    await page.getByRole("button", { name: "返回工作台", exact: true }).waitFor();
    await checkExpandedBounds(page);
    await checkChartPreserved();

    await page.getByRole("button", { name: "全屏", exact: true }).click();
    await page.waitForFunction(() => document.fullscreenElement?.classList.contains("investment-view"));
    await page.getByRole("button", { name: "退出全屏", exact: true }).waitFor();
    await checkChartPreserved();
    await page.evaluate(() => document.exitFullscreen());
    await page.getByRole("button", { name: "全屏", exact: true }).waitFor();

    const [popup] = await Promise.all([context.waitForEvent("page"), page.getByRole("link", { name: "新窗口", exact: true }).click()]);
    await popup.frameLocator("iframe").getByText("终端布局测试").waitFor();
    assert.equal(new URL(popup.url()).searchParams.get("view"), "terminal");
    assert.equal(await popup.evaluate(() => window.opener), null);
    await checkExpandedBounds(popup);
    await checkChartPreserved();
    await popup.close();
    assert.equal(loads, 2, "expanding and fullscreen must not reload the existing chart");

    await page.getByRole("button", { name: "返回工作台", exact: true }).click();
    await page.getByRole("heading", { name: "投资", exact: true }).waitFor();
    assert.equal(await page.getByRole("navigation", { name: "主导航" }).isVisible(), true);
    await checkChartPreserved();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("button", { name: "页面铺满", exact: true }).click();
    await checkExpandedBounds(page);
    await checkChartPreserved();
    await page.screenshot({ path: join(tmpdir(), "workbench-investment-expanded-mobile.png"), fullPage: true });

    await page.getByRole("region", { name: "投资终端", exact: true }).evaluate(element => { element.requestFullscreen = () => Promise.reject(new Error("fullscreen unavailable")); });
    await page.getByRole("button", { name: "全屏", exact: true }).click();
    await page.getByRole("alert").filter({ hasText: "浏览器未能进入全屏" }).waitFor();
    await page.getByRole("button", { name: "返回工作台", exact: true }).click();
    // System appearance changes must propagate without navigating or reloading the chart.
    await page.getByRole("link", { name: "设置", exact: true }).click();
    await page.getByRole("tab", { name: "外观" }).click();
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith("/api/settings/appearance") && response.request().method() === "PUT"),
      page.getByRole("button", { name: /跟随系统/ }).click(),
    ]);
    await page.getByRole("link", { name: "投资", exact: true }).click();
    await chart.getByText("终端布局测试").waitFor();
    await iframe.evaluate(element => { window.originalChart = element; element.contentWindow.chartState.interval = "15m"; });
    original = await iframe.evaluate(element => element.contentWindow.chartState);
    const systemLoads = loads;
    for (const colorScheme of ["dark", "light", "dark", "light"]) {
      await page.emulateMedia({ colorScheme });
      await checkTheme(colorScheme);
    }
    assert.equal(loads, systemLoads, "system theme changes must not reload the chart");
    await page.getByRole("link", { name: "总览", exact: true }).click();
    await page.getByRole("heading", { name: "总览", exact: true }).waitFor();
    assert.notEqual(await page.evaluate(() => getComputedStyle(document.body).overflow), "hidden");
    assert.deepEqual(errors, []);
    console.log("PASS: repeated and system theme changes, expanded/normal layout, native fullscreen, new window, mobile sizing, chart preservation and fullscreen failure recovery; no trades.");
  } finally {
    await browser?.close();
    if (app && app.exitCode === null && app.pid) {
      const stopped = new Promise(resolve => app.once("exit", resolve));
      app.kill("SIGTERM");
      await stopped;
    }
    await rm(directory, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
