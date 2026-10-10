import assert from "node:assert/strict";
import { test } from "node:test";
import { investmentViewPreferencesSchema } from "../src/modules/investment/schema.ts";

test("view preferences ignore terminal-owned entries and metadata", () => {
  const parsed = investmentViewPreferencesSchema.parse({
    entries: {
      view: { revision: 7, value: { tab: "monitor", expanded: true } },
      chart: { revision: "future", value: { resolution: "2H", fields: ["new"] }, metadata: { version: 2 } },
      layout: { metadata: "new", value: ["future-layout"] },
      account: { value: { id: "future-account" } },
      "tv:new-setting": { format: 2, value: { nested: true } },
    },
  });

  assert.deepEqual(parsed, {
    entries: { view: { revision: 7, value: { tab: "monitor", expanded: true } } },
  });
});

test("view preferences reject invalid view fields and revision", () => {
  for (const view of [
    { revision: 1, value: { tab: "future", expanded: false } },
    { revision: 1, value: { tab: "terminal", expanded: "yes" } },
    { revision: 0, value: { tab: "terminal", expanded: false } },
    { revision: 1.5, value: { tab: "terminal", expanded: false } },
    { revision: Number.MAX_SAFE_INTEGER + 1, value: { tab: "terminal", expanded: false } },
  ]) {
    assert.equal(investmentViewPreferencesSchema.safeParse({ entries: { view } }).success, false);
  }
});

test("view preferences accept an absent view and a null tombstone", () => {
  assert.deepEqual(investmentViewPreferencesSchema.parse({ entries: {} }), { entries: {} });
  assert.deepEqual(
    investmentViewPreferencesSchema.parse({ entries: { view: { revision: 9, value: null } } }),
    { entries: { view: { revision: 9, value: null } } },
  );
});
