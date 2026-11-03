import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import {
  createTask, deleteTask, loadTaskForEdit, updateTask, useRefreshTodo, useTaskCounts, useTasks, useTodoAgenda,
  type TaskCreateInput, type TaskUpdateInput,
} from "./queries";
import {
  emptyTaskDraft, mergeTaskDraft, prepareTaskCreate, taskDraft, taskEditInput, type TaskDraft, type TaskDraftField,
} from "./taskActions";
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
import { Field } from "../../components/ui/Field";
import { APIError } from "../../shared/api";

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
  const [newTask, setNewTask] = useState(emptyTaskDraft);
  const createAttempt = useRef<TaskCreateInput | null>(null);
  function changeNewTask<K extends TaskDraftField>(field: K, value: TaskDraft[K]) {
    if (newTask[field] === value) return;
    createAttempt.current = null;
    setNewTask((current) => ({ ...current, [field]: value }));
  }
  const priorityFilter = (PRIORITIES.some((item) => item.value === params.get("priority")) ? params.get("priority") : "") as "" | TaskPriority;
  const categoryFilter = params.get("category")?.trim() ?? "";
  const [categoryDraft, setCategoryDraft] = useState(categoryFilter);
  const titleInput = useRef<HTMLInputElement>(null);
  const editTrigger = useRef<HTMLElement | null>(null);
  const [editing, setEditing] = useState<Task | null>(null);
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
    mutationFn: createTask,
    retry: false,
    onSuccess: (_task, input) => {
      if (createAttempt.current === input) {
        createAttempt.current = null;
        setNewTask(emptyTaskDraft());
      }
      refresh();
      toast.success("待办已创建");
    },
    onError: (error) => toast.error(error.message),
  });
  const update = useMutation({
    mutationFn: updateTask,
    retry: false,
    onSuccess: refresh,
    onError: (error) => {
      if (error instanceof APIError && error.status === 409) void refresh();
      toast.error(error.message);
    },
  });
  const remove = useMutation({
    mutationFn: deleteTask,
    retry: false,
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
    if (newTask.title.trim() && !create.isPending) {
      createAttempt.current = prepareTaskCreate(createAttempt.current, newTask);
      create.mutate(createAttempt.current);
    }
  }
  const toggle = (task: Task) => update.mutate({ id: task.id, completed: !task.completedAt, revision: task.revision });
  const edit = (task: Task, element: HTMLElement) => {
    editTrigger.current = element;
    setEditing(task);
  };
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
              <TodayCard key={task.id} task={task} overdue={task.overdue} now={now} onToggle={() => toggle(task)} onEdit={(element) => edit(task, element)} />
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
            value={newTask.title}
            onChange={(event) => changeNewTask("title", event.target.value)}
            placeholder="添加待办，回车保存"
            className="composer-input"
          />
          <div className="composer-opts">
            <input
              aria-label="备注"
              value={newTask.description}
              onChange={(event) => changeNewTask("description", event.target.value)}
              maxLength={10000}
              placeholder="备注"
              className="ui-input min-w-40 flex-1"
            />
            <TodoDeadline
              value={newTask.dueAt ?? ""}
              onChange={(value) => changeNewTask("dueAt", value ? new Date(value).toISOString() : null)}
            />
            <Select
              aria-label="优先级"
              className="composer-select"
              value={newTask.priority}
              onValueChange={(value) => changeNewTask("priority", value as TaskPriority)}
              options={PRIORITIES}
            />
            <input
              aria-label="分类"
              value={newTask.category}
              onChange={(event) => changeNewTask("category", event.target.value)}
              maxLength={40}
              placeholder="分类"
              className="ui-input composer-category"
            />
            <Button size="sm" disabled={create.isPending || !newTask.title.trim()}>
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
                  onEdit={(element) => edit(task, element)}
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
      {editing && (
        <EditTaskDialog
          key={editing.id}
          task={editing}
          pending={update.isPending}
          onClose={() => setEditing(null)}
          onCloseAutoFocus={() => editTrigger.current?.focus()}
          onSave={async (input) => {
            await update.mutateAsync(input);
            setEditing(null);
            toast.success("待办已保存");
          }}
        />
      )}
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

function TodayCard({
  task, overdue, now, onToggle, onEdit,
}: {
  task: Task; overdue: boolean; now: Date; onToggle: () => void; onEdit: (element: HTMLElement) => void;
}) {
  const note = task.description.split("\n")[0]?.trim();
  return (
    <article className="cal todo-card">
      <div className="cal-top">
        <Badge tone={overdue ? "warning" : "info"}>
          {overdue ? `已逾期 · ${task.dueAt ? dayLabel(task.dueAt, now) : ""}` : task.dueAt ? dayLabel(task.dueAt, now) : "今天"}
        </Badge>
        <div className="flex items-center gap-1">
          <Button type="button" variant="ghost" size="icon" onClick={(event) => onEdit(event.currentTarget)} aria-label={`编辑 ${task.title}`}>
            <Pencil size={15} />
          </Button>
          <CheckButton task={task} onToggle={onToggle} />
        </div>
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
  onEdit,
  onDelete,
}: {
  task: Task;
  now: Date;
  overdue: boolean;
  onToggle: () => void;
  onEdit: (element: HTMLElement) => void;
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
        <Button variant="ghost" size="icon" onClick={(event) => onEdit(event.currentTarget)} aria-label={`编辑 ${task.title}`}>
          <Pencil size={16} />
        </Button>
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

const DRAFT_LABELS: Record<TaskDraftField, string> = {
  title: "标题", description: "备注", dueAt: "截止时间", priority: "优先级", category: "分类",
};

function draftValueLabel(field: TaskDraftField, draft: TaskDraft) {
  if (field === "priority") return PRIORITIES.find((item) => item.value === draft.priority)?.label;
  if (field === "dueAt" && draft.dueAt) return new Date(draft.dueAt).toLocaleString("zh-CN");
  return draft[field] || "（空）";
}

function EditTaskDialog({
  task, pending, onClose, onCloseAutoFocus, onSave,
}: {
  task: Task;
  pending: boolean;
  onClose: () => void;
  onCloseAutoFocus: () => void;
  onSave: (input: TaskUpdateInput) => Promise<void>;
}) {
  const [base, setBase] = useState(task);
  const [draft, setDraft] = useState(() => taskDraft(task));
  const [needsLatest, setNeedsLatest] = useState(false);
  const [conflicts, setConflicts] = useState<TaskDraftField[]>([]);
  const latest = useMutation({ mutationFn: loadTaskForEdit, retry: false });
  const remote = taskDraft(base);
  const blocked = pending || latest.isPending;

  function changeDraft<K extends TaskDraftField>(field: K, value: TaskDraft[K]) {
    setDraft((current) => ({ ...current, [field]: value }));
  }

  async function readLatest() {
    try {
      const task = await latest.mutateAsync(base.id);
      const merged = mergeTaskDraft(base, draft, task);
      setBase(task);
      setDraft(merged.draft);
      setConflicts(merged.conflicts);
      setNeedsLatest(false);
    } catch {
      // The inline error keeps the draft and offers another explicit read.
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!draft.title.trim() || blocked || needsLatest || conflicts.length > 0) return;
    try {
      await onSave(taskEditInput(base, draft));
    } catch (error) {
      if (error instanceof APIError && error.status === 409) {
        setNeedsLatest(true);
        latest.reset();
      }
    }
  }

  return (
    <Dialog.Root open onOpenChange={(open) => { if (!open) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="command-overlay z-40" />
        <Dialog.Content
          className="module-settings-dialog z-50 p-5"
          style={{ maxWidth: 520 }}
          onCloseAutoFocus={(event) => { event.preventDefault(); onCloseAutoFocus(); }}
        >
          <Dialog.Title className="font-semibold">编辑待办</Dialog.Title>
          <Dialog.Description className="sr-only">修改待办内容</Dialog.Description>
          <form className="mt-4 space-y-4" onSubmit={submit}>
            {needsLatest && (
              <Notice
                tone="warning"
                title="待办已在其他设备更新"
                action={(
                  <Button type="button" size="sm" variant="secondary" disabled={blocked} onClick={readLatest}>
                    {latest.isPending ? "读取中…" : "读取最新"}
                  </Button>
                )}
              >
                草稿已保留，读取最新内容后合并。
              </Notice>
            )}
            {latest.isError && <Notice tone="danger" title="读取失败">{latest.error.message}</Notice>}
            {latest.isSuccess && !needsLatest && (
              <Notice tone={conflicts.length ? "warning" : "info"} title={conflicts.length ? "存在冲突字段" : "最新内容已合并"}>
                {conflicts.length ? "选择保留的内容后再次保存。" : "草稿已保留，可检查后再次保存。"}
              </Notice>
            )}
            <fieldset disabled={blocked} className="min-w-0 space-y-4">
              <Field label="标题">
                <input
                  className="ui-input" value={draft.title} onChange={(event) => changeDraft("title", event.target.value)} maxLength={300} autoFocus
                />
              </Field>
              <Field label="备注">
                <textarea
                  className="ui-input min-h-24 resize-y" value={draft.description}
                  onChange={(event) => changeDraft("description", event.target.value)} maxLength={10000}
                />
              </Field>
              <Field label="截止时间">
                <div>
                  <TodoDeadline
                    value={draft.dueAt ?? ""}
                    onChange={(value) => changeDraft("dueAt", value ? new Date(value).toISOString() : null)}
                  />
                </div>
              </Field>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label="优先级">
                  <Select value={draft.priority} onValueChange={(value) => changeDraft("priority", value as TaskPriority)} options={PRIORITIES} />
                </Field>
                <Field label="分类">
                  <input className="ui-input" value={draft.category} onChange={(event) => changeDraft("category", event.target.value)} maxLength={40} />
                </Field>
              </div>
              {conflicts.map((field) => (
                <fieldset key={field} className="min-w-0 space-y-2 border-t border-[var(--border)] pt-2">
                  <legend className="text-sm font-semibold">{DRAFT_LABELS[field]}冲突</legend>
                  <p className="whitespace-pre-wrap break-words text-sm">草稿：{draftValueLabel(field, draft)}</p>
                  <p className="whitespace-pre-wrap break-words text-sm">最新：{draftValueLabel(field, remote)}</p>
                  <div className="flex flex-wrap gap-2">
                    <Button type="button" size="sm" variant="secondary" onClick={() => setConflicts((current) => current.filter((key) => key !== field))}>
                      保留草稿
                    </Button>
                    <Button
                      type="button" size="sm" variant="secondary"
                      onClick={() => {
                        changeDraft(field, remote[field]);
                        setConflicts((current) => current.filter((key) => key !== field));
                      }}
                    >
                      使用最新
                    </Button>
                  </div>
                </fieldset>
              ))}
            </fieldset>
            <div className="flex justify-end gap-2">
              <Dialog.Close asChild>
                <Button type="button" variant="secondary">取消</Button>
              </Dialog.Close>
              <Button disabled={blocked || needsLatest || conflicts.length > 0 || !draft.title.trim()}>{pending ? "保存中…" : "保存"}</Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
