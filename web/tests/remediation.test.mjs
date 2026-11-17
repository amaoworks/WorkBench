import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { safeExternalURL } from "../src/shared/url.ts";
import { notificationStreamEvent } from "../src/features/notifications/streamEvent.ts";
import { futuConnectionDetail } from "../src/modules/investment/futuState.ts";

const repo = join(dirname(fileURLToPath(import.meta.url)), "../..");
const read = (path) => readFileSync(join(repo, path), "utf8");

function walk(dir, suffix) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...walk(path, suffix));
    else if (name.endsWith(suffix)) out.push(path);
  }
  return out;
}

function cssBlock(css, header) {
  const at = css.indexOf(header);
  assert.notEqual(at, -1, header);
  const open = css.indexOf("{", at);
  let depth = 0;
  for (let i = open; i < css.length; i++) {
    if (css[i] === "{") depth++;
    else if (css[i] === "}") {
      depth--;
      if (depth === 0) return css.slice(open + 1, i);
    }
  }
  throw new Error(`unclosed ${header}`);
}

function cssVars(block) {
  const vars = new Map();
  for (const match of block.matchAll(/--([a-z0-9-]+)\s*:\s*([^;]+);/g)) vars.set(`--${match[1]}`, match[2].trim());
  return vars;
}

function resolveVar(name, vars, seen = new Set()) {
  const raw = vars.get(name);
  assert.ok(raw, name);
  const ref = raw.match(/^var\((--[a-z0-9-]+)\)$/);
  if (!ref) return raw;
  assert.equal(seen.has(name), false);
  seen.add(name);
  return resolveVar(ref[1], vars, seen);
}

function channel(value) {
  const unit = value / 255;
  return unit <= 0.04045 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4;
}

function contrast(fill, text) {
  const parse = (hex) => {
    const match = hex.match(/^#([0-9a-f]{6})$/i);
    assert.ok(match, hex);
    const n = Number.parseInt(match[1], 16);
    return [channel((n >> 16) & 255), channel((n >> 8) & 255), channel(n & 255)];
  };
  const luminance = ([r, g, b]) => 0.2126 * r + 0.7152 * g + 0.0722 * b;
  const [lighter, darker] = [luminance(parse(fill)), luminance(parse(text))].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
}

test("article, digest, and brief links allow only http(s)", () => {
  for (const value of ["https://example.com/a?b=1", "http://127.0.0.1/path"]) {
    const href = safeExternalURL(value);
    assert.equal(typeof href, "string");
    assert.match(href, /^https?:\/\//);
    assert.equal(new URL(href).protocol === "http:" || new URL(href).protocol === "https:", true);
  }
  for (const value of ["javascript:alert(1)", "data:text/html,hi", "ftp://example.com/file", "mailto:a@b.c", "not a url"]) {
    assert.equal(safeExternalURL(value), null);
  }
});

test("claude primary button text contrasts at least 4.5:1", () => {
  const css = read("web/src/styles.css");
  const light = cssVars(cssBlock(css, 'html[data-palette="claude"] {'));
  const dark = cssVars(cssBlock(css, 'html[data-palette="claude"].dark, html[data-palette="claude"] .dark {'));
  assert.ok(contrast(resolveVar("--cta", light), resolveVar("--on-cta", light)) >= 4.5);
  assert.ok(contrast(resolveVar("--cta", dark), resolveVar("--on-cta", dark)) >= 4.5);
});

test("Futu connection detail names a logged-in session separately from quote login", () => {
  assert.equal(futuConnectionDetail({ connected: true, qotLogined: true, serviceState: "running" }), "行情已登录。");
  assert.equal(futuConnectionDetail({ connected: true, qotLogined: false, serviceState: "running" }), "服务已启动，等待行情登录。");
  assert.equal(futuConnectionDetail({ connected: false, qotLogined: false, serviceState: "stopped" }), "服务已停止");
  assert.equal(futuConnectionDetail({ connected: false, qotLogined: true, serviceState: "error" }), "服务启动失败");
});

test("notification stream refreshes on first open and reconnect, without polling on errors", () => {
  for (const events of [["open"], ["error", "error", "open"], ["open", "error", "open"]]) {
    assert.deepEqual(events.map((event) => notificationStreamEvent(event).refresh), events.map((event) => event === "open"));
  }
});

test("shipped surfaces match the remediation copy and wiring", () => {
  const today = read("web/src/modules/todo/TodoTodayWidget.tsx");
  assert.match(today, /className="ov-note field-error" role="alert"/);
  const sources = walk(join(repo, "web/src"), ".tsx").concat(walk(join(repo, "web/src"), ".ts"));
  for (const path of sources) assert.equal(readFileSync(path, "utf8").includes("studio-button"), false, path);
  assert.match(read("web/src/features/dashboard/DashboardPage.tsx"), /<h1>总览<\/h1>/);
  assert.match(read("web/src/app/LoginPage.tsx"), /<h1>登录<\/h1>/);
  const newsDir = join(repo, "web/src/modules/news");
  for (const path of walk(newsDir, ".tsx").concat(walk(newsDir, ".ts"))) {
    const text = readFileSync(path, "utf8");
    assert.equal(text.includes("主题"), false, path);
  }
  assert.match(read("web/src/modules/news/SourcesTab.tsx"), /label="话题"/);
  assert.match(read("web/eslint.config.js"), /"@typescript-eslint\/no-explicit-any": "error"/);
  const dashboard = read("web/src/features/dashboard/DashboardPage.tsx");
  assert.match(dashboard, /widgetMetaRegistry/);
  assert.match(dashboard, /summaryOrder/);
  assert.equal(/const\s+\w*[Ii]cons?\s*=\s*\{/.test(dashboard), false);
  assert.equal(/summaryOrder\s*=\s*\[/.test(dashboard), false);
  for (const path of walk(join(repo, "web/src/features"), ".tsx").concat(walk(join(repo, "web/src/features"), ".ts"))) {
    const text = readFileSync(path, "utf8");
    assert.equal(/useModules[\s\S]{0,80}from ["'][^"']*\/app\//.test(text), false, path);
    assert.equal(/from ["'][^"']*\/app\/queries["']/.test(text), false, path);
  }
  const history = read("web/src/modules/investment/NetWorthHistory.tsx");
  assert.match(history, /accountLabels/);
  assert.equal(history.includes("hash.slice"), false);
  const named = [
    "scripts/frontend-ux.e2e.cjs",
    "scripts/investment-page.e2e.cjs",
    "scripts/investment-startup.e2e.cjs",
    "scripts/module-platform.e2e.cjs",
    "scripts/README.md",
  ];
  for (const path of named) {
    const text = read(path);
    assert.match(text, /总览/);
    assert.equal(text.includes("今天，一目了然"), false, path);
    assert.equal(text.includes("可修改配置和连接。"), false, path);
  }
  assert.equal(read("scripts/external-module.e2e.cjs").includes("可修改配置和连接。"), false);
  assert.equal(read("scripts/news.e2e.cjs").includes('getByLabel("主题"'), false);
  assert.match(read("scripts/news.e2e.cjs"), /getByLabel\("话题"/);
  assert.match(read("scripts/frontend-ux.e2e.cjs"), /getByLabel\("话题"/);
  assert.equal(read("web/src/app/Layout.tsx").includes("在后台设置中启用"), false);
  const multiUser = read("doc/multi-user.md");
  assert.match(multiUser, /modules\/investment\.md#行情来源与权限/);
  assert.equal(multiUser.includes("| 未绑定 Schwab |"), false);
  assert.match(read("web/src/modules/investment/FutuSettings.tsx"), /futuConnectionDetail\(initial\)/);
  for (const path of ["scripts/news.e2e.cjs", "scripts/settings.e2e.cjs", "scripts/wallos.e2e.cjs"]) {
    assert.equal(read(path).includes("在后台设置中启用。"), false, path);
  }
  const alertsWidget = read("web/src/modules/investment/InvestmentAlertsWidget.tsx");
  assert.equal(alertsWidget.includes("lastError && <p className=\"ov-note ov-down\""), false);
  assert.match(alertsWidget, /lastError && <p className="ov-note field-error" role="alert"/);
  const monitor = read("web/src/modules/investment/InvestmentMonitor.tsx");
  assert.equal(monitor.includes('rule.enabled ? "暂停" : "启用"'), false);
  assert.match(monitor, /label=\{`启用 \$\{rule\.symbol\}`\}/);
});
