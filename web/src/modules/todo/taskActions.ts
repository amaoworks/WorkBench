import type { Task, TaskPriority, TodoToday } from "./schema";
import type { TaskCreateInput, TaskUpdateInput } from "./queries";

export type TaskDraft = {
  title: string;
  description: string;
  dueAt: string | null;
  priority: TaskPriority;
  category: string;
};
export type TaskDraftField = keyof TaskDraft;
export const TASK_DRAFT_FIELDS: readonly TaskDraftField[] = ["title", "description", "dueAt", "priority", "category"];

export const emptyTaskDraft = (): TaskDraft => ({ title: "", description: "", dueAt: null, priority: "none", category: "" });

export function taskDraft(task: Task): TaskDraft {
  return {
    title: task.title, description: task.description, dueAt: task.dueAt ? new Date(task.dueAt).toISOString() : null,
    priority: task.priority, category: task.category ?? "",
  };
}

/** Retain the decoded request, not just its key, when a create response is lost. */
export function prepareTaskCreate(
  previous: TaskCreateInput | null, draft: TaskDraft, newKey: () => string = () => crypto.randomUUID(),
): TaskCreateInput {
  if (previous && TASK_DRAFT_FIELDS.every((field) => previous[field] === draft[field])) return previous;
  return { ...draft, idempotencyKey: newKey() };
}

export function taskEditInput(task: Task, draft: TaskDraft): TaskUpdateInput {
  return { id: task.id, ...draft, clearDueAt: draft.dueAt === null, revision: task.revision };
}

/** Only unchanged local fields adopt remote changes; overlapping edits require an explicit choice. */
export function mergeTaskDraft(base: Task, draft: TaskDraft, latest: Task) {
  const original = taskDraft(base);
  const remote = taskDraft(latest);
  const merged = { ...draft };
  const conflicts: TaskDraftField[] = [];
  for (const field of TASK_DRAFT_FIELDS) {
    if (draft[field] === original[field]) {
      // Object.assign preserves the field/value relationship across the union of draft keys.
      Object.assign(merged, { [field]: remote[field] });
    } else if (remote[field] !== original[field] && draft[field] !== remote[field]) {
      conflicts.push(field);
    }
  }
  return { draft: merged, conflicts };
}

type TodayItem = TodoToday["items"][number];
export function applyWidgetCompletion(current: TodayItem[], item: TodayItem, task: Task, completed: boolean): TodayItem[] {
  const remaining = current.filter((entry) => entry.id !== item.id);
  return completed ? [...remaining, { ...item, ...task }] : remaining;
}

/** There is no single-task GET endpoint; read unfiltered pages so moved/completed tasks remain discoverable. */
export async function findTaskForEdit(
  id: string, readPage: (cursor: string) => Promise<{ items: Task[]; nextCursor?: string }>,
): Promise<Task> {
  let cursor = "";
  do {
    const page = await readPage(cursor);
    const task = page.items.find((item) => item.id === id);
    if (task) return task;
    cursor = page.nextCursor ?? "";
  } while (cursor);
  throw new Error("待办已不存在，草稿已保留");
}
