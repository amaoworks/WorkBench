import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Check, Flag, Plus, Repeat, Trash2 } from "lucide-react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { createTask, deleteTask, updateTask, useRefreshTodo, useTaskCounts, useTasks } from "./queries";
import type { Task, TaskPriority } from "./schema";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Badge } from "../../components/ui/Badge";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { TodoDeadline } from "./TodoDeadline";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { formatDateTime } from "../../shared/format";
import { Select } from "../../components/ui/Select";

const WALLOS_PREFIX = "Wallos续费提醒 - ";
const PRIORITIES: readonly { value: TaskPriority; label: string }[] = [
  { value: "none", label: "无优先级" }, { value: "low", label: "低优先级" },
  { value: "medium", label: "中优先级" }, { value: "high", label: "高优先级" },
];
const PRIORITY_FILTERS = [{ value: "", label: "全部优先级" }, ...PRIORITIES] as const;

type GroupId = "overdue" | "today" | "week" | "later" | "none" | "done";
const GROUPS: { id: GroupId; title: string }[] = [
  { id: "overdue", title: "已逾期" },
  { id: "today", title: "今天" },
  { id: "week", title: "本周" },
  { id: "later", title: "以后" },
  { id: "none", title: "无截止时间" },
  { id: "done", title: "已完成" },
];

const VIEWS = [
  { id: "all", label: "全部", groups: ["overdue", "today", "week", "later", "none", "done"] },
  { id: "today", label: "今天", groups: ["overdue", "today"] },
  { id: "week", label: "本周", groups: ["overdue", "today", "week"] },
  { id: "later", label: "以后", groups: ["later", "none"] },
  { id: "done", label: "已完成", groups: ["done"] },
] as const satisfies readonly { id: string; label: string; groups: readonly GroupId[] }[];
type ViewId = (typeof VIEWS)[number]["id"];

function groupOf(task: Task, now: Date): GroupId {
  if (task.completedAt) return "done";
  if (!task.dueAt) return "none";
  const due = new Date(task.dueAt).getTime();
  if (due < now.getTime()) return "overdue";
  const endToday = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
  if (due < endToday) return "today";
  const daysUntilMonday = (8 - now.getDay()) % 7 || 7;
  const endWeek = new Date(now.getFullYear(), now.getMonth(), now.getDate() + daysUntilMonday).getTime();
  if (due < endWeek) return "week";
  return "later";
}

function useCurrentTime() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  return now;
}

export default function TodoPage() {
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const viewParam = params.get("view");
  const view = VIEWS.find((item) => item.id === viewParam) ?? VIEWS[0];
  const [title, setTitle] = useState("");
  const [dueAt, setDueAt] = useState("");
  const [priority, setPriority] = useState<TaskPriority>("none");
  const [category, setCategory] = useState("");
  const priorityFilter = (PRIORITIES.some((item) => item.value === params.get("priority")) ? params.get("priority") : "") as "" | TaskPriority;
  const categoryFilter = params.get("category")?.trim() ?? "";
  const [categoryDraft, setCategoryDraft] = useState(categoryFilter);
  const titleInput = useRef<HTMLInputElement>(null);
  const filters = { view: view.id, priority: priorityFilter, category: categoryFilter };
  const tasks = useTasks(filters);
  const taskCounts = useTaskCounts({ priority: priorityFilter, category: categoryFilter });
  const items = tasks.data?.pages.flatMap((page) => page.items) ?? [];
  const refresh = useRefreshTodo();
  const wantsNew = params.get("new") === "1";
  // The 新建待办 menu entry links to ?new=1; focus the input every time it is followed.
  useEffect(() => {
    if (wantsNew) titleInput.current?.focus();
  }, [wantsNew, location.key]);
  const create = useMutation({
    mutationFn: () => createTask({ title, dueAt: dueAt ? new Date(dueAt).toISOString() : null, priority, category }),
    onSuccess: () => {
      setTitle("");
      setDueAt("");
      setPriority("none");
      setCategory("");
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
  const now = useCurrentTime();
  const grouped = new Map<GroupId, Task[]>();
  for (const task of items) {
    const group = groupOf(task, now);
    grouped.set(group, [...(grouped.get(group) ?? []), task]);
  }
  const groups = taskCounts.data;
  const counts: Record<ViewId, number> = groups ? {
    all: groups.overdue + groups.today + groups.week + groups.later + groups.none + groups.done,
    today: groups.overdue + groups.today,
    week: groups.overdue + groups.today + groups.week,
    later: groups.later + groups.none,
    done: groups.done,
  } : { all: 0, today: 0, week: 0, later: 0, done: 0 };
  const visible = GROUPS.filter((group) => (view.groups as readonly GroupId[]).includes(group.id) && grouped.get(group.id)?.length);
  const open = groups ? groups.overdue + groups.today + groups.week + groups.later + groups.none : 0;
  const viewLink = (nextView: ViewId) => {
    const next = new URLSearchParams();
    if (nextView !== "all") next.set("view", nextView);
    if (priorityFilter) next.set("priority", priorityFilter);
    if (categoryFilter) next.set("category", categoryFilter);
    const query = next.toString();
    return query ? `/todo?${query}` : "/todo";
  };
  const applyFilters = (nextPriority: "" | TaskPriority, nextCategory: string) => {
    const next = new URLSearchParams(params);
    next.delete("new");
    if (nextPriority) next.set("priority", nextPriority); else next.delete("priority");
    if (nextCategory.trim()) next.set("category", nextCategory.trim()); else next.delete("category");
    setParams(next);
  };
  return (
    <div className="page todo-page">
      <PageHeader title="待办" description={groups ? taskCount(counts.all, open) : undefined} />
      <Card className="mb-5 p-4">
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
          <Select aria-label="优先级" value={priority} onValueChange={(value) => setPriority(value as TaskPriority)} options={PRIORITIES} />
          <input
            aria-label="分类"
            value={category}
            onChange={(event) => setCategory(event.target.value)}
            maxLength={40}
            placeholder="分类"
            className="ui-input min-w-0 flex-1"
          />
          <Button disabled={create.isPending || !title.trim()}>
            <Plus size={17} />
            添加
          </Button>
        </form>
      </Card>
      <Card className="mb-5 p-4">
        <form
          className="flex flex-wrap gap-3"
          onSubmit={(event) => { event.preventDefault(); applyFilters(priorityFilter, categoryDraft); }}
        >
          <Select
            aria-label="按优先级筛选"
            value={priorityFilter}
            onValueChange={(value) => applyFilters(value as "" | TaskPriority, categoryDraft)}
            options={PRIORITY_FILTERS}
          />
          <input
            aria-label="按分类筛选"
            value={categoryDraft}
            onChange={(event) => setCategoryDraft(event.target.value)}
            maxLength={40}
            placeholder="筛选分类"
            className="ui-input min-w-0 flex-1"
          />
          <Button variant="secondary">筛选</Button>
          {(priorityFilter || categoryFilter) && (
            <Button type="button" variant="ghost" onClick={() => { setCategoryDraft(""); applyFilters("", ""); }}>清除</Button>
          )}
        </form>
      </Card>
      <nav className="pill-tabs mb-5" aria-label="待办视图">
        {VIEWS.map((item) => (
          <Link
            key={item.id}
            to={viewLink(item.id)}
            className={cn("pill-tab focus-ring", item.id === view.id && "is-active")}
            aria-current={item.id === view.id ? "page" : undefined}
          >
            {item.label}
            {taskCounts.data && counts[item.id] > 0 && <span className="pill-tab-count">{counts[item.id]}</span>}
          </Link>
        ))}
      </nav>
      {tasks.isLoading && (
        <Card className="space-y-3 p-4">
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </Card>
      )}
      {tasks.isError && (
        <Notice tone="danger" title="待办加载失败">
          {tasks.error.message}
        </Notice>
      )}
      {tasks.data && items.length === 0 && (
        <Card>
          <EmptyState
            title={view.id === "all" && !priorityFilter && !categoryFilter ? "清单还是空的" : `「${view.label}」里没有待办`}
            description="切换视图或筛选条件，或在上方添加一项。"
          />
        </Card>
      )}
      <div className="todo-groups">
        {visible.map((group) => (
          <section key={group.id} className="todo-group" aria-labelledby={`todo-group-${group.id}`} data-group={group.id}>
            <h2 id={`todo-group-${group.id}`} className="todo-group-title">
              {group.title}
              <span>{grouped.get(group.id)?.length}</span>
            </h2>
            <Card className="divide-y divide-[var(--border)]">
              {grouped.get(group.id)?.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  overdue={group.id === "overdue"}
                  onToggle={() => update.mutate({ id: task.id, completed: !task.completedAt })}
                  onDelete={(element) => deletion.request(task, element)}
                />
              ))}
            </Card>
          </section>
        ))}
      </div>
      {tasks.hasNextPage && (
        <div className="mt-4">
          <Button variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => tasks.fetchNextPage()}>
            {tasks.isFetchingNextPage ? "加载中…" : "加载更多"}
          </Button>
        </div>
      )}
      <ConfirmDialog {...deletion.dialogProps} />
    </div>
  );
}

function TaskRow({
  task,
  overdue,
  onToggle,
  onDelete,
}: {
  task: Task;
  overdue: boolean;
  onToggle: () => void;
  onDelete: (element: HTMLElement) => void;
}) {
  const wallos = task.category === "renewal";
  const title = wallos && task.title.startsWith(WALLOS_PREFIX) ? task.title.slice(WALLOS_PREFIX.length) : task.title;
  return (
    <div className="task-row group flex items-center gap-3 px-5 py-4">
      <button
        onClick={onToggle}
        role="checkbox"
        aria-checked={!!task.completedAt}
        aria-label={`${task.completedAt ? "标记为未完成" : "标记为完成"}：${task.title}`}
        className="task-check focus-ring"
      >
        {task.completedAt && <Check size={14} />}
      </button>
      <div className="min-w-0 flex-1">
        <p className={cn("flex min-w-0 items-center gap-2 font-medium", task.completedAt && "text-[var(--muted)] line-through")}>
          <span className="truncate">{title}</span>
          {wallos && (
            <Badge>
              <Repeat size={11} />
              续费
            </Badge>
          )}
          {!wallos && task.category && <Badge>{task.category}</Badge>}
          {task.priority !== "none" && (
            <Badge tone={task.priority === "high" ? "danger" : task.priority === "medium" ? "warning" : "neutral"}>
              <Flag size={11} />
              {PRIORITIES.find((item) => item.value === task.priority)?.label}
            </Badge>
          )}
        </p>
        {task.description && <p className="mt-1 whitespace-pre-line break-words text-xs text-[var(--muted)]">{task.description}</p>}
        {(task.dueAt || task.completedAt) && (
          <p className={cn("mt-1 text-xs text-[var(--muted)]", overdue && "text-warning")}>
            {task.completedAt
              ? `完成于 ${formatDateTime(task.completedAt, { seconds: false })}`
              : task.dueAt && `${overdue ? "已逾期 · " : ""}${formatDateTime(task.dueAt, { seconds: false })}`}
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
