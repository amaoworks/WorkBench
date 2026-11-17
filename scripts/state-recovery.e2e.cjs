// Browser fixtures only: no workspace, provider credentials, or external service is used.
const assert = require("node:assert/strict");
const { readFile } = require("node:fs/promises");
const { createServer } = require("node:http");
const { join, extname } = require("node:path");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");

const dist = join(__dirname, "../internal/webui/dist");
const timestamp = "2026-01-01T00:00:00Z";
const admin = {
  id: "admin", username: "admin", displayName: "Admin", role: "admin", status: "active",
  mustChangePassword: false, createdAt: timestamp, updatedAt: timestamp,
};
let user = { ...admin, id: "alice", username: "alice", displayName: "Alice", role: "user" };
let notifications = [];
let notificationReads = 0;
let countReads = 0;
let savedInput;
let chatRequests = 0;
let chatStarted;
let chatResponse;
const chats = new Set();

function json(response, data) {
  response.writeHead(200, { "Content-Type": "application/json" });
  response.end(JSON.stringify(data));
}

const server = createServer(async (request, response) => {
  try {
    const path = new URL(request.url, "http://fixture.test").pathname;
    switch (path) {
      case "/api/auth/status":
        return json(response, { authenticated: true, mode: "password", mustChangePassword: false, user: admin });
      case "/api/auth/csrf": return json(response, { token: "fixture-csrf" });
      case "/api/settings": return json(response, { appearance: { theme: "light", motion: "reduced", palette: "default" } });
      case "/api/modules": return json(response, { items: [] });
      case "/api/notifications":
        notificationReads++;
        return json(response, { items: notifications });
      case "/api/notifications/unread-count":
        countReads++;
        return json(response, { count: notifications.length });
      case "/api/admin/users": return json(response, { items: [admin, user] });
      case "/api/admin/users/alice": {
        assert.equal(request.method, "PATCH");
        let body = "";
        for await (const chunk of request) body += chunk;
        savedInput = JSON.parse(body);
        user = { ...user, displayName: savedInput.displayName.trim(), role: savedInput.role };
        return json(response, { user });
      }
      case "/api/settings/telegram":
        return json(response, {
          enabled: false, chatId: "", hasBotToken: false, failedDeliveries: 0,
          status: { lastAttemptAt: null, lastSuccessAt: null, lastError: "" },
        });
      case "/api/ai/status": return json(response, { available: true, models: ["fixture"], defaultModel: "fixture" });
      case "/api/conversations": return json(response, { items: [] });
      case "/api/conversations/fixture/messages":
        return json(response, { messages: [{ id: "saved-user", role: "user", content: "已确认的消息" }] });
      case "/api/chat/stream":
        chatRequests++;
        chatResponse = response;
        chats.add(response);
        response.on("close", () => chats.delete(response));
        chatStarted();
        // Hold the response before chat.started until the browser cancels it.
        return;
    }
    if (path.startsWith("/api/")) throw new Error(`Unexpected API: ${path}`);
    const file = path.startsWith("/assets/") ? path.slice(1) : "index.html";
    const contentType = { ".js": "text/javascript", ".css": "text/css", ".woff2": "font/woff2" }[extname(file)] || "text/html";
    response.writeHead(200, { "Content-Type": contentType });
    response.end(await readFile(join(dist, file)));
  } catch (error) {
    response.writeHead(500);
    response.end(error.message);
  }
});

(async () => {
  let browser;
  try {
    await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
    const baseURL = `http://127.0.0.1:${server.address().port}`;
    browser = await chromium.launch({
      headless: true, executablePath: process.env.CHROMIUM_PATH || "/usr/bin/chromium", args: ["--no-sandbox"],
    });
    const page = await browser.newPage();
    page.setDefaultTimeout(10_000);
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.addInitScript(() => {
      window.EventSource = class extends EventTarget {
        constructor() {
          super();
          window.notificationStream = this;
        }
        emit(type) { this[`on${type}`]?.(new Event(type)); }
        close() {}
      };
    });

    await page.goto(`${baseURL}/notifications`);
    await page.getByText("暂无通知", { exact: true }).waitFor();
    notifications = [{
      id: "missed", sourceModule: "core", severity: "info", title: "断连期间的通知", content: "测试通知", createdAt: timestamp,
    }];
    let reads = notificationReads;
    let counts = countReads;
    await page.evaluate(() => {
      window.notificationStream.emit("error");
      window.notificationStream.emit("error");
      window.notificationStream.emit("open");
    });
    await page.getByText("断连期间的通知", { exact: true }).waitFor();
    assert.equal(notificationReads, reads + 1, "first successful connection must refresh the list once");
    assert.equal(countReads, counts + 1, "first successful connection must refresh the unread count once");
    notifications = [];
    reads = notificationReads;
    await page.evaluate(() => {
      window.notificationStream.emit("error");
      window.notificationStream.emit("open");
    });
    await page.getByText("暂无通知", { exact: true }).waitFor();
    assert.equal(notificationReads, reads + 1, "reconnect must refresh the list once");

    await page.goto(`${baseURL}/admin/settings?tab=users`);
    const editor = page.locator("form").filter({ has: page.getByRole("heading", { name: "alice", exact: true }) });
    const name = editor.getByLabel("显示名称", { exact: true });
    const save = editor.getByRole("button", { name: "保存", exact: true });
    for (const draft of ["  Alice Updated  ", "  Alice Updated "]) {
      await name.fill(draft);
      await save.click();
      await page.waitForFunction(() => {
        const input = [...document.querySelectorAll("input")].find((node) => node.value === "Alice Updated");
        return input && input.closest("form").querySelector('button[type="submit"], button:not([type])')?.disabled;
      });
      assert.equal(await name.inputValue(), "Alice Updated");
      assert.equal(await save.isDisabled(), true, "normalized save must clear the dirty state");
      assert.equal(savedInput.displayName, draft);
    }

    const openAI = page.getByRole("button", { name: "打开 AI", exact: true });
    const panel = page.getByRole("dialog", { name: /问 AI/ });
    await openAI.click();
    const message = panel.getByLabel("发送给 Workbench AI 的消息", { exact: true });
    await message.fill("未确认的消息必须保留");
    const requested = new Promise((resolve) => { chatStarted = resolve; });
    await panel.getByRole("button", { name: "发送消息", exact: true }).click();
    await requested;
    await panel.getByRole("button", { name: "关闭 AI 面板", exact: true }).click();
    await panel.waitFor({ state: "detached" });
    await openAI.click();
    await panel.getByText("未确认的消息必须保留", { exact: true }).waitFor();
    await panel.getByText("回复已中断", { exact: true }).waitFor();
    assert.equal(chatRequests, 1, "reopening must never resubmit an unconfirmed turn");

    await panel.getByRole("button", { name: "新建对话", exact: true }).click();
    await message.fill("已确认的消息");
    const nextRequest = new Promise((resolve) => { chatStarted = resolve; });
    await panel.getByRole("button", { name: "发送消息", exact: true }).click();
    await nextRequest;
    chatResponse.writeHead(200, { "Content-Type": "text/event-stream" });
    chatResponse.write('event: chat.started\ndata: {"conversationId":"fixture","messageId":"saved-user","assistantMessageId":"saved-assistant"}\n\n');
    chatResponse.write('event: chat.delta\ndata: {"text":"部分回复"}\n\n');
    await panel.locator(".ai-message").filter({ hasText: "部分回复" }).waitFor();
    await panel.getByRole("button", { name: "关闭 AI 面板", exact: true }).click();
    await panel.waitFor({ state: "detached" });
    await openAI.click();
    await panel.locator(".ai-message").filter({ hasText: "部分回复" }).filter({ hasText: "回复已中断" }).waitFor();
    assert.equal(await panel.getByText("已确认的消息", { exact: true }).count(), 1, "persisted and local user messages must merge by ID");
    assert.equal(chatRequests, 2);
    assert.deepEqual(errors, []);
    console.log("PASS: notification first connection/reconnect, normalized user saves, and AI cancellation before/after chat.started.");
  } finally {
    if (browser) await browser.close();
    for (const response of chats) response.destroy();
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
