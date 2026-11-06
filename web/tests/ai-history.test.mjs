import assert from "node:assert/strict";
import { after, test } from "node:test";
import { fileURLToPath } from "node:url";
import { InfiniteQueryObserver, QueryClient, focusManager } from "@tanstack/react-query";
import { readFileSync } from "node:fs";
import { createServer } from "vite";

const server = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)),
  configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false },
  appType: "custom",
});
after(() => server.close());
const {
  conversationsQueryOptions, conversationMessagesQueryOptions, appendConversationMessages, mergeConversationMessages,
} = await server.ssrLoadModule("/src/features/ai/queries.ts");

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
  const message = (index) => ({ id: `message-${index}`, role: index % 2 ? "assistant" : "user", content: `Message ${index}` });
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
    if (!query.has("cursor")) return Response.json({ messages: [{ id: "newest", role: "assistant", content: "Newest" }], nextCursor: "older" });
    assert.equal(query.get("cursor"), "older");
    attempts += 1;
    if (attempts === 1) return Response.json({ code: "unavailable", message: "Try again" }, { status: 503 });
    return Response.json({ messages: [{ id: "oldest", role: "user", content: "Oldest" }] });
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


function waitForResult(observer, predicate) {
  if (predicate(observer.getCurrentResult())) return Promise.resolve(observer.getCurrentResult());
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error("Query did not reach the expected state"));
    }, 2000);
    const unsubscribe = observer.subscribe((result) => {
      if (!predicate(result)) return;
      clearTimeout(timer);
      unsubscribe();
      resolve(result);
    });
  });
}

const tool = { callId: "call-1", name: "todo.create", label: "创建待办", risk: "low_write", status: "succeeded" };
const turn = (prefix) => [
  { id: `${prefix}-user`, role: "user", content: "重复的问题" },
  { id: `${prefix}-assistant`, role: "assistant", content: "重复的回答" },
];

test("AI panel keeps history enabled after local sends", () => {
  const panel = readFileSync(new URL("../src/features/ai/AIPanel.tsx", import.meta.url), "utf8");
  assert.match(panel, /useConversationMessages\(conversationId, open\)/);
  assert.match(panel, /mergeConversationMessages\(history.data \?\? \[\], messages, activeMessageIds\)/);
  assert.match(panel, /messages\.filter\(\(message\) => message\.active\)/);
  assert.match(panel, /active: false, content: `\$\{prefix\}请求失败/);
  assert.match(panel, /invalidateQueries\(\{ queryKey: \["ai", "conversation"\] \}\)/);
});

test("AI history refreshes on reopen and focus after sending, without merging repeated text", async (t) => {
  const rows = turn("original");
  let reads = 0;
  t.mock.method(globalThis, "fetch", async () => {
    reads += 1;
    return Response.json({ messages: rows });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 20_000, refetchOnWindowFocus: false } } });
  const options = conversationMessagesQueryOptions("conversation");
  const observer = new InfiniteQueryObserver(client, { ...options, enabled: true });
  const unsubscribe = observer.subscribe(() => {});
  client.mount();
  t.after(() => {
    unsubscribe();
    observer.destroy();
    client.unmount();
    client.clear();
    focusManager.setFocused(undefined);
  });
  await observer.refetch({ throwOnError: true });

  const desktop = turn("desktop");
  desktop[1].tools = [tool];
  appendConversationMessages(client, "conversation", desktop);
  rows.push(...turn("desktop"));
  await client.invalidateQueries({ queryKey: ["ai", "conversation", "conversation", "messages"] });
  assert.equal(observer.getCurrentResult().data.length, 4);
  assert.deepEqual(observer.getCurrentResult().data.at(-1).tools, [tool]);

  observer.setOptions({ ...options, enabled: false });
  const beforeClosedRefresh = reads;
  rows.push(...turn("mobile"));
  await client.invalidateQueries({ queryKey: ["ai", "conversation", "conversation", "messages"] });
  assert.equal(reads, beforeClosedRefresh, "closed panels do not fetch history");
  const reopening = waitForResult(observer, (result) => result.data?.length === rows.length && !result.isFetching);
  observer.setOptions({ ...options, enabled: true });
  const reopened = await reopening;
  assert.deepEqual(reopened.data.map(({ id }) => id), rows.map(({ id }) => id));

  focusManager.setFocused(false);
  rows.push(...turn("mobile-again"));
  const focused = waitForResult(observer, (result) => result.data?.length === rows.length && !result.isFetching);
  focusManager.setFocused(true);
  const result = await focused;
  assert.deepEqual(result.data.map(({ id }) => id), rows.map(({ id }) => id));
  assert.deepEqual(result.data.find(({ id }) => id === "desktop-assistant").tools, [tool]);
});

test("AI refresh retains paginated older history and updates a persisted assistant summary by ID", async (t) => {
  const rows = Array.from({ length: 80 }, (_, index) => ({
    id: `message-${index}`, role: index % 2 ? "assistant" : "user", content: `Message ${index}`,
  }));
  t.mock.method(globalThis, "fetch", async (path) => {
    const query = new URL(path, "https://workbench.test").searchParams;
    const end = Number(query.get("cursor") ?? rows.length);
    const start = Math.max(0, end - 50);
    return Response.json({ messages: rows.slice(start, end), ...(start ? { nextCursor: String(start) } : {}) });
  });
  const observer = observe(t, conversationMessagesQueryOptions("conversation"));
  await observer.refetch({ throwOnError: true });
  await observer.fetchNextPage({ throwOnError: true });
  rows[79] = { ...rows[79], content: "最终答案，而非临时工具摘要" };
  rows.push(...Array.from({ length: 110 }, (_, index) => ({ id: `remote-${index}`, role: "user", content: "相同文本" })));
  const refreshed = await observer.refetch({ throwOnError: true });
  assert.deepEqual(refreshed.data.map(({ id }) => id), rows.map(({ id }) => id));
  assert.equal(refreshed.data[79].content, rows[79].content);
  assert.equal(new Set(refreshed.data.map(({ id }) => id)).size, rows.length);
  const again = await observer.refetch({ throwOnError: true });
  assert.deepEqual(again.data, refreshed.data);
});

test("AI history refresh preserves active deltas and badges over a persisted tool summary", () => {
  const history = [
    ...turn("old"),
    { id: "current-user", role: "user", content: "重复的问题" },
    { id: "current-assistant", role: "assistant", content: "创建待办：已完成" },
  ];
  const local = [
    { id: "current-user", role: "user", content: "重复的问题" },
    { id: "current-assistant", role: "assistant", content: "正在生成的回答", tools: [tool] },
  ];
  const visible = mergeConversationMessages(history, local, local.map(({ id }) => id));
  assert.equal(visible.length, 4);
  assert.equal(visible.at(-1).content, "正在生成的回答");
  assert.deepEqual(visible.at(-1).tools, [tool]);
  assert.equal(history.at(-1).content, "创建待办：已完成", "merging does not mutate query data");
});

for (const refresh of ["reopen", "focus"]) {
  test(`AI interrupted stream recovers a persisted final answer on ${refresh}`, async (t) => {
    const rows = turn("interrupted");
    rows[1].content = "已保存的工具摘要";
    t.mock.method(globalThis, "fetch", async () => Response.json({ messages: rows }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
    const options = conversationMessagesQueryOptions("conversation");
    const observer = new InfiniteQueryObserver(client, { ...options, enabled: true });
    const unsubscribe = observer.subscribe(() => {});
    client.mount();
    t.after(() => {
      unsubscribe(); observer.destroy(); client.unmount(); client.clear(); focusManager.setFocused(undefined);
    });
    await observer.refetch({ throwOnError: true });
    const local = [
      { ...rows[0] },
      { ...rows[1], content: "不完整的回答\n\n请求失败：连接中断", tools: [tool] },
    ];
    if (refresh === "reopen") observer.setOptions({ ...options, enabled: false });
    else focusManager.setFocused(false);
    rows[1] = { ...rows[1], content: "服务器已保存的完整回答" };
    const refreshed = waitForResult(observer, (result) => result.data?.at(-1).content === rows[1].content && !result.isFetching);
    if (refresh === "reopen") observer.setOptions({ ...options, enabled: true });
    else focusManager.setFocused(true);
    const result = await refreshed;
    const visible = mergeConversationMessages(result.data, local);
    assert.equal(visible.length, 2);
    assert.equal(visible.at(-1).content, rows[1].content);
    assert.deepEqual(visible.at(-1).tools, [tool]);
    assert.match(local.at(-1).content, /请求失败/, "reconciliation does not mutate local state");
  });
}

test("AI failed local messages without a persisted counterpart remain visible", () => {
  const history = turn("old");
  const local = [
    { id: "unsaved-user", role: "user", content: "创建待办" },
    { id: "unsaved-assistant", role: "assistant", content: "请求失败：连接中断" },
  ];
  assert.deepEqual(mergeConversationMessages(history, local), [...history, ...local]);
  assert.deepEqual(mergeConversationMessages([...history, local[0]], local), [...history, ...local]);
});

test("AI new active turn does not revive an earlier failed override during completion cache updates", () => {
  const previous = turn("previous");
  const current = turn("current");
  const history = [...previous, ...current];
  const local = [
    previous[0], { ...previous[1], content: "请求失败：连接中断", tools: [tool] },
    current[0], { ...current[1], content: "正在生成的回答" },
  ];
  const activeIds = current.map(({ id }) => id);
  const visible = mergeConversationMessages(history, local, activeIds);
  assert.equal(visible[1].content, previous[1].content);
  assert.equal(visible[3].content, "正在生成的回答");
  assert.deepEqual(visible[1].tools, [tool]);
  // The active IDs stay attached to their messages until onSuccess removes that pair.
  const afterSuccess = mergeConversationMessages(history, local.slice(0, -2), activeIds);
  assert.equal(afterSuccess[1].content, previous[1].content);
  assert.equal(afterSuccess[3].content, current[1].content);
});

test("AI completion replaces a cached temporary summary and keeps tool badges after refetch", async (t) => {
  const client = new QueryClient();
  const options = conversationMessagesQueryOptions("conversation");
  const observer = new InfiniteQueryObserver(client, { ...options, enabled: false });
  const unsubscribe = observer.subscribe(() => {});
  t.after(() => { unsubscribe(); observer.destroy(); client.clear(); });
  let content = "创建待办：已完成";
  t.mock.method(globalThis, "fetch", async () => Response.json({ messages: [
    { id: "user", role: "user", content: "创建待办" }, { id: "assistant", role: "assistant", content },
  ] }));
  await observer.refetch({ throwOnError: true });
  content = "最终答案";
  appendConversationMessages(client, "conversation", [
    { id: "user", role: "user", content: "创建待办" },
    { id: "assistant", role: "assistant", content, tools: [tool] },
  ]);
  assert.equal(observer.getCurrentResult().data.at(-1).content, content);
  const refreshed = await observer.refetch({ throwOnError: true });
  assert.equal(refreshed.data.length, 2);
  assert.equal(refreshed.data.at(-1).content, content);
  assert.deepEqual(refreshed.data.at(-1).tools, [tool]);
});
