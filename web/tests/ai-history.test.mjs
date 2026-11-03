import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { createServer } from "vite";

const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)),
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: "custom",
});
after(() => server.close());
const { conversationsQueryOptions, conversationMessagesQueryOptions } = await server.ssrLoadModule("/src/features/ai/queries.ts");

function observe(t, options) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const observer = new InfiniteQueryObserver(client, { ...options, enabled: false });
  t.after(() => {
    observer.destroy();
    client.clear();
  });
  return observer;
}

const conversation = (id) => ({ id, title: `Conversation ${id}`, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" });

test("AI conversations fetch the 101st conversation and preserve cursor encoding", async (t) => {
  const cursor = "older+page/=";
  const paths = [];
  t.mock.method(globalThis, "fetch", async (path) => {
    paths.push(path);
    const query = new URL(path, "https://workbench.test").searchParams;
    assert.equal(query.get("limit"), "100");
    if (!query.has("cursor")) {
      return Response.json({ items: Array.from({ length: 100 }, (_, index) => conversation(String(index))), nextCursor: cursor });
    }
    assert.equal(query.get("cursor"), cursor);
    return Response.json({ items: [conversation("99"), conversation("100")] });
  });
  const observer = observe(t, conversationsQueryOptions());
  const first = await observer.refetch({ throwOnError: true });
  assert.equal(first.data.length, 100);
  assert.equal(first.hasNextPage, true);
  const second = await observer.fetchNextPage({ throwOnError: true });
  assert.equal(second.data.length, 101);
  assert.equal(second.data.at(-1).id, "100");
  assert.equal(second.hasNextPage, false);
  assert.equal(paths.length, 2);
});

test("AI messages prepend the 51st older message in chronological order", async (t) => {
  const cursor = "older-message";
  const message = (index) => ({ role: index % 2 ? "assistant" : "user", content: `Message ${index}` });
  t.mock.method(globalThis, "fetch", async (path) => {
    const url = new URL(path, "https://workbench.test");
    assert.equal(url.pathname, "/api/conversations/id%2Fwith%20spaces/messages");
    assert.equal(url.searchParams.get("limit"), "50");
    if (!url.searchParams.has("cursor")) {
      return Response.json({ messages: Array.from({ length: 50 }, (_, index) => message(index + 1)), nextCursor: cursor });
    }
    assert.equal(url.searchParams.get("cursor"), cursor);
    return Response.json({ messages: [message(0)] });
  });
  const observer = observe(t, conversationMessagesQueryOptions("id/with spaces"));
  const first = await observer.refetch({ throwOnError: true });
  assert.equal(first.data.length, 50);
  assert.equal(first.data[0].content, "Message 1");
  assert.equal(first.hasNextPage, true);
  const second = await observer.fetchNextPage({ throwOnError: true });
  assert.deepEqual(second.data.map((item) => item.content), Array.from({ length: 51 }, (_, index) => `Message ${index}`));
  assert.equal(second.hasNextPage, false);
});

test("AI older-message failure preserves loaded history and retries the same cursor", async (t) => {
  let attempts = 0;
  t.mock.method(globalThis, "fetch", async (path) => {
    const query = new URL(path, "https://workbench.test").searchParams;
    if (!query.has("cursor")) return Response.json({ messages: [{ role: "assistant", content: "Newest" }], nextCursor: "older" });
    assert.equal(query.get("cursor"), "older");
    attempts += 1;
    if (attempts === 1) return Response.json({ code: "unavailable", message: "Try again" }, { status: 503 });
    return Response.json({ messages: [{ role: "user", content: "Oldest" }] });
  });
  const observer = observe(t, conversationMessagesQueryOptions("conversation"));
  await observer.refetch({ throwOnError: true });
  const failed = await observer.fetchNextPage();
  assert.equal(failed.isFetchNextPageError, true);
  assert.equal(failed.data[0].content, "Newest");
  const retried = await observer.fetchNextPage({ throwOnError: true });
  assert.deepEqual(retried.data.map((item) => item.content), ["Oldest", "Newest"]);
  assert.equal(attempts, 2);
});
