import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { aiSettingsSchema, aiModelsSchema } from "../src/features/settings/schema.ts";
import { aiStatusSchema } from "../src/shared/schema.ts";

const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)), configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false }, appType: "custom",
});
after(() => server.close());
const { fetchAIModels, saveAISettings, testAISettings } = await server.ssrLoadModule("/src/features/settings/queries.ts");
const { streamChat, aiStatusQueryOptions } = await server.ssrLoadModule("/src/features/ai/queries.ts");

const connection = { baseUrl: "https://draft.invalid/v1", apiKey: "unsaved-key", clearApiKey: false };
const input = { ...connection, enabled: true, model: "model-b", models: ["model-a", "model-b"] };

test("AI discovery sends the draft connection without saving models or changing input", async (t) => {
  t.mock.method(globalThis, "fetch", async (path, options) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    assert.equal(path, "/api/settings/ai/models");
    assert.equal(options.method, "POST");
    assert.deepEqual(JSON.parse(options.body), connection);
    return Response.json({ models: ["model-a", "model-b", "model-c"] });
  });
  const snapshot = structuredClone(connection);
  assert.deepEqual(await fetchAIModels(connection), { models: ["model-a", "model-b", "model-c"] });
  assert.deepEqual(connection, snapshot);
});

test("AI save and connection test send all allowed models and the selected default", async (t) => {
  const paths = [];
  t.mock.method(globalThis, "fetch", async (path, options) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    paths.push(path);
    assert.deepEqual(JSON.parse(options.body), input);
    return Response.json(path.endsWith("/test") ? { latencyMs: 25 } : { saved: true });
  });
  assert.deepEqual(await saveAISettings(input), { saved: true });
  assert.deepEqual(await testAISettings(input), { latencyMs: 25 });
  assert.deepEqual(paths, ["/api/settings/ai", "/api/settings/ai/test"]);
});

test("AI production schemas reject malformed model lists", async (t) => {
  assert.equal(aiModelsSchema.safeParse({ models: [42] }).success, false);
  assert.equal(aiModelsSchema.safeParse({ models: ["x".repeat(201)] }).success, false);
  assert.equal(aiSettingsSchema.safeParse({ ...input, hasApiKey: true }).success, true);
  assert.equal(aiSettingsSchema.safeParse({ ...input, hasApiKey: true, models: Array(51).fill("model") }).success, false);
  t.mock.method(globalThis, "fetch", async () => Response.json({ models: [42] }));
  await assert.rejects(fetchAIModels(connection));
});

test("AI status exposes model IDs and default without retaining admin credentials", async (t) => {
  const status = { available: true, models: ["model-a", "model-b"], defaultModel: "model-a" };
  t.mock.method(globalThis, "fetch", async (path) => {
    assert.equal(path, "/api/ai/status");
    return Response.json({ ...status, apiKey: "must-not-be-used", baseUrl: "private" });
  });
  assert.deepEqual(await aiStatusQueryOptions().queryFn(), status);
  assert.equal(aiStatusSchema.safeParse({ available: true, models: [false], defaultModel: "model-a" }).success, false);
});

test("AI turns switch explicit models with the same conversation; default omits model", async (t) => {
  const models = ["model-a", "model-b", undefined];
  const requests = [];
  t.mock.method(globalThis, "fetch", async (path, options) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    assert.equal(path, "/api/chat/stream");
    requests.push(JSON.parse(options.body));
    return new Response('event: chat.completed\ndata: {"conversationId":"same-conversation","messageId":"reply","text":"ok","usage":{}}\n\n', {
      headers: { "content-type": "text/event-stream" },
    });
  });
  for (const model of models) {
    const result = await streamChat("same-conversation", "continue", undefined, model, () => {}, () => {}, () => {});
    assert.equal(result.text, "ok");
  }
  assert.deepEqual(requests.map((request) => request.model), models);
  assert.ok(requests.every((request) => request.conversationId === "same-conversation"));
  assert.equal(Object.hasOwn(requests[2], "model"), false);
});
