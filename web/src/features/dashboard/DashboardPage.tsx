import { useQuery } from "@tanstack/react-query";
import { Activity, Sparkles } from "lucide-react";
import { api } from "../../shared/api";
import { dashboardSchema, type Widget } from "../../shared/schema";
import { Card, CardHeader, StatCard } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";

const widgetRegistry: Record<string, React.ComponentType<{ widget: Widget }>> = {
  "todo.summary": TodoSummaryWidget
};

export default function DashboardPage() {
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: async () => dashboardSchema.parse(await api<unknown>("/api/dashboard")) });
  return (
    <div>
      <div className="mb-7 flex items-end justify-between"><div><p className="mb-1 flex items-center gap-1.5 text-xs font-medium text-indigo-500"><Sparkles size={14} />PERSONAL WORKSPACE</p><h1 className="text-2xl font-semibold tracking-tight">今天，从重要的事情开始</h1><p className="mt-1 text-[var(--muted)]">你的模块状态和近期行动集中在这里。</p></div></div>
      {dashboard.isLoading && <div className="grid gap-4 md:grid-cols-3"><Skeleton className="h-32" /><Skeleton className="h-32" /><Skeleton className="h-32" /></div>}
      {dashboard.isError && <Card className="p-6 text-red-500">总览加载失败：{dashboard.error.message}</Card>}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {dashboard.data?.widgets.map((widget) => { const Component = widgetRegistry[widget.widgetKind]; return Component ? <Component key={widget.id} widget={widget} /> : <UnknownWidget key={widget.id} widget={widget} />; })}
      </div>
      <Card className="mt-4"><CardHeader title="每日简报" description="AI 可用时将在这里生成摘要" /><div className="flex items-center gap-3 px-5 py-6 text-[var(--muted)]"><div className="grid size-9 place-items-center rounded-lg bg-indigo-50 text-indigo-500 dark:bg-indigo-500/10"><Activity size={18} /></div><span>完成更多工作后，这里会出现你的每日进展。</span></div></Card>
    </div>
  );
}

function TodoSummaryWidget({ widget }: { widget: Widget }) {
  const summary = useQuery({ queryKey: ["widget", widget.id], queryFn: () => api<{ open: number; overdue: number; completed: number }>(widget.dataRoute) });
  if (summary.isLoading) return <Skeleton className="h-32" />;
  if (summary.isError) return <Card className="p-5 text-red-500">{widget.title}加载失败</Card>;
  return <div className="grid grid-cols-3 gap-3 md:col-span-2 xl:col-span-3"><StatCard label="未完成" value={summary.data?.open ?? 0} /><StatCard label="已逾期" value={summary.data?.overdue ?? 0} accent="amber" /><StatCard label="已完成" value={summary.data?.completed ?? 0} accent="emerald" /></div>;
}

function UnknownWidget({ widget }: { widget: Widget }) { return <Card className="p-5"><p className="font-medium">{widget.title}</p><p className="mt-2 text-sm text-amber-600">当前前端版本不认识组件：{widget.widgetKind}</p></Card>; }

