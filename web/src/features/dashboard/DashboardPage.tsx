import { useQuery } from "@tanstack/react-query";
import { Activity } from "lucide-react";
import { api } from "../../shared/api";
import { dashboardSchema, type Widget } from "../../shared/schema";
import { Card, CardHeader, StatCard } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { PageHeader } from "../../components/ui/PageHeader";

const widgetRegistry: Record<string, React.ComponentType<{ widget: Widget }>> = {
  "todo.summary": TodoSummaryWidget
};

export default function DashboardPage() {
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: async () => dashboardSchema.parse(await api<unknown>("/api/dashboard")) });
  return (
    <div className="page">
      <PageHeader title="总览" description="查看工作进展，安排接下来的行动。" />
      {dashboard.isLoading && <div className="grid gap-4 md:grid-cols-3"><Skeleton className="h-32" /><Skeleton className="h-32" /><Skeleton className="h-32" /></div>}
      {dashboard.isError && <Card className="p-6 text-danger">总览加载失败：{dashboard.error.message}</Card>}
      <div className="grid grid-cols-1 gap-4">
        {dashboard.data?.widgets.map((widget) => { const Component = widgetRegistry[widget.widgetKind]; return Component ? <Component key={widget.id} widget={widget} /> : <UnknownWidget key={widget.id} widget={widget} />; })}
      </div>
      <Card className="mt-5"><CardHeader title="每日简报" description="工作空间的进展摘要" /><div className="flex items-center gap-3 px-5 py-6 text-[var(--muted)]"><div className="icon-tile"><Activity size={18} /></div><span className="text-sm">暂无简报，先从一项待办开始。</span></div></Card>
    </div>
  );
}

function TodoSummaryWidget({ widget }: { widget: Widget }) {
  const summary = useQuery({ queryKey: ["widget", widget.id], queryFn: () => api<{ open: number; overdue: number; completed: number }>(widget.dataRoute) });
  if (summary.isLoading) return <Skeleton className="h-32" />;
  if (summary.isError) return <Card className="p-5 text-danger">{widget.title}加载失败</Card>;
  return <div className="dashboard-stats"><StatCard label="未完成" value={summary.data?.open ?? 0} /><StatCard label="已逾期" value={summary.data?.overdue ?? 0} accent="warning" /><StatCard label="已完成" value={summary.data?.completed ?? 0} accent="success" /></div>;
}

function UnknownWidget({ widget }: { widget: Widget }) { return <Card className="p-5"><p className="font-medium">{widget.title}</p><p className="mt-2 text-sm text-warning">当前版本暂不支持此组件。</p></Card>; }
