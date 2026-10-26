import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { savedSchema, type Widget } from "../../shared/schema";
import { moduleQueryKey } from "../../shared/queryKeys";
import { widgetSeed } from "../../shared/dashboard";
import { todoTodaySchema, tasksResponseSchema, wallosSettingsSchema, wallosSyncSchema, taskSchema, type WallosSettingsInput } from "./schema";

const todoKey = (...parts: readonly unknown[]) => moduleQueryKey("todo", ...parts);

export function useTasks() {
  return useInfiniteQuery({
    queryKey: todoKey("tasks"),
    initialPageParam: "",
    queryFn: async ({ pageParam }) => tasksResponseSchema.parse(await api(`/api/modules/todo/tasks?limit=50${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`)),
    getNextPageParam: (page) => page.nextCursor
  });
}

export function createTask(input: { title: string; dueAt: string | null }) {
  return apiValidated("/api/modules/todo/tasks", taskSchema, { method: "POST", body: JSON.stringify(input) });
}

export function updateTask({ id, completed }: { id: string; completed: boolean }) {
  return apiValidated(`/api/modules/todo/tasks/${id}`, taskSchema, { method: "PATCH", body: JSON.stringify({ completed }) });
}

export function deleteTask(id: string) {
  return api(`/api/modules/todo/tasks/${id}`, { method: "DELETE" });
}

export const browserTimeZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

/** Overview card: due-today and overdue tasks plus upcoming renewals, bucketed in the browser's time zone. */
export function useTodoToday(widget: Widget) {
  const tz = browserTimeZone();
  const seed = widgetSeed(widget, todoTodaySchema);
  return useQuery({
    queryKey: todoKey("widget", widget.id, tz), ...(seed.initialData?.timeZone === tz ? seed : {}),
    queryFn: () => apiValidated(`${widget.dataRoute}?tz=${encodeURIComponent(tz)}`, todoTodaySchema), refetchInterval: 120_000
  });
}

export function useWallosSettings(enabled: boolean) {
  return useQuery({ queryKey: todoKey("wallos"), enabled, queryFn: async () => wallosSettingsSchema.parse(await api("/api/modules/todo/wallos")) });
}

export function saveWallosSettings(input: WallosSettingsInput) {
  return apiValidated("/api/modules/todo/wallos", savedSchema, { method: "PUT", body: JSON.stringify(input) });
}

export async function syncWallos() {
  return wallosSyncSchema.parse(await api("/api/modules/todo/wallos/sync", { method: "POST", body: "{}" }));
}

export function useRefreshTodo() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: todoKey() });
}
