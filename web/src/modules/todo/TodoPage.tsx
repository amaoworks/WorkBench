import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { CalendarClock, Check, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { createTask, deleteTask, updateTask, useRefreshTodo, useTasks } from "./queries";
import type { Task } from "./schema";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";

export default function TodoPage() {
  const [title, setTitle] = useState("");
  const [dueAt, setDueAt] = useState("");
  const [deleting, setDeleting] = useState<Task | null>(null);
  const deleteTrigger = useRef<HTMLElement | null>(null);
  const titleInput = useRef<HTMLInputElement>(null);
  const tasks = useTasks();
  const items = tasks.data?.pages.flatMap((page) => page.items) ?? [];
  const refresh = useRefreshTodo();
  const create = useMutation({
    mutationFn: () => createTask({ title, dueAt: dueAt ? new Date(dueAt).toISOString() : null }),
    onSuccess: () => { setTitle(""); setDueAt(""); refresh(); toast.success("待办已创建"); },
    onError: (error) => toast.error(error.message)
  });
  const update = useMutation({ mutationFn: updateTask, onSuccess: refresh, onError: (error) => toast.error(error.message) });
  const remove = useMutation({ mutationFn: deleteTask, onSuccess: () => { deleteTrigger.current = null; setDeleting(null); refresh(); toast.success("待办已删除"); }, onError: (error) => toast.error(error.message) });
  function submit(event: FormEvent) { event.preventDefault(); if (title.trim()) create.mutate(); }
  return (
    <div className="page todo-page">
      <PageHeader title="待办" />
      <Card className="mb-4 p-4">
        <form onSubmit={submit} className="task-create flex gap-3">
          <input ref={titleInput} aria-label="待办标题" value={title} onChange={(event) => setTitle(event.target.value)} placeholder="添加一项待办…" className="ui-input min-w-0 flex-1" />
          <label className="task-date relative">
            <span className="sr-only">截止时间</span>
            <CalendarClock className="pointer-events-none absolute left-3 top-2.5 text-[var(--muted)]" size={17} />
            <input type="datetime-local" value={dueAt} onChange={(event) => setDueAt(event.target.value)} className="ui-input" />
          </label>
          <Button disabled={create.isPending || !title.trim()}><Plus size={17} />添加</Button>
        </form>
      </Card>
      <Card>
        <CardHeader title="任务清单" description={tasks.data ? `已加载 ${items.length} 项 · ${items.filter((task) => !task.completedAt).length} 项未完成` : "正在同步"} />
        {tasks.isLoading && <div className="space-y-3 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>}
        {tasks.isError && <p className="p-5 text-danger">{tasks.error.message}</p>}
        {tasks.data && items.length === 0 && <EmptyState title="清单还是空的" description="写下一个清晰、可以立即行动的任务。" />}
        <div className="divide-y divide-[var(--border)]">
          {items.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              onToggle={() => update.mutate({ id: task.id, completed: !task.completedAt })}
              onDelete={() => { deleteTrigger.current = document.activeElement as HTMLElement; setDeleting(task); }}
            />
          ))}
        </div>
        {tasks.hasNextPage && <div className="p-4"><Button variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => tasks.fetchNextPage()}>{tasks.isFetchingNextPage ? "加载中…" : "加载更多"}</Button></div>}
      </Card>
      <ConfirmDialog open={deleting !== null} title="删除待办" description={`确定删除“${deleting?.title ?? ""}”吗？`} busy={remove.isPending}
        onCancel={() => setDeleting(null)} onConfirm={() => { if (deleting && !remove.isPending) remove.mutate(deleting.id); }}
        onClosed={() => { const target = deleteTrigger.current; if (target?.isConnected) target.focus(); else titleInput.current?.focus(); }} />
    </div>
  );
}

function TaskRow({ task, onToggle, onDelete }: { task: Task; onToggle: () => void; onDelete: () => void }) {
  const overdue = task.dueAt && !task.completedAt && new Date(task.dueAt) < new Date();
  return (
    <div className="task-row group flex items-center gap-3 px-5 py-4">
      <button onClick={onToggle} role="checkbox" aria-checked={!!task.completedAt} aria-label={task.completedAt ? "标记为未完成" : "标记为完成"} className="task-check focus-ring">{task.completedAt && <Check size={14} />}</button>
      <div className="min-w-0 flex-1">
        <p className={cn("truncate font-medium", task.completedAt && "text-[var(--muted)] line-through")}>{task.title}</p>
        {task.description && <p className="mt-1 whitespace-pre-line break-words text-xs text-[var(--muted)]">{task.description}</p>}
        {task.dueAt && <p className={cn("mt-1 text-xs text-[var(--muted)]", overdue && "text-warning")}>{overdue ? "已逾期 · " : ""}{new Date(task.dueAt).toLocaleString()}</p>}
      </div>
      <Button variant="ghost" size="icon" className="task-delete opacity-0 group-hover:opacity-100 focus:opacity-100" onClick={onDelete} aria-label="删除待办"><Trash2 size={16} /></Button>
    </div>
  );
}
