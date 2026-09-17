import assert from "node:assert/strict";
import { test } from "node:test";
import { api, apiValidated, APIError } from "../src/shared/api.ts";
import { taskSchema } from "../src/modules/todo/schema.ts";
import { moduleSchema } from "../src/shared/schema.ts";

test("validated API rejects a successful but incompatible response", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ id: "task" }));
  await assert.rejects(apiValidated("/fixture", taskSchema));
});

test("202 responses still require schema validation", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ pending: true }, { status: 202 }));
  await assert.rejects(apiValidated("/fixture", moduleSchema));
});

test("204 does not attempt to decode JSON", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response(null, { status: 204 }));
  assert.equal(await api("/fixture"), undefined);
});

test("backend errors preserve status and code", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ code: "module_lifecycle_failed", message: "retry" }, { status: 503 }));
  await assert.rejects(apiValidated("/fixture", moduleSchema), (error) => error instanceof APIError && error.status === 503 && error.code === "module_lifecycle_failed");
});
