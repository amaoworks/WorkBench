import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Check, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { createTask, deleteTask, updateTask, useRefreshTodo, useTasks } from "./queries";
import type { Task } from "./schema";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { TodoDeadline } from "./TodoDeadline";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { formatDateTime } from "../../shared/format";

export default function TodoPage() {
  const [title, setTitle] = useState("");
  const [dueAt, setDueAt] = useState("");
  const titleInput = useRef<HTMLInputElement>(null);
  const tasks = useTasks();
  const items = tasks.data?.pages.flatMap((page) => page.items) ?? [];
  const refresh = useRefreshTodo();
  const create = useMutation({
    mutationFn: () => createTask({ title, dueAt: dueAt ? new Date(dueAt).toISOString() : null }),
    onSuccess: () => {
      setTitle("");
      setDueAt("");
      refresh();
      toast.success("待办已创建");
    },
    onError: (error) => toast.error(error.message),
  });
  const update = useMutation({ mutationFn: updateTask, onSuccess: refresh, onError: (error) => toast.error(error.message) });
  const remove = useMutation({
    mutationFn: deleteTask,
    onSuccess: async () => {
      await refresh();
      toast.success("待办已删除");
    },
    onError: (error) => toast.error(error.message),
  });
  const deletion = useConfirmDelete<Task>({
    title: "删除待办",
    description: (task) => `确定删除“${task.title}”吗？`,
    onConfirm: (task) => remove.mutateAsync(task.id),
    fallbackFocus: () => titleInput.current,
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    if (title.trim()) create.mutate();
  }
  return (
    <div className="page todo-page">
      <PageHeader title="待办" />
      <Card className="mb-4 p-4">
        <form onSubmit={submit} className="task-create flex gap-3">
          <input
            ref={titleInput}
            aria-label="待办标题"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="添加一项待办…"
            className="ui-input min-w-0 flex-1"
          />
          <TodoDeadline value={dueAt} onChange={setDueAt} />
          <Button disabled={create.isPending || !title.trim()}>
            <Plus size={17} />
            添加
          </Button>
        </form>
      </Card>
      <Card>
        <CardHeader
          title="任务清单"
          description={tasks.data ? taskCount(items.length, items.filter((task) => !task.completedAt).length) : undefined}
        />
        {tasks.isLoading && (
          <div className="space-y-3 p-4">
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
          </div>
        )}
        {tasks.isError && (
          <Notice tone="danger" title="待办加载失败" className="m-4">
            {tasks.error.message}
          </Notice>
        )}
        {tasks.data && items.length === 0 && <EmptyState title="清单还是空的" description="写下一个清晰、可以立即行动的任务。" />}
        <div className="divide-y divide-[var(--border)]">
          {items.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              onToggle={() => update.mutate({ id: task.id, completed: !task.completedAt })}
              onDelete={(element) => deletion.request(task, element)}
            />
          ))}
        </div>
        {tasks.hasNextPage && (
          <div className="p-4">
            <Button variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => tasks.fetchNextPage()}>
              {tasks.isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </Card>
      <ConfirmDialog {...deletion.dialogProps} />
    </div>
  );
}

function TaskRow({ task, onToggle, onDelete }: { task: Task; onToggle: () => void; onDelete: (element: HTMLElement) => void }) {
  const overdue = task.dueAt && !task.completedAt && new Date(task.dueAt) < new Date();
  return (
    <div className="task-row group flex items-center gap-3 px-5 py-4">
      <button
        onClick={onToggle}
        role="checkbox"
        aria-checked={!!task.completedAt}
        aria-label={task.completedAt ? "标记为未完成" : "标记为完成"}
        className="task-check focus-ring"
      >
        {task.completedAt && <Check size={14} />}
      </button>
      <div className="min-w-0 flex-1">
        <p className={cn("truncate font-medium", task.completedAt && "text-[var(--muted)] line-through")}>{task.title}</p>
        {task.description && <p className="mt-1 whitespace-pre-line break-words text-xs text-[var(--muted)]">{task.description}</p>}
        {task.dueAt && (
          <p className={cn("mt-1 text-xs text-[var(--muted)]", overdue && "text-warning")}>
            {overdue ? "已逾期 · " : ""}
            {formatDateTime(task.dueAt, { seconds: false })}
          </p>
        )}
      </div>
      <Button
        variant="ghost"
        size="icon"
        className="task-delete text-[var(--muted)] hover:text-[var(--danger)] focus-visible:text-[var(--danger)]"
        onClick={(event) => onDelete(event.currentTarget)}
        aria-label={`删除 ${task.title}`}
      >
        <Trash2 size={16} />
      </Button>
    </div>
  );
}

function taskCount(total: number, open: number) {
  if (total === 0) return undefined;
  if (open === total) return `${total} 项未完成`;
  if (open === 0) return `${total} 项 · 全部完成`;
  return `${total} 项 · ${open} 项未完成`;
}
