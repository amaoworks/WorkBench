import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { Card, StatCard } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";

const summarySchema = z.object({ open: z.number(), overdue: z.number(), completed: z.number() });
export default function TodoSummaryWidget({ widget }: { widget: Widget }) {
  const summary = useQuery({ queryKey: ["widget", widget.id], queryFn: async () => summarySchema.parse(await api<unknown>(widget.dataRoute)) });
  if (summary.isPending) return <Skeleton className="h-32" />;
  if (summary.isError) return <Card className="p-5 text-danger">{widget.title}加载失败</Card>;
  return <div className="dashboard-stats"><StatCard label="未完成" value={summary.data.open} /><StatCard label="已逾期" value={summary.data.overdue} accent="warning" /><StatCard label="已完成" value={summary.data.completed} accent="success" /></div>;
}
