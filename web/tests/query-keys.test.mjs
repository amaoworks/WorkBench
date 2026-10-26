import assert from "node:assert/strict";
import { test } from "node:test";
import { QueryClient } from "@tanstack/react-query";
import { clearModuleQueries, moduleQueryKey } from "../src/shared/queryKeys.ts";

function pendingQuery(client, queryKey) {
  let finish;
  let started;
  let aborted = false;
  const ready = new Promise((resolve) => {
    started = resolve;
  });
  const result = client.fetchQuery({
    queryKey,
    queryFn: ({ signal }) =>
      new Promise((resolve, reject) => {
        finish = resolve;
        signal.addEventListener(
          "abort",
          () => {
            aborted = true;
            reject(new Error("query aborted"));
          },
          { once: true },
        );
        started();
      }),
  });
  const settled = result.then(
    (value) => ({ value }),
    (error) => ({ error }),
  );
  return {
    ready,
    settled,
    finish(value) {
      finish(value);
    },
    get aborted() {
      return aborted;
    },
  };
}

test("clearing one module cancels and removes only that module prefix", async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const todoTasksKey = moduleQueryKey("todo", "tasks");
  const todoWidgetKey = moduleQueryKey("todo", "widget", "todo.today");
  const notificationsKey = ["notifications", "list"];
  const newsWidgetKey = moduleQueryKey("news", "widget", "news.brief");
  const todo = pendingQuery(client, todoTasksKey);
  const notifications = pendingQuery(client, notificationsKey);
  const newsWidget = pendingQuery(client, newsWidgetKey);
  client.setQueryData(todoWidgetKey, { open: 2 });
  await Promise.all([todo.ready, notifications.ready, newsWidget.ready]);

  await clearModuleQueries(client, "todo");

  assert.equal(todo.aborted, true);
  assert.ok((await todo.settled).error);
  assert.equal(client.getQueryState(todoTasksKey), undefined);
  assert.equal(client.getQueryState(todoWidgetKey), undefined);
  assert.equal(notifications.aborted, false);
  assert.notEqual(client.getQueryState(notificationsKey), undefined);
  assert.equal(newsWidget.aborted, false);
  assert.notEqual(client.getQueryState(newsWidgetKey), undefined);

  notifications.finish({ items: [] });
  newsWidget.finish({ articles: 3 });
  assert.deepEqual((await notifications.settled).value, { items: [] });
  assert.deepEqual((await newsWidget.settled).value, { articles: 3 });
  client.clear();
});
