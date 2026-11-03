import assert from "node:assert/strict";
import { after, test } from "node:test";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import {
  applyWidgetCompletion, emptyTaskDraft, findTaskForEdit, mergeTaskDraft, prepareTaskCreate, taskDraft, taskEditInput,
} from "../src/modules/todo/taskActions.ts";

const vite = await createServer({
  root: fileURLToPath(new URL("../", import.meta.url)), configFile: false,
  server: { middlewareMode: true, hmr: false, ws: false }, appType: "custom",
});
after(() => vite.close());
const { createTask, updateTask, loadTaskForEdit } = await vite.ssrLoadModule("/src/modules/todo/queries.ts");

const task = (changes = {}) => ({
  id: "task-1", title: "原始标题", description: "原始备注", priority: "none", category: "工作",
  dueAt: "2026-07-16T09:00:37.123Z", createdAt: "2026-07-01T00:00:00Z", updatedAt: "2026-07-01T00:00:00Z", revision: 1,
  ...changes,
});

test("widget completion then restore sends revisions 1 and 2 using the returned task", async (t) => {
  const original = { ...task(), overdue: true };
  let server = task();
  const requests = [];
  t.mock.method(globalThis, "fetch", async (path, init) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    assert.equal(path, `/api/modules/todo/tasks/${original.id}`);
    assert.equal(init.method, "PATCH");
    const request = JSON.parse(init.body);
    requests.push(request);
    assert.equal(request.revision, server.revision, "CAS must use the current server revision");
    server = { ...server, completedAt: request.completed ? "2026-07-16T10:00:00Z" : undefined, revision: server.revision + 1 };
    return Response.json(server);
  });
  const update = (item, completed) => updateTask({ id: item.id, completed, revision: item.revision });
  let done = applyWidgetCompletion([], original, await update(original, true), true);
  assert.equal(done[0].revision, 2);
  assert.equal(done[0].completedAt, server.completedAt);
  assert.equal(done[0].overdue, true, "widget-only metadata survives the task response");
  done = applyWidgetCompletion(done, done[0], await update(done[0], false), false);
  assert.deepEqual(requests.map(({ revision }) => revision), [1, 2]);
  assert.equal(done.length, 0);
  assert.equal(server.completedAt, undefined);
});

test("a lost create response retains the key and decoded request snapshot for manual retry", async (t) => {
  const draft = { ...emptyTaskDraft(), title: "创建待办", description: "备注", dueAt: "2026-07-16T09:00:00.000Z" };
  let keys = 0;
  const nextKey = () => `create-${++keys}`;
  let attempt = prepareTaskCreate(null, draft, nextKey);
  const originalBody = JSON.stringify(attempt);
  const requests = [];
  const stored = new Map();
  t.mock.method(globalThis, "fetch", async (path, init) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    assert.equal(path, "/api/modules/todo/tasks");
    assert.equal(init.method, "POST");
    const idempotencyKey = init.headers.get("Idempotency-Key");
    const body = JSON.parse(init.body);
    assert.equal("idempotencyKey" in body, false, "key belongs in the request header");
    requests.push({ idempotencyKey, body });
    if (!stored.has(idempotencyKey)) {
      stored.set(idempotencyKey, { body, task: task({ ...body, id: "created" }) });
      throw new TypeError("response lost after commit");
    }
    assert.deepEqual(body, stored.get(idempotencyKey).body);
    return Response.json(stored.get(idempotencyKey).task);
  });
  await assert.rejects(createTask(attempt), /response lost/);
  assert.equal(requests.length, 1, "no automatic replay");
  attempt = prepareTaskCreate(attempt, { ...draft }, nextKey);
  assert.equal(JSON.stringify(attempt), originalBody);
  const result = await createTask(attempt);
  assert.equal(result.id, "created");
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(keys, 1);
  assert.equal(stored.size, 1, "manual retry creates only one task");

  // A confirmed success ends the logical action, even if the next task has identical fields.
  const next = prepareTaskCreate(null, draft, nextKey);
  assert.notEqual(next.idempotencyKey, attempt.idempotencyKey);
});

test("changing each create field starts a new action and does not mutate the saved request", () => {
  const draft = { ...emptyTaskDraft(), title: "创建待办" };
  const changes = { title: "新标题", description: "新备注", dueAt: "2026-07-17T09:00:00.000Z", priority: "high", category: "生活" };
  let keys = 0;
  for (const [field, value] of Object.entries(changes)) {
    const saved = prepareTaskCreate(null, draft, () => `key-${++keys}`);
    const snapshot = structuredClone(saved);
    const changed = { ...draft, [field]: value };
    const next = prepareTaskCreate(saved, changed, () => `key-${++keys}`);
    assert.notEqual(next.idempotencyKey, saved.idempotencyKey, field);
    assert.equal(next[field], value);
    assert.deepEqual(saved, snapshot);
  }
  const mutable = { ...draft };
  const saved = prepareTaskCreate(null, mutable, () => "snapshot");
  mutable.title = "后续输入";
  assert.equal(saved.title, draft.title, "request must not alias mutable form values");
});

test("reading latest merges disjoint changes while preserving the local draft and latest CAS revision", () => {
  const base = task();
  const draft = { ...taskDraft(base), title: "我的标题" };
  const latest = task({ description: "另一端备注", category: "生活", priority: "high", revision: 2, completedAt: "2026-07-16T10:00:00Z" });
  const merged = mergeTaskDraft(base, draft, latest);
  assert.deepEqual(merged.conflicts, []);
  assert.equal(merged.draft.title, "我的标题");
  assert.equal(merged.draft.description, "另一端备注");
  assert.equal(merged.draft.category, "生活");
  assert.equal(merged.draft.priority, "high");
  const save = taskEditInput(latest, merged.draft);
  assert.equal(save.revision, 2);
  assert.equal(save.dueAt, base.dueAt, "unchanged deadline retains seconds and milliseconds");
  assert.equal(save.clearDueAt, false);
  assert.equal("completed" in save, false, "editing content must not overwrite remote completion");
  assert.equal(draft.description, base.description, "merging must not mutate the preserved draft");
});

test("overlapping edits require explicit choices; another remote write still causes a new CAS conflict", () => {
  const base = task();
  const local = { ...taskDraft(base), title: "我的标题", description: "我的备注", dueAt: null, category: "" };
  const latest = task({ title: "远端标题", description: "远端备注", dueAt: "2026-07-18T09:00:00Z", category: "生活", revision: 2 });
  const merged = mergeTaskDraft(base, local, latest);
  assert.deepEqual(merged.conflicts, ["title", "description", "dueAt", "category"]);
  assert.deepEqual(merged.draft, local, "overlapping remote changes must not replace local input silently");
  // Explicitly keep the local title/deadline/category, and choose the latest description.
  const resolved = { ...merged.draft, description: taskDraft(latest).description };
  const save = taskEditInput(latest, resolved);
  assert.equal(save.title, "我的标题");
  assert.equal(save.description, "远端备注");
  assert.equal(save.clearDueAt, true);
  assert.equal(save.category, "");
  assert.equal(save.revision, 2);
  const newer = { ...latest, title: "远端再次修改", priority: "high", revision: 3 };
  assert.notEqual(save.revision, newer.revision, "the next save cannot silently overwrite a later write");
  const again = mergeTaskDraft(latest, resolved, newer);
  assert.deepEqual(again.conflicts, ["title"]);
  assert.equal(again.draft.title, "我的标题");
  assert.equal(again.draft.priority, "high");
  assert.equal(taskEditInput(newer, again.draft).revision, 3);
});

test("409 read-merge-save flow keeps the draft, reads latest and sends revision 2 only after manual save", async (t) => {
  const base = task();
  const local = { ...taskDraft(base), title: "我的标题" };
  let server = task({ description: "另一端的新备注", revision: 2 });
  const writes = [];
  t.mock.method(globalThis, "fetch", async (path, init) => {
    if (path === "/api/auth/csrf") return Response.json({ token: "test-csrf" });
    if (init.method === "GET") return Response.json({ items: [server] });
    const input = JSON.parse(init.body);
    writes.push(input);
    if (input.revision !== server.revision) {
      return Response.json({ code: "todo_conflict", message: "待办已被修改" }, { status: 409 });
    }
    server = { ...server, ...input, revision: server.revision + 1 };
    return Response.json(server);
  });
  await assert.rejects(updateTask(taskEditInput(base, local)), (error) => error.status === 409);
  assert.equal(writes.length, 1, "409 must not automatically replay the write");
  assert.equal(local.title, "我的标题");
  const latest = await loadTaskForEdit(base.id);
  const merged = mergeTaskDraft(base, local, latest);
  assert.deepEqual(merged.conflicts, []);
  assert.equal(writes.length, 1, "reading/merging must not save implicitly");
  const saved = await updateTask(taskEditInput(latest, merged.draft));
  assert.deepEqual(writes.map(({ revision }) => revision), [1, 2]);
  assert.equal(saved.title, "我的标题");
  assert.equal(saved.description, "另一端的新备注");
  assert.equal(saved.revision, 3);
});

test("identical local and remote edits are not conflicts, including normalized timestamps", () => {
  const base = task();
  const latest = task({ title: "共同标题", dueAt: "2026-07-17T10:00:00Z", revision: 2 });
  const local = { ...taskDraft(base), title: latest.title, dueAt: "2026-07-17T10:00:00.000Z" };
  const merged = mergeTaskDraft(base, local, latest);
  assert.deepEqual(merged.conflicts, []);
  assert.deepEqual(merged.draft, local);
});

test("read latest follows unfiltered pages even when a task moved or was completed", async (t) => {
  const latest = task({ revision: 8, category: "已移动", completedAt: "2026-07-16T10:00:00Z" });
  const cursors = [];
  t.mock.method(globalThis, "fetch", async (path) => {
    const url = new URL(path, "https://workbench.test");
    assert.equal(url.pathname, "/api/modules/todo/tasks");
    assert.equal(url.searchParams.get("limit"), "100");
    for (const filter of ["view", "priority", "category"]) assert.equal(url.searchParams.has(filter), false);
    const cursor = url.searchParams.get("cursor") ?? "";
    cursors.push(cursor);
    return Response.json(cursor ? { items: [latest] } : { items: [task({ id: "unrelated" })], nextCursor: "page+2/=" });
  });
  const result = await loadTaskForEdit(latest.id);
  assert.deepEqual(cursors, ["", "page+2/="]);
  assert.deepEqual(result, latest);
});

test("deleted tasks and read failures do not discard the draft or automatically retry", async () => {
  const draft = taskDraft(task({ title: "未保存的输入" }));
  const original = structuredClone(draft);
  await assert.rejects(findTaskForEdit("deleted", async () => ({ items: [] })), /待办已不存在，草稿已保留/);
  let reads = 0;
  await assert.rejects(findTaskForEdit("task-1", async () => {
    reads++;
    throw new Error("offline");
  }), /offline/);
  assert.equal(reads, 1);
  assert.deepEqual(draft, original);
});

test("page and widget wire the tested helpers into create, conflict recovery and undo", () => {
  const page = readFileSync(new URL("../src/modules/todo/TodoPage.tsx", import.meta.url), "utf8");
  const widget = readFileSync(new URL("../src/modules/todo/TodoTodayWidget.tsx", import.meta.url), "utf8");
  const queries = readFileSync(new URL("../src/modules/todo/queries.ts", import.meta.url), "utf8");
  assert.match(widget, /onSuccess: \(task, \{ item, completed \}\) => \{\s*setDone\(\(current\) => applyWidgetCompletion\(current, item, task, completed\)\)/);
  assert.match(page, /createAttempt\.current = prepareTaskCreate\(createAttempt\.current, newTask\)/);
  assert.match(page, /function changeNewTask[\s\S]*?createAttempt\.current = null;/, "editing invalidates even an edit-and-revert action");
  assert.match(page, /if \(createAttempt\.current === input\)/, "a late create response must not clear a newer draft");
  assert.match(page, /onSave\(taskEditInput\(base, draft\)\)/);
  assert.match(page, /mergeTaskDraft\(base, draft, task\)/);
  assert.match(page, /setBase\(task\)/);
  assert.match(page, /if \(!draft\.title\.trim\(\) \|\| blocked \|\| needsLatest \|\| conflicts\.length > 0\) return;/);
  assert.match(page, /mutationFn: updateTask,\s*retry: false/);
  assert.match(page, /mutationFn: deleteTask,\s*retry: false/);
  assert.match(queries, /findTaskForEdit\(id,/);
});
