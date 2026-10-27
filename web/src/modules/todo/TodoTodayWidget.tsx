import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Repeat } from "lucide-react";
import type { Widget } from "../../shared/schema";
import { Badge } from "../../components/ui/Badge";
import { ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { WidgetHeader } from "../../components/dashboard/WidgetHeader";
import { cn } from "../../shared/cn";
import { formatClock } from "../../shared/format";
import { useOverviewSummary } from "../../shared/dashboard";
import { moduleQueryKey } from "../../shared/queryKeys";
import { updateTask, useTodoToday } from "./queries";
import type { TodoToday } from "./schema";

type Item = TodoToday["items"][number];
const DAY = 86_400_000;

function dueLabel(item: Item): { text: string; tone: "danger" | "warning" } | null {
  if (!item.dueAt) return null;
  const due = new Date(item.dueAt);
  if (!item.overdue) return { text: `今天 ${formatClock(due)}`, tone: "warning" };
  const days = Math.floor((Date.now() - due.getTime()) / DAY);
  return { text: days >= 1 ? `逾期 ${days} 天` : `已逾期 · ${formatClock(due)}`, tone: "danger" };
}

function renewalDays(paymentDate: string) {
  const [year = 1970, month = 1, day = 1] = paymentDate.split("-").map(Number);
  const target = new Date(year, month - 1, day).getTime();
  const today = new Date();
  return Math.round((target - new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime()) / DAY);
}

export default function TodoTodayWidget({ widget }: { widget: Widget }) {
  const today = useTodoToday(widget);
  const client = useQueryClient();
  // Tasks completed from this card stay visible (struck through) until the page is left, so a misclick can be undone.
  const [done, setDone] = useState<Item[]>([]);
  const toggle = useMutation({
    mutationFn: ({ item, completed }: { item: Item; completed: boolean }) => updateTask({ id: item.id, completed }),
    onSuccess: (_task, { item, completed }) => {
      setDone((current) => (completed ? [...current.filter((entry) => entry.id !== item.id), item] : current.filter((entry) => entry.id !== item.id)));
      return client.invalidateQueries({ queryKey: moduleQueryKey("todo") });
    },
  });

  const summaryData = today.data;
  const nextItem = summaryData?.items[0];
  const summaryCompleted = summaryData?.completedToday ?? 0;
  const summaryTotal = (summaryData?.total ?? 0) + summaryCompleted;
  useOverviewSummary(
    widget.widgetKind,
    summaryData && (summaryTotal > 0 || summaryData.overdue > 0)
      ? {
          title: "今日待办",
          value: summaryCompleted,
          unit: `/ ${summaryTotal} 项完成${summaryData.overdue > 0 ? ` · ${summaryData.overdue} 项逾期` : ""}`,
          phrase: `${summaryCompleted}/${summaryTotal} 项完成`,
          detail: nextItem ? `下一项：${nextItem.title}` : undefined,
          to: "/todo?view=today",
        }
      : null,
  );

  if (today.isPending) return <Skeleton className="h-32" />;
  if (today.isError)
    return (
      <Notice tone="danger" title="待办加载失败">
        {today.error.message}
      </Notice>
    );
  const data = today.data;
  const doneIds = new Set(done.map((item) => item.id));
  const open = data.items.filter((item) => !doneIds.has(item.id));
  const renewals = data.renewals ?? [];
  const completedToday = data.completedToday ?? 0;
  const dayTotal = data.total + completedToday;
  const progress = dayTotal > 0 ? Math.round((completedToday / dayTotal) * 100) : 0;
  if (open.length === 0 && done.length === 0 && renewals.length === 0 && completedToday === 0) return null;
  const visibleCompleted = data.items.filter((item) => doneIds.has(item.id)).length;
  const more = Math.max(0, data.total - open.length - visibleCompleted);
  return (
    <>
      <WidgetHeader>
        {data.overdue > 0 ? <Badge tone="danger">{data.overdue} 项逾期</Badge> : open.length > 0 && <Badge>{data.total} 项</Badge>}
      </WidgetHeader>
      {dayTotal > 0 && (
        <div className="mb-4" aria-label={`今日完成 ${completedToday}/${dayTotal}`}>
          <div className="mb-1 flex items-center justify-between text-xs text-[var(--muted)]">
            <span>今日完成</span>
            <span>{completedToday}/{dayTotal}</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-[var(--soft)]">
            <div className="h-full rounded-full bg-[var(--cta)]" style={{ width: `${progress}%` }} />
          </div>
        </div>
      )}
      {(open.length > 0 || done.length > 0) && (
        <>
          <p className="ov-section">任务</p>
          <ul className="ov-list" aria-label="今天要处理的任务">
            {[...open.map((item) => ({ item, completed: false })), ...done.map((item) => ({ item, completed: true }))].map(({ item, completed }) => {
              const due = dueLabel(item);
              return (
                <li key={item.id} className="ov-item">
                  <button
                    type="button"
                    role="checkbox"
                    aria-checked={completed}
                    aria-label={`${completed ? "恢复" : "完成"}：${item.title}`}
                    className={cn("ov-check focus-ring", completed && "is-done")}
                    disabled={toggle.isPending && toggle.variables?.item.id === item.id}
                    onClick={() => toggle.mutate({ item, completed: !completed })}
                  >
                    {completed && <Check size={12} strokeWidth={3} />}
                  </button>
                  <div className="ov-item-text">
                    <p className={cn("ov-item-title", completed && "is-done")}>{item.title}</p>
                    <p className="ov-note">
                      {completed ? "已完成" : due && <Badge tone={due.tone}>{due.text}</Badge>}
                      {!completed && !due && "无截止时间"}
                    </p>
                  </div>
                </li>
              );
            })}
          </ul>
          {toggle.isError && <p className="ov-note ov-down">操作失败：{toggle.error.message}</p>}
        </>
      )}
      {renewals.length > 0 && (
        <>
          <p className="ov-section">
            <Repeat size={13} />
            即将续费 · Wallos
          </p>
          <ul className="ov-list" aria-label="即将续费">
            {renewals.map((renewal) => {
              const days = renewalDays(renewal.paymentDate);
              return (
                <li key={renewal.taskId} className="ov-item">
                  <div className="ov-item-text">
                    <p className="ov-item-title">{renewal.name}</p>
                    <p className="ov-note">
                      {renewal.paymentDate.slice(5).replace("-", "/")}
                      {renewal.amount ? ` · ${renewal.amount}` : ""}
                    </p>
                  </div>
                  <Badge tone={days <= 2 ? "warning" : "neutral"}>{days <= 0 ? "今天" : `${days} 天`}</Badge>
                </li>
              );
            })}
          </ul>
        </>
      )}
      {more > 0 && (
        <div className="ov-card-foot">
          <span>还有 {more} 项未显示</span>
          <ButtonLink size="sm" variant="secondary" to="/todo">
            查看全部
          </ButtonLink>
        </div>
      )}
    </>
  );
}
