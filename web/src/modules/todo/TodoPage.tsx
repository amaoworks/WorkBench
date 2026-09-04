import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Check, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { tasksResponseSchema, type Task } from "../../shared/schema";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";

export default function TodoPage() {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState("");
  const [dueAt, setDueAt] = useState("");
  const tasks = useQuery({ queryKey: ["todo", "tasks"], queryFn: async () => tasksResponseSchema.parse(await api<unknown>("/api/modules/todo/tasks")) });
  const refresh = () => { void queryClient.invalidateQueries({ queryKey: ["todo"] }); void queryClient.invalidateQueries({ queryKey: ["dashboard"] }); void queryClient.invalidateQueries({ queryKey: ["widget"] }); };
  const create = useMutation({
    mutationFn: () => api<Task>("/api/modules/todo/tasks", { method: "POST", body: JSON.stringify({ title, dueAt: dueAt ? new Date(dueAt).toISOString() : null }) }),
    onSuccess: () => { setTitle(""); setDueAt(""); refresh(); toast.success("待办已创建"); },
    onError: (error) => toast.error(error.message)
  });
  const update = useMutation({ mutationFn: ({ id, completed }: { id: string; completed: boolean }) => api<Task>(`/api/modules/todo/tasks/${id}`, { method: "PATCH", body: JSON.stringify({ completed }) }), onSuccess: refresh, onError: (error) => toast.error(error.message) });
  const remove = useMutation({ mutationFn: (id: string) => api<void>(`/api/modules/todo/tasks/${id}`, { method: "DELETE" }), onSuccess: () => { refresh(); toast.success("待办已删除"); }, onError: (error) => toast.error(error.message) });
  function submit(event: FormEvent) { event.preventDefault(); if (title.trim()) create.mutate(); }
  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight">待办</h1>
        <p className="mt-1 text-[var(--muted)]">轻量管理下一步行动，到期提醒由统一调度器处理。</p>
      </div>
      <Card className="mb-4 p-4">
        <form onSubmit={submit} className="flex flex-col gap-3 md:flex-row">
          <input aria-label="待办标题" value={title} onChange={(event) => setTitle(event.target.value)} placeholder="添加一项待办…" className="focus-ring min-w-0 flex-1 rounded-lg border border-[var(--border)] bg-transparent px-3 py-2.5" />
          <label className="relative">
            <span className="sr-only">截止时间</span>
            <CalendarClock className="pointer-events-none absolute left-3 top-2.5 text-[var(--muted)]" size={17} />
            <input type="datetime-local" value={dueAt} onChange={(event) => setDueAt(event.target.value)} className="focus-ring rounded-lg border border-[var(--border)] bg-transparent py-2.5 pl-9 pr-3" />
          </label>
          <Button disabled={create.isPending || !title.trim()}><Plus size={17} />添加</Button>
        </form>
      </Card>
      <Card>
        <CardHeader title="任务清单" description={tasks.data ? `${tasks.data.items.filter((task) => !task.completedAt).length} 项未完成` : "正在同步"} />
        {tasks.isLoading && <div className="space-y-3 p-4"><Skeleton className="h-14" /><Skeleton className="h-14" /><Skeleton className="h-14" /></div>}
        {tasks.isError && <p className="p-5 text-red-500">{tasks.error.message}</p>}
        {tasks.data?.items.length === 0 && <EmptyState title="清单还是空的" description="写下一个清晰、可以立即行动的任务。" />}
        <div className="divide-y divide-[var(--border)]">
          {tasks.data?.items.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              onToggle={() => update.mutate({ id: task.id, completed: !task.completedAt })}
              onDelete={() => { if (window.confirm(`确定删除“${task.title}”吗？`)) remove.mutate(task.id); }}
            />
          ))}
        </div>
      </Card>
    </div>
  );
}

function TaskRow({ task, onToggle, onDelete }: { task: Task; onToggle: () => void; onDelete: () => void }) {
  const overdue = task.dueAt && !task.completedAt && new Date(task.dueAt) < new Date();
  return (
    <div className="group flex items-center gap-3 px-4 py-3.5">
      <button onClick={onToggle} aria-label={task.completedAt ? "标记为未完成" : "标记为完成"} className={cn("focus-ring grid size-6 shrink-0 place-items-center rounded-full border transition", task.completedAt ? "border-emerald-500 bg-emerald-500 text-white" : "border-slate-300 hover:border-indigo-500 dark:border-slate-600")}>{task.completedAt && <Check size={14} />}</button>
      <div className="min-w-0 flex-1">
        <p className={cn("truncate font-medium", task.completedAt && "text-[var(--muted)] line-through")}>{task.title}</p>
        {task.dueAt && <p className={cn("mt-0.5 text-xs text-[var(--muted)]", overdue && "text-amber-600")}>{overdue ? "已逾期 · " : ""}{new Date(task.dueAt).toLocaleString()}</p>}
      </div>
      <Button variant="ghost" size="icon" className="opacity-0 group-hover:opacity-100 focus:opacity-100" onClick={onDelete} aria-label="删除待办"><Trash2 size={16} /></Button>
    </div>
  );
}
