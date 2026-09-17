import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { savedSchema, type Widget } from "../../shared/schema";
import { summarySchema, tasksResponseSchema, wallosSettingsSchema, wallosSyncSchema, taskSchema, type WallosSettingsInput } from "./schema";

export function useTasks() {
  return useInfiniteQuery({
    queryKey: ["todo", "tasks"],
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

export function useTodoSummary(widget: Widget) {
  return useQuery({ queryKey: ["widget", widget.id], queryFn: async () => summarySchema.parse(await api(widget.dataRoute)) });
}

export function useWallosSettings(enabled: boolean) {
  return useQuery({ queryKey: ["todo", "wallos"], enabled, queryFn: async () => wallosSettingsSchema.parse(await api("/api/modules/todo/wallos")) });
}

export function saveWallosSettings(input: WallosSettingsInput) {
  return apiValidated("/api/modules/todo/wallos", savedSchema, { method: "PUT", body: JSON.stringify(input) });
}

export async function syncWallos() {
  return wallosSyncSchema.parse(await api("/api/modules/todo/wallos/sync", { method: "POST", body: "{}" }));
}

export function useRefreshTodo() {
  const client = useQueryClient();
  return () => {
    for (const queryKey of [["todo"], ["dashboard"], ["widget"]]) void client.invalidateQueries({ queryKey });
  };
}
