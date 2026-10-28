import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { createTask, deleteTask, updateTask, useRefreshTodo, useTaskCounts, useTasks, useTodoAgenda } from "./queries";
import type { Task, TaskPriority } from "./schema";
import { Button } from "../../components/ui/Button";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Badge } from "../../components/ui/Badge";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { TodoDeadline } from "./TodoDeadline";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Select } from "../../components/ui/Select";

const WALLOS_PREFIX = "Wallos续费提醒 - ";
const PRIORITIES: readonly { value: TaskPriority; label: string }[] = [
  { value: "none", label: "无优先级" }, { value: "low", label: "低优先级" },
  { value: "medium", label: "中优先级" }, { value: "high", label: "高优先级" },
];
const PRIORITY_FILTERS = [{ value: "", label: "全部优先级" }, ...PRIORITIES] as const;
// Priority is a neutral, single-hue scale (solid / tint / grey) so todo never borrows the market's red/green.
const PRIORITY_BADGE: Record<Exclude<TaskPriority, "none">, { label: string; tone: "solid" | "info" | "neutral" }> = {
  high: { label: "高", tone: "solid" }, medium: { label: "中", tone: "info" }, low: { label: "低", tone: "neutral" },
};

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

const startOfDay = (date: Date, offset = 0) => new Date(date.getFullYear(), date.getMonth(), date.getDate() + offset).getTime();

function groupOf(task: Task, now: Date): GroupId {
  if (task.completedAt) return "done";
  if (!task.dueAt) return "none";
  const due = new Date(task.dueAt).getTime();
  if (due < now.getTime()) return "overdue";
  if (due < startOfDay(now, 1)) return "today";
  const daysUntilMonday = (8 - now.getDay()) % 7 || 7;
  if (due < startOfDay(now, daysUntilMonday)) return "week";
  return "later";
}

const pad = (value: number) => String(value).padStart(2, "0");
const clock = (date: Date) => `${pad(date.getHours())}:${pad(date.getMinutes())}`;
const shortDate = (date: Date) => `${date.getMonth() + 1}/${pad(date.getDate())}`;

/** Compact, relative date like the prototype's right column: 今天 16:30 · 明天 09:00 · 10/29. */
function dayLabel(value: string, now: Date) {
  const date = new Date(value);
  const time = date.getTime();
  if (time >= startOfDay(now) && time < startOfDay(now, 1)) return `今天 ${clock(date)}`;
  if (time >= startOfDay(now, 1) && time < startOfDay(now, 2)) return `明天 ${clock(date)}`;
  if (time >= startOfDay(now, -1) && time < startOfDay(now)) return `昨天 ${clock(date)}`;
  return date.getFullYear() === now.getFullYear() ? shortDate(date) : `${date.getFullYear()}/${shortDate(date)}`;
}

function useCurrentTime() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);
  return now;
}

const displayTitle = (task: Task) =>
  task.category === "renewal" && task.title.startsWith(WALLOS_PREFIX) ? task.title.slice(WALLOS_PREFIX.length) : task.title;

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
  const tasks = useTasks({ view: view.id, priority: priorityFilter, category: categoryFilter });
  const taskCounts = useTaskCounts({ priority: priorityFilter, category: categoryFilter });
  const agenda = useTodoAgenda();
  const items = tasks.data?.pages.flatMap((page) => page.items) ?? [];
  const refresh = useRefreshTodo();
  const wantsNew = params.get("new") === "1";
  // `?new=1` (e.g. from the AI panel or a bookmark) focuses the composer every time it is followed.
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
  const toggle = (task: Task) => update.mutate({ id: task.id, completed: !task.completedAt });
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
  const filtered = !!(priorityFilter || categoryFilter);
  const todayItems = agenda.data?.items ?? [];

  return (
    <div className="page todo-page">
      <PageHeader title="待办" />

      {/* Today stays quiet when nothing is due: the section simply isn't shown. */}
      {!(agenda.data && todayItems.length === 0) && (
      <section className="sec" aria-labelledby="todo-today">
        <h2 id="todo-today" className="sec-title">今天</h2>
        {agenda.isPending && (
          <div className="cal-grid">
            <Skeleton className="h-[146px] rounded-2xl" />
            <Skeleton className="h-[146px] rounded-2xl" />
          </div>
        )}
        {agenda.isError && <Notice tone="danger" title="今日待办加载失败">{agenda.error.message}</Notice>}
        {todayItems.length > 0 && (
          <div className="cal-grid">
            {todayItems.slice(0, 8).map((task) => (
              <TodayCard key={task.id} task={task} overdue={task.overdue} now={now} onToggle={() => toggle(task)} />
            ))}
          </div>
        )}
      </section>
      )}

      <section className="sec" aria-labelledby="todo-all">
        <h2 id="todo-all" className="sec-title">全部待办</h2>
        <div className="toolbar">
          <nav className="rtabs" aria-label="待办视图">
            {VIEWS.map((item) => (
              <Link
                key={item.id}
                to={viewLink(item.id)}
                className={cn("rtab sm focus-ring", item.id === view.id && "is-active")}
                aria-current={item.id === view.id ? "page" : undefined}
              >
                {item.label}
                {groups && counts[item.id] > 0 && <span className="rtab-count">{counts[item.id]}</span>}
              </Link>
            ))}
          </nav>
          <form
            className="todo-filter"
            onSubmit={(event) => { event.preventDefault(); applyFilters(priorityFilter, categoryDraft); }}
          >
            <Select
              aria-label="按优先级筛选"
              className="todo-filter-priority"
              value={priorityFilter}
              onValueChange={(value) => applyFilters(value as "" | TaskPriority, categoryDraft)}
              options={PRIORITY_FILTERS}
            />
            <input
              aria-label="按分类筛选"
              value={categoryDraft}
              onChange={(event) => setCategoryDraft(event.target.value)}
              maxLength={40}
              placeholder="分类"
              className="ui-input todo-filter-category"
            />
            <Button size="sm" variant="secondary">筛选</Button>
            {filtered && (
              <Button size="sm" type="button" variant="ghost" onClick={() => { setCategoryDraft(""); applyFilters("", ""); }}>清除</Button>
            )}
          </form>
        </div>

        <form onSubmit={submit} className="composer task-create">
          <span className="ck" aria-hidden="true" />
          <input
            ref={titleInput}
            aria-label="待办标题"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="添加待办，回车保存"
            className="composer-input"
          />
          <div className="composer-opts">
            <TodoDeadline value={dueAt} onChange={setDueAt} />
            <Select aria-label="优先级" className="composer-select" value={priority} onValueChange={(value) => setPriority(value as TaskPriority)} options={PRIORITIES} />
            <input
              aria-label="分类"
              value={category}
              onChange={(event) => setCategory(event.target.value)}
              maxLength={40}
              placeholder="分类"
              className="ui-input composer-category"
            />
            <Button size="sm" disabled={create.isPending || !title.trim()}>
              <Plus size={15} />
              添加
            </Button>
          </div>
        </form>

        {tasks.isLoading && (
          <div className="mt-2 space-y-2">
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
          </div>
        )}
        {tasks.isError && (
          <Notice tone="danger" title="待办加载失败">
            {tasks.error.message}
          </Notice>
        )}
        {tasks.data && items.length === 0 && (
          <EmptyState
            title="暂无待办"
          />
        )}
        <div className="todo-groups">
          {visible.map((group) => (
            <section key={group.id} className="todo-group" aria-labelledby={`todo-group-${group.id}`} data-group={group.id}>
              <h3 id={`todo-group-${group.id}`} className="todo-group-title">
                {group.title}
                <span>{grouped.get(group.id)?.length}</span>
              </h3>
              {grouped.get(group.id)?.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  now={now}
                  overdue={group.id === "overdue"}
                  onToggle={() => toggle(task)}
                  onDelete={(element) => deletion.request(task, element)}
                />
              ))}
            </section>
          ))}
        </div>
        {tasks.hasNextPage && (
          <div className="mt-4 flex justify-center">
            <Button variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => tasks.fetchNextPage()}>
              {tasks.isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </section>
      <ConfirmDialog {...deletion.dialogProps} />
    </div>
  );
}

function CheckButton({ task, onToggle, className }: { task: Task; onToggle: () => void; className?: string }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      role="checkbox"
      aria-checked={!!task.completedAt}
      aria-label={`${task.completedAt ? "标记为未完成" : "标记为完成"}：${task.title}`}
      className={cn("ck focus-ring", task.completedAt && "done", className)}
    />
  );
}

function PriorityBadge({ priority }: { priority: TaskPriority }) {
  if (priority === "none") return null;
  const badge = PRIORITY_BADGE[priority];
  return <Badge tone={badge.tone} aria-label={`${badge.label}优先级`}>{badge.label}</Badge>;
}

function TodayCard({ task, overdue, now, onToggle }: { task: Task; overdue: boolean; now: Date; onToggle: () => void }) {
  const note = task.description.split("\n")[0]?.trim();
  return (
    <article className="cal todo-card">
      <div className="cal-top">
        <Badge tone={overdue ? "warning" : "info"}>
          {overdue ? `已逾期 · ${task.dueAt ? dayLabel(task.dueAt, now) : ""}` : task.dueAt ? dayLabel(task.dueAt, now) : "今天"}
        </Badge>
        <CheckButton task={task} onToggle={onToggle} />
      </div>
      <h3 className="cal-title">{displayTitle(task)}</h3>
      <dl className="kvs">
        <div>
          <dt>优先级</dt>
          <dd>{task.priority === "none" ? "—" : <PriorityBadge priority={task.priority} />}</dd>
        </div>
        <div>
          <dt>分类</dt>
          <dd>{task.category === "renewal" ? "续费" : task.category || "—"}</dd>
        </div>
        <div className="min-w-0">
          <dt>备注</dt>
          <dd className="truncate">{note || "—"}</dd>
        </div>
      </dl>
    </article>
  );
}

function TaskRow({
  task,
  now,
  overdue,
  onToggle,
  onDelete,
}: {
  task: Task;
  now: Date;
  overdue: boolean;
  onToggle: () => void;
  onDelete: (element: HTMLElement) => void;
}) {
  const wallos = task.category === "renewal";
  const meta = [wallos ? "续费" : task.category, task.description.replace(/\s+/g, " ").trim()].filter(Boolean).join(" · ");
  const when = task.completedAt
    ? `完成 ${dayLabel(task.completedAt, now)}`
    : task.dueAt ? `${overdue ? "逾期 " : ""}${dayLabel(task.dueAt, now)}` : "";
  return (
    <div className={cn("trow task-row", task.completedAt && "is-done")}>
      <CheckButton task={task} onToggle={onToggle} />
      <div className="nm">
        <b>{displayTitle(task)}</b>
        {meta && <small>{meta}</small>}
      </div>
      <div className="rt">
        <PriorityBadge priority={task.priority} />
        {when && <span className={cn("task-when", overdue && "is-overdue")}>{when}</span>}
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
    </div>
  );
}
