import type { Widget } from "../../shared/schema";
import { StatCard } from "../../components/ui/Card";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useTodoSummary } from "./queries";

export default function TodoSummaryWidget({ widget }: { widget: Widget }) {
  const summary = useTodoSummary(widget);
  if (summary.isPending) return <Skeleton className="h-32" />;
  if (summary.isError)
    return (
      <Notice tone="danger" title={`${widget.title}加载失败`}>
        {summary.error.message}
      </Notice>
    );
  return (
    <div className="dashboard-stats">
      <StatCard label="未完成" value={summary.data.open} />
      <StatCard label="已逾期" value={summary.data.overdue} accent="warning" />
      <StatCard label="已完成" value={summary.data.completed} accent="success" />
    </div>
  );
}
