// Disposable Workbench, local RSS/Atom feeds and a local Responses API fixture.
// Telegram stays disabled; digest push is verified only as a local notification.
const assert = require("node:assert/strict");
const http = require("node:http");
const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { spawn } = require("node:child_process");
const { createServer } = require("node:net");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "/tmp/workbench-e2e/node_modules/playwright");

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function unusedPort() {
  const listener = createServer();
  await new Promise((resolve, reject) => { listener.once("error", reject); listener.listen(0, "127.0.0.1", resolve); });
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}

function rssFeed(origin) {
  const items = Array.from({ length: 35 }, (_, index) => {
    const number = String(index + 1).padStart(2, "0");
    const title = index === 0 ? "Quantum Browser Story" : `Browser Fixture News ${number}`;
    const body = index === 0 ? "Original body quantum for browser regression." : `Original fixture content ${number}.`;
    const published = new Date(Date.now() - index * 60_000).toUTCString();
    return `<item><guid>rss-${number}</guid><title>${title}</title><link>${origin}/articles/rss-${number}</link><description>${body}</description><pubDate>${published}</pubDate></item>`;
  }).join("");
  return `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Browser RSS</title><link>${origin}</link><description>Fixture</description>${items}</channel></rss>`;
}

function atomFeed(origin) {
  return `<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Browser Atom</title><id>${origin}/atom</id>
    <entry><id>atom-01</id><title>Browser Atom Markets</title><link href="${origin}/articles/atom-01"/><updated>${new Date().toISOString()}</updated><content type="text">Atom market fixture content.</content></entry>
    <entry><id>atom-02</id><title>Browser Atom Science</title><link href="${origin}/articles/atom-02"/><updated>${new Date(Date.now() - 120_000).toISOString()}</updated><content type="text">Atom science fixture content.</content></entry>
  </feed>`;
}

function responsesBody(text, sequence) {
  return JSON.stringify({
    id: `resp_news_${sequence}`, object: "response", created_at: Math.floor(Date.now() / 1000), status: "completed", model: "fixture-model",
    output: [{ id: `msg_news_${sequence}`, type: "message", status: "completed", role: "assistant", content: [{ type: "output_text", text, annotations: [] }] }],
    usage: { input_tokens: 10, input_tokens_details: { cached_tokens: 0 }, output_tokens: 10, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: 20 }
  });
}

async function stopProcess(process) {
  if (!process?.pid || process.exitCode !== null) return;
  const stopped = new Promise((resolve) => process.once("exit", resolve));
  process.kill("SIGTERM");
  if (await Promise.race([stopped.then(() => true), sleep(5_000).then(() => false)])) return;
  process.kill("SIGKILL");
  await stopped;
}

async function expectSearchIconSpacing(search, label) {
  const geometry = await search.evaluate((input) => {
    const box = input.getBoundingClientRect();
    const icon = input.parentElement.querySelector("svg").getBoundingClientRect();
    const style = getComputedStyle(input);
    return {
      textLeft: box.left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft),
      iconRight: icon.right,
      centerOffset: Math.abs(box.top + box.height / 2 - (icon.top + icon.height / 2))
    };
  });
  assert(geometry.textLeft >= geometry.iconRight + 4, `${label}: search text overlaps the icon: ${JSON.stringify(geometry)}`);
  assert(geometry.centerOffset <= 1, `${label}: search icon is not vertically centered: ${JSON.stringify(geometry)}`);
}

async function expectFormControlStyles(page, reference, label) {
  await reference.blur();
  await reference.evaluate((node) => Promise.all(node.getAnimations().map((animation) => animation.finished)));
  const form = reference.locator("xpath=ancestor::form[1]");
  const controls = form.locator(".studio-field > :is(input, select, textarea)");
  const snapshots = await controls.evaluateAll((nodes) => nodes.map((node) => {
    const style = getComputedStyle(node);
    const box = node.getBoundingClientRect();
    return {
      tag: node.tagName,
      height: box.height,
      width: box.width,
      fieldWidth: node.parentElement.getBoundingClientRect().width,
      resize: style.resize,
      style: Object.fromEntries([
        "fontFamily", "fontSize", "color", "backgroundColor",
        "borderTopWidth", "borderTopStyle", "borderTopColor", "borderRadius", "padding"
      ].map((property) => [property, style[property]]))
    };
  }));
  assert(snapshots.length > 1, `${label}: expected multiple form controls`);
  for (const snapshot of snapshots) {
    assert.deepEqual(snapshot.style, snapshots[0].style, `${label}: ${snapshot.tag} style differs from the input`);
    assert(Math.abs(snapshot.width - snapshot.fieldWidth) <= 1, `${label}: ${snapshot.tag} does not fill its field`);
    if (snapshot.tag === "TEXTAREA") {
      assert(snapshot.height > snapshots[0].height, `${label}: multiline control must retain its rows`);
      assert.equal(snapshot.resize, "vertical", `${label}: textarea must resize vertically`);
    } else {
      // Native selects compute normal line-height; compare their visible height.
      assert.equal(snapshot.height, snapshots[0].height, `${label}: single-line control heights differ`);
    }
  }
  async function focusedStyle(control) {
    await control.focus();
    return control.evaluate(async (node) => {
      await Promise.all(node.getAnimations().map((animation) => animation.finished));
      const style = getComputedStyle(node);
      return { border: style.borderTopColor, shadow: style.boxShadow };
    });
  }
  const focused = await focusedStyle(reference);
  assert.notEqual(focused.shadow, "none", `${label}: focused input must have a visible ring`);
  for (const control of await form.locator(".studio-field > :is(select, textarea)").all()) {
    assert.deepEqual(await focusedStyle(control), focused, `${label}: focus styles differ from the input`);
  }
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${label}: form overflows the page`);
}

(async () => {
  const directory = await mkdtemp(join(tmpdir(), "workbench-news-"));
  const fixtureErrors = [];
  let aiRequests = 0;
  let feedRequests = 0;
  let app;
  let browser;
  let output = "";
  const fixture = http.createServer(async (request, response) => {
    try {
      const origin = `http://127.0.0.1:${fixture.address().port}`;
      if (request.url === "/rss.xml" || request.url === "/atom.xml") {
        feedRequests++;
        if (request.headers["if-none-match"] === '"news-browser-v1"') {
          response.writeHead(304, { ETag: '"news-browser-v1"' });
          response.end();
          return;
        }
        response.writeHead(200, { "Content-Type": request.url === "/rss.xml" ? "application/rss+xml" : "application/atom+xml", ETag: '"news-browser-v1"' });
        response.end(request.url === "/rss.xml" ? rssFeed(origin) : atomFeed(origin));
        return;
      }
      if (request.url === "/v1/responses" && request.method === "POST") {
        let raw = "";
        for await (const chunk of request) raw += chunk;
        const body = JSON.parse(raw);
        assert.equal(request.headers.authorization, "Bearer fixture-key");
        assert.equal(body.model, "fixture-model");
        assert.equal(body.store, false);
        aiRequests++;
        const text = raw.includes("translatedTitle")
          ? JSON.stringify({ translatedTitle: "量子浏览器资讯", translatedContent: "浏览器回归译文。", summary: "浏览器回归摘要。" })
          : "浏览器简报内容。[1]";
        response.writeHead(200, { "Content-Type": "application/json" });
        response.end(responsesBody(text, aiRequests));
        return;
      }
      response.writeHead(404);
      response.end();
    } catch (error) {
      fixtureErrors.push(error.stack || error.message);
      if (!response.headersSent) response.writeHead(500);
      response.end();
    }
  });

  try {
    await new Promise((resolve, reject) => { fixture.once("error", reject); fixture.listen(0, "127.0.0.1", resolve); });
    const fixtureURL = `http://127.0.0.1:${fixture.address().port}`;
    const appPort = await unusedPort();
    const baseURL = `http://127.0.0.1:${appPort}`;
    const env = { ...process.env };
    for (const key of Object.keys(env)) {
      if (key.startsWith("TELEGRAM_") || key.startsWith("WORKBENCH_TELEGRAM_")) delete env[key];
    }
    Object.assign(env, {
      OPENAI_API_KEY: "fixture-key",
      OPENAI_BASE_URL: `${fixtureURL}/v1`,
      OPENAI_MODEL: "fixture-model",
      WORKBENCH_PUBLIC_URL: "",
      WORKBENCH_ALLOWED_HOSTS: "",
      NO_PROXY: "127.0.0.1,localhost",
      no_proxy: "127.0.0.1,localhost"
    });
    app = spawn(process.env.WORKBENCH_BIN || "/tmp/workbench-news-e2e-bin", ["-data", join(directory, "data.db"), "-auth", "local", "-listen", `127.0.0.1:${appPort}`], { env, stdio: ["ignore", "pipe", "pipe"] });
    let spawnError;
    app.on("error", (error) => { spawnError = error; });
    const collect = (chunk) => { output = (output + chunk.toString()).slice(-32_000); };
    app.stdout.on("data", collect);
    app.stderr.on("data", collect);
    const startupDeadline = Date.now() + 15_000;
    while (true) {
      if (spawnError) throw spawnError;
      if (app.exitCode !== null) throw new Error(`Temporary Workbench exited (${app.exitCode})\n${output}`);
      if (await fetch(`${baseURL}/health/ready`).then((response) => response.ok).catch(() => false)) break;
      if (Date.now() >= startupDeadline) throw new Error(`Temporary Workbench did not start within 15 seconds\n${output}`);
      await sleep(100);
    }

    browser = await chromium.launch({ headless: true, executablePath: process.env.CHROMIUM_PATH || "/usr/bin/chromium", args: ["--no-sandbox"] });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
    page.setDefaultTimeout(15_000);
    const pageErrors = [];
    const unexpectedBrowserRequests = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    page.on("dialog", async (dialog) => { pageErrors.push(`Unexpected native dialog: ${dialog.message()}`); await dialog.dismiss(); });
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.origin !== baseURL) unexpectedBrowserRequests.push(request.url());
    });

    await page.goto(`${baseURL}/settings?tab=modules`);
    const telegram = await (await page.request.get(`${baseURL}/api/settings/telegram`)).json();
    assert.equal(telegram.enabled, false, "Telegram must remain disabled");
    assert.equal(telegram.hasBotToken, false, "Telegram token must not be configured");

    await page.getByRole("button", { name: "资讯设置", exact: true }).click();
    let settingsDialog = page.getByRole("dialog", { name: "资讯设置", exact: true });
    const autoAI = settingsDialog.getByRole("switch", { name: "自动分析新资讯", exact: true });
    if (await autoAI.getAttribute("aria-checked") === "true") await autoAI.click();
    await settingsDialog.getByLabel("目标语言", { exact: true }).fill("zh-TW");
    await settingsDialog.getByLabel("每日 AI 上限", { exact: true }).fill("7");
    await settingsDialog.getByLabel("保留天数", { exact: true }).fill("45");
    await settingsDialog.getByRole("switch", { name: "启用定时简报", exact: true }).click();
    await settingsDialog.getByLabel("小时", { exact: true }).fill("8");
    await settingsDialog.getByLabel("分钟", { exact: true }).fill("30");
    await settingsDialog.getByLabel("时区", { exact: true }).fill("UTC");
    await settingsDialog.getByLabel("最多条数", { exact: true }).fill("12");
    await settingsDialog.getByLabel("主题", { exact: true }).fill("重点");
    await settingsDialog.getByRole("switch", { name: "生成后提交推送", exact: true }).click();
    await settingsDialog.getByRole("button", { name: "保存", exact: true }).click();
    await page.getByText("资讯设置已保存", { exact: true }).waitFor();
    await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();
    await page.reload();
    await page.getByRole("button", { name: "资讯设置", exact: true }).click();
    settingsDialog = page.getByRole("dialog", { name: "资讯设置", exact: true });
    assert.equal(await settingsDialog.getByLabel("目标语言", { exact: true }).inputValue(), "zh-TW");
    assert.equal(await settingsDialog.getByLabel("每日 AI 上限", { exact: true }).inputValue(), "7");
    assert.equal(await settingsDialog.getByLabel("保留天数", { exact: true }).inputValue(), "45");
    assert.equal(await settingsDialog.getByRole("switch", { name: "启用定时简报", exact: true }).getAttribute("aria-checked"), "true");
    assert.equal(await settingsDialog.getByLabel("小时", { exact: true }).inputValue(), "8");
    assert.equal(await settingsDialog.getByLabel("分钟", { exact: true }).inputValue(), "30");
    assert.equal(await settingsDialog.getByLabel("时区", { exact: true }).inputValue(), "UTC");
    assert.equal(await settingsDialog.getByLabel("最多条数", { exact: true }).inputValue(), "12");
    assert.equal(await settingsDialog.getByLabel("主题", { exact: true }).inputValue(), "重点");
    assert.equal(await settingsDialog.getByRole("switch", { name: "生成后提交推送", exact: true }).getAttribute("aria-checked"), "true");
    await settingsDialog.getByRole("switch", { name: "启用定时简报", exact: true }).click();
    await settingsDialog.getByRole("button", { name: "保存", exact: true }).click();
    await page.getByText("资讯设置已保存", { exact: true }).waitFor();
    await page.getByRole("button", { name: "关闭业务设置", exact: true }).click();

    await page.goto(`${baseURL}/news`);
    await page.getByRole("heading", { name: "资讯", exact: true }).waitFor();
    await page.getByRole("button", { name: "订阅源", exact: true }).click();
    for (const source of [
      { name: "浏览器 RSS", url: `${fixtureURL}/rss.xml`, topic: "科技" },
      { name: "浏览器 Atom", url: `${fixtureURL}/atom.xml`, topic: "市场" }
    ]) {
      await page.getByRole("button", { name: "添加订阅源", exact: true }).click();
      await page.getByLabel("名称", { exact: true }).fill(source.name);
      await page.locator("form select").selectOption("rss");
      await page.getByLabel("订阅地址", { exact: true }).fill(source.url);
      await page.getByLabel("主题", { exact: true }).fill(source.topic);
      await page.getByLabel("刷新间隔（分钟）", { exact: true }).fill("1440");
      await page.getByRole("switch", { name: "启用订阅源", exact: true }).click();
      const saved = page.waitForResponse((response) => response.url().endsWith("/api/modules/news/sources") && response.request().method() === "POST");
      await page.getByRole("button", { name: "保存", exact: true }).click();
      assert.equal((await saved).status(), 201);
      await page.getByText(source.name, { exact: true }).waitFor();
    }

    await page.getByRole("button", { name: "关注规则", exact: true }).click();
    await page.getByRole("button", { name: "添加规则", exact: true }).click();
    await page.getByLabel("名称", { exact: true }).fill("量子关注");
    await page.getByLabel("主题", { exact: true }).fill("重点");
    await page.getByLabel("包含关键词", { exact: true }).fill("Quantum, 量子");
    await page.getByLabel("排除关键词", { exact: true }).fill("广告");
    await page.getByLabel("浏览器 RSS", { exact: true }).check();
    const savedRule = page.waitForResponse((response) => response.url().endsWith("/api/modules/news/rules") && response.request().method() === "POST");
    await page.getByRole("button", { name: "保存", exact: true }).click();
    assert.equal((await savedRule).status(), 201);
    await page.getByText("量子关注", { exact: true }).waitFor();

    await page.getByRole("button", { name: "订阅源", exact: true }).click();
    for (const name of ["浏览器 RSS", "浏览器 Atom"]) {
      const row = page.getByText(name, { exact: true }).locator("xpath=ancestor::div[contains(@class, 'sm:flex-row')][1]");
      const enabled = page.waitForResponse((response) => /\/api\/modules\/news\/sources\/[^/]+$/.test(response.url()) && response.request().method() === "PUT");
      await row.getByRole("button", { name: "启用", exact: true }).click();
      assert.equal((await enabled).status(), 200);
      await row.getByRole("button", { name: "停用", exact: true }).waitFor();
    }
    for (let attempt = 0; attempt < 2; attempt++) {
      const refreshed = page.waitForResponse((response) => response.url().endsWith("/api/modules/news/refresh") && response.request().method() === "POST");
      await page.getByRole("button", { name: "全部刷新", exact: true }).click();
      assert.equal((await refreshed).status(), 200);
      await page.getByRole("button", { name: "全部刷新", exact: true }).waitFor({ state: "visible" });
    }
    assert(feedRequests >= 4, "both feeds should be requested twice");
    const stats = await (await page.request.get(`${baseURL}/api/modules/news/stats`)).json();
    assert.equal(stats.articles, 37, "repeat refresh must not duplicate articles");

    await page.getByRole("button", { name: "资讯", exact: true }).click();
    await page.getByText("Quantum Browser Story", { exact: true }).waitFor();
    await page.getByRole("button", { name: "加载更多", exact: true }).click();
    await page.getByText("Browser Atom Science", { exact: true }).waitFor();
    assert.equal(await page.locator("article").count(), 37, "article cursor should load the second page");

    const search = page.getByPlaceholder("搜索标题或内容", { exact: true });
    await search.fill("Quantum Browser Story");
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await page.getByText("Quantum Browser Story", { exact: true }).waitFor();
    assert.equal(await page.locator("article").count(), 1);
    await search.fill("");
    await page.getByLabel("已匹配", { exact: true }).check();
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await page.getByText("Quantum Browser Story", { exact: true }).waitFor();
    assert.equal(await page.locator("article").count(), 1, "matched filter should apply the saved rule");

    const quantum = page.locator("article", { hasText: "Quantum Browser Story" });
    await quantum.getByRole("button", { name: /Quantum Browser Story/ }).first().click();
    await quantum.getByText("Original body quantum for browser regression.", { exact: true }).waitFor();
    assert(await quantum.locator(`a[href="${fixtureURL}/articles/rss-01"]`).count() > 0, "article detail should retain the original URL");
    await quantum.getByRole("button", { name: "标为已读", exact: true }).click();
    await quantum.getByRole("button", { name: "标为未读", exact: true }).waitFor();
    await quantum.getByRole("button", { name: "收藏", exact: true }).click();
    await quantum.getByRole("button", { name: "取消收藏", exact: true }).waitFor();
    const beforeAnalyze = aiRequests;
    const firstAnalyze = page.waitForResponse((response) => /\/api\/modules\/news\/articles\/[^/]+\/analyze$/.test(response.url()) && response.request().method() === "POST");
    await quantum.getByRole("button", { name: "AI 分析", exact: true }).click();
    assert.equal((await firstAnalyze).status(), 200);
    await quantum.getByText("浏览器回归译文。", { exact: true }).waitFor();
    await quantum.getByText("浏览器回归摘要。", { exact: true }).waitFor();
    assert.equal(aiRequests, beforeAnalyze + 1);
    const secondAnalyze = page.waitForResponse((response) => /\/api\/modules\/news\/articles\/[^/]+\/analyze$/.test(response.url()) && response.request().method() === "POST");
    await quantum.getByRole("button", { name: "AI 分析", exact: true }).click();
    assert.equal((await secondAnalyze).status(), 200);
    assert.equal(aiRequests, beforeAnalyze + 1, "second analysis should use the cached result");

    await page.getByRole("button", { name: "简报", exact: true }).click();
    await page.getByLabel("主题", { exact: true }).fill("重点");
    await page.locator("form select").selectOption("24");
    await page.getByLabel("最多条数", { exact: true }).fill("10");
    await page.getByRole("switch", { name: "提交推送", exact: true }).click();
    const createdDigest = page.waitForResponse((response) => response.url().endsWith("/api/modules/news/digests") && response.request().method() === "POST");
    await page.getByRole("button", { name: "生成简报", exact: true }).click();
    assert.equal((await createdDigest).status(), 201);
    await page.getByText("推送已提交", { exact: true }).first().waitFor();
    assert(await page.locator(`a[href="${fixtureURL}/articles/rss-01"]`).count() > 0, "digest should retain the article URL");
    const submittedPush = page.waitForResponse((response) => /\/api\/modules\/news\/digests\/[^/]+\/push$/.test(response.url()) && response.request().method() === "POST");
    await page.getByRole("button", { name: "发送推送", exact: true }).first().click();
    assert.equal((await submittedPush).status(), 200);
    const notifications = await (await page.request.get(`${baseURL}/api/notifications?limit=100`)).json();
    assert(notifications.items.length > 0, "digest push should create a local notification");
    const telegramAfterPush = await (await page.request.get(`${baseURL}/api/settings/telegram`)).json();
    assert.equal(telegramAfterPush.enabled, false);
    assert.equal(telegramAfterPush.hasBotToken, false);

    await page.getByRole("button", { name: "资讯", exact: true }).click();
    await page.getByLabel("已匹配", { exact: true }).uncheck();
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    for (const width of [1440, 390, 360]) {
      await page.setViewportSize({ width, height: width < 600 ? 844 : 1000 });
      if (width < 600) await page.waitForFunction(() => document.querySelector(".workspace-sidebar")?.getBoundingClientRect().width <= 65);
      for (const dark of [false, true]) {
        if (await page.evaluate(() => document.documentElement.classList.contains("dark")) !== dark) {
          await page.getByRole("button", { name: "切换主题", exact: true }).click();
          await page.waitForFunction((expected) => document.documentElement.classList.contains("dark") === expected, dark);
        }
        for (const text of ["", "搜索标题或内容"]) {
          await search.fill(text);
          await expectSearchIconSpacing(search, `${width}px ${dark ? "dark" : "light"} ${text ? "value" : "placeholder"}`);
        }
        const label = `${width}px ${dark ? "dark" : "light"}`;
        await page.getByRole("button", { name: "订阅源", exact: true }).click();
        await page.getByRole("button", { name: "添加订阅源", exact: true }).click();
        await expectFormControlStyles(page, page.getByLabel("名称", { exact: true }), `${label} source`);
        await page.locator("form").screenshot({ path: `/tmp/workbench-news-source-${width}-${dark ? "dark" : "light"}.png` });
        const sourceKind = page.getByLabel("类型", { exact: true });
        for (const kind of ["rss", "rsshub", "google", "yahoo"]) {
          await sourceKind.selectOption(kind);
          assert.equal(await page.getByLabel("订阅地址", { exact: true }).count(), kind === "rss" || kind === "rsshub" ? 1 : 0);
          assert.equal(await page.getByLabel("查询词", { exact: true }).count(), kind === "google" ? 1 : 0);
        }
        await page.getByRole("button", { name: "取消", exact: true }).click();
        await page.getByRole("button", { name: "关注规则", exact: true }).click();
        await page.getByRole("button", { name: "添加规则", exact: true }).click();
        await expectFormControlStyles(page, page.getByLabel("名称", { exact: true }), `${label} rule`);
        await page.getByRole("button", { name: "取消", exact: true }).click();
        await page.getByRole("button", { name: "简报", exact: true }).click();
        await expectFormControlStyles(page, page.getByLabel("主题", { exact: true }), `${label} digest`);
        await page.getByRole("button", { name: "资讯", exact: true }).click();
      }
      await search.fill("");
      const overflow = await page.evaluate(() => ({
        page: { scrollWidth: document.documentElement.scrollWidth, innerWidth },
        elements: [...document.querySelectorAll("body *")].map((element) => {
          const box = element.getBoundingClientRect();
          return { tag: element.tagName, className: element.className, text: element.textContent?.trim().slice(0, 80), left: box.left, right: box.right, scrollWidth: element.scrollWidth, clientWidth: element.clientWidth };
        }).filter((item) => item.right > innerWidth + 1 || item.left < -1 || item.scrollWidth > item.clientWidth + 1).slice(0, 12)
      }));
      assert.equal(overflow.page.scrollWidth > overflow.page.innerWidth, false, `${width}px news page overflow: ${JSON.stringify(overflow)}`);
      await page.screenshot({ path: `/tmp/workbench-news-${width}.png`, fullPage: true, animations: "disabled" });
    }

    await page.goto(`${baseURL}/settings?tab=modules`);
    const newsSwitch = page.getByRole("switch", { name: "资讯", exact: true });
    await newsSwitch.click();
    await page.waitForFunction(() => document.querySelector('[role="switch"][aria-label="资讯"]')?.getAttribute("aria-checked") === "false");
    assert.equal(await page.getByRole("link", { name: "资讯", exact: true }).count(), 0);
    assert.equal((await page.request.get(`${baseURL}/api/modules/news/settings`)).status(), 503);
    await page.getByRole("button", { name: "资讯设置", exact: true }).click();
    await page.getByText("启用后可配置采集、AI 和简报。", { exact: true }).waitFor();

    assert.deepEqual(fixtureErrors, []);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(unexpectedBrowserRequests, []);
    console.log("PASS: news sources/refresh deduplication, rules/search/pagination, read/star/detail, AI cache, digest/local push, settings persistence, module gate, desktop/mobile layout, search icon spacing and consistent form controls in light/dark themes; no real outbound delivery.");
  } catch (error) {
    if (output) error.message += `\nTemporary Workbench output:\n${output}`;
    throw error;
  } finally {
    if (browser) await browser.close();
    await stopProcess(app);
    fixture.closeAllConnections?.();
    await new Promise((resolve) => fixture.close(resolve));
    await rm(directory, { recursive: true, force: true });
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
