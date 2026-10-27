import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { savedSchema, type Widget } from "../../shared/schema";
import { moduleQueryKey } from "../../shared/queryKeys";
import { widgetSeed } from "../../shared/dashboard";
import {
  todoTodaySchema, taskCountsSchema, tasksResponseSchema, wallosSettingsSchema, wallosSyncSchema, taskSchema,
  type TaskPriority, type WallosSettingsInput
} from "./schema";

const todoKey = (...parts: readonly unknown[]) => moduleQueryKey("todo", ...parts);

type TaskFilters = { view: string; priority: "" | TaskPriority; category: string };

function taskSearch(filters: TaskFilters) {
  const params = new URLSearchParams({ limit: "50", tz: browserTimeZone() });
  if (filters.view !== "all") params.set("view", filters.view);
  if (filters.priority) params.set("priority", filters.priority);
  if (filters.category.trim()) params.set("category", filters.category.trim());
  return params;
}

export function useTasks(filters: TaskFilters) {
  return useInfiniteQuery({
    queryKey: todoKey("tasks", filters),
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      const params = taskSearch(filters);
      if (pageParam) params.set("cursor", pageParam);
      return tasksResponseSchema.parse(await api(`/api/modules/todo/tasks?${params}`));
    },
    getNextPageParam: (page) => page.nextCursor,
    refetchInterval: 30_000
  });
}

export function useTaskCounts(filters: Omit<TaskFilters, "view">) {
  const params = taskSearch({ ...filters, view: "all" });
  params.delete("limit");
  return useQuery({
    queryKey: todoKey("counts", filters, browserTimeZone()),
    queryFn: () => apiValidated(`/api/modules/todo/tasks/counts?${params}`, taskCountsSchema),
    refetchInterval: 30_000
  });
}

export function createTask(input: { title: string; dueAt: string | null; priority: TaskPriority; category: string }) {
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

/** Today section of the todo page: the same agenda as the overview card (open tasks due today or overdue, completedToday). */
export function useTodoAgenda() {
  const tz = browserTimeZone();
  return useQuery({
    queryKey: todoKey("agenda", tz),
    queryFn: () => apiValidated(`/api/modules/todo/widget/today?tz=${encodeURIComponent(tz)}`, todoTodaySchema),
    refetchInterval: 30_000
  });
}
