import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import * as shared from "../src/shared/schema.ts";
import * as settings from "../src/features/settings/schema.ts";
import * as todo from "../src/modules/todo/schema.ts";
import * as investment from "../src/modules/investment/schema.ts";
import * as news from "../src/modules/news/schema.ts";

const schemas = {
  authStatus: shared.authStatusSchema, modules: shared.modulesResponseSchema,
  module: shared.moduleSchema, dashboard: shared.dashboardSchema, widgets: shared.widgetCatalogSchema,
  notifications: shared.notificationsPageSchema, unreadCount: shared.unreadCountSchema,
  aiStatus: shared.aiStatusSchema, error: shared.apiErrorSchema, saved: shared.savedSchema,
  settings: settings.settingsSchema, appearance: settings.appearanceSchema, logging: settings.loggingSettingsSchema,
  backup: settings.backupSchema, task: todo.taskSchema, tasks: todo.tasksResponseSchema,
  summary: todo.summarySchema, todoToday: todo.todoTodaySchema, taskCounts: todo.taskCountsSchema,
  wallos: todo.wallosSettingsSchema, wallosSync: todo.wallosSyncSchema,
  schwab: investment.schwabSettingsSchema, futu: investment.futuSettingsSchema, overnight: investment.overnightSettingsSchema,
  investmentSnapshot: investment.overviewSnapshotSchema, investmentQuotes: investment.overviewQuotesSchema, investmentAlerts: investment.overviewAlertsSchema,
  investmentPositions: investment.positionsResponseSchema, investmentOrders: investment.ordersResponseSchema,
  investmentNetworth: investment.netWorthHistorySchema,
  investmentPreferences: investment.investmentViewPreferencesSchema, investmentPreferenceSaved: investment.investmentPreferenceSavedSchema,
  telegram: settings.telegramSettingsSchema, priceMonitor: investment.priceMonitorSchema, priceRule: investment.priceRuleSchema, priceHistory: investment.priceHistorySchema,
  newsSources: news.sourcesResponseSchema, newsSource: news.sourceSchema,
  newsRefreshSource: news.refreshSourceSchema, newsRefreshAll: news.refreshAllSchema,
  newsRules: news.rulesResponseSchema, newsRule: news.ruleSchema,
  newsArticles: news.articlesResponseSchema, newsArticle: news.articleSchema,
  newsSettings: news.settingsSchema, newsStats: news.statsSchema,
  newsDigests: news.digestsResponseSchema, newsDigest: news.digestSchema,
  newsBrief: news.newsBriefSchema
};

const temporary = mkdtempSync(join(tmpdir(), "workbench-contracts-"));
let samples;
try {
  const output = join(temporary, "responses.json");
  execFileSync(process.env.GO_BIN || "go", ["test", "./internal/app", "-run", "^TestFrontendContracts$", "-count=1"], {
    cwd: fileURLToPath(new URL("../../", import.meta.url)),
    env: { ...process.env, WORKBENCH_CONTRACT_OUTPUT: output },
    timeout: 120_000, stdio: "inherit"
  });
  samples = JSON.parse(readFileSync(output, "utf8"));
} finally {
  rmSync(temporary, { recursive: true, force: true });
}

test("every declared contract has a real backend response", () => {
  assert.deepEqual(new Set(samples.map((sample) => sample.schema)), new Set(Object.keys(schemas)));
});

for (const sample of samples) {
  test(`${sample.name}: HTTP ${sample.status} matches the production schema`, () => {
    assert.ok(schemas[sample.schema], `unknown contract ${sample.schema}`);
    const result = schemas[sample.schema].safeParse(sample.body);
    assert.equal(result.success, true, JSON.stringify(result.error?.issues));
  });
}

// Mutate actual responses so the test also proves that nested fields, optional
// populated fields and array entries are checked, not just the envelope.
function scalarPaths(value, prefix = []) {
  if (value === null || typeof value !== "object") return [prefix];
  return Object.entries(value).flatMap(([key, child]) => scalarPaths(child, [...prefix, key]));
}
for (const sample of samples) {
  test(`${sample.name}: detects field type drift`, () => {
    const schema = schemas[sample.schema];
    const parsed = schema.parse(sample.body);
    // Embedded widget data is opaque to the dashboard schema; it is checked per widget below.
    const opaque = (path) => sample.schema === "dashboard" && path[0] === "widgets" && path[2] === "data";
    for (const path of scalarPaths(parsed).filter((path) => !opaque(path))) {
      const damaged = structuredClone(parsed);
      let parent = damaged;
      for (const key of path.slice(0, -1)) parent = parent[key];
      parent[path.at(-1)] = { incompatible: true };
      assert.equal(schema.safeParse(damaged).success, false, `accepted changed ${path.join(".")}`);
    }
  });
}

for (const [name, path] of [
  ["module.enabled", ["observedEnabled"]],
  ["settings", ["logging", "level"]],
  ["task.created", ["id"]],
  ["schwab", ["reauthorizationRequired"]],
  ["futu", ["serviceState"]],
  ["tasks.paginated", ["items", 0, "title"]]
]) {
  test(`${name}: detects missing ${path.join(".")}`, () => {
    const sample = samples.find((item) => item.name === name);
    const damaged = structuredClone(sample.body);
    let parent = damaged;
    for (const key of path.slice(0, -1)) parent = parent[key];
    delete parent[path.at(-1)];
    assert.equal(schemas[sample.schema].safeParse(damaged).success, false);
  });
}

const widgetDataSchemas = {
  "investment.snapshot": investment.overviewSnapshotSchema, "investment.quotes": investment.overviewQuotesSchema,
  "investment.alerts": investment.overviewAlertsSchema, "todo.today": todo.todoTodaySchema, "news.brief": news.newsBriefSchema,
  "notifications.summary": shared.unreadCountSchema
};
test("dashboard include=data embeds each widget's own contract", () => {
  const widgets = samples.find((sample) => sample.name === "dashboard.data").body.widgets;
  assert.ok(widgets.some((widget) => widget.data !== undefined), "no widget data embedded");
  for (const widget of widgets) {
    if (widget.data === undefined) continue;
    const schema = widgetDataSchemas[widget.widgetKind];
    assert.ok(schema, `no schema for ${widget.widgetKind}`);
    const result = schema.safeParse(widget.data);
    assert.equal(result.success, true, `${widget.id}: ${JSON.stringify(result.error?.issues)}`);
  }
});

test("rejects unsupported enum and module contract versions", () => {
  const module = samples.find((sample) => sample.name === "module.enabled").body;
  assert.equal(shared.moduleSchema.safeParse({ ...module, contractVersion: 2 }).success, false);
  assert.equal(settings.loggingSettingsSchema.safeParse({ level: "trace" }).success, false);
  const futu = samples.find((sample) => sample.name === "futu").body;
  assert.equal(investment.futuSettingsSchema.safeParse({ ...futu, serviceState: "new-state" }).success, false);
});
