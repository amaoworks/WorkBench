import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { PageHeader } from "../../components/ui/PageHeader";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { useOverview } from "./queries";

export default function InvestmentPage() {
  const overview = useOverview();
  const client = useQueryClient();
  const refresh = async () => { await Promise.all([client.invalidateQueries({ queryKey: ["investment"] }), client.invalidateQueries({ queryKey: ["widget", "investment.overview"] })]); };
  const sync = useMutation({ mutationFn: () => api("/api/modules/investment/sync", { method: "POST", body: "{}" }), onSuccess: async () => { await refresh(); toast.success("模拟行情已同步"); }, onError: (err) => toast.error(err.message) });
  const summary = useMutation({ mutationFn: () => api("/api/modules/investment/summary", { method: "POST", body: "{}" }), onSuccess: refresh, onError: (err) => toast.error(err.message) });
  return <div className="page"><PageHeader title="模拟行情" description="使用虚构数据体验行情同步、波动提醒和 AI 摘要。" />
    <div className="mb-4 flex flex-wrap gap-3"><Button disabled={sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? "同步中…" : "同步模拟行情"}</Button><Button variant="secondary" disabled={summary.isPending || !overview.data?.items.length} onClick={() => summary.mutate()}>{summary.isPending ? "正在生成…" : "生成 AI 摘要"}</Button></div>
    <p className="mb-5 text-sm text-[var(--muted)]">行情每五分钟自动同步；变动达到 ±2% 时产生站内提醒。生成摘要会把模拟行情发送给设置中的 AI 服务。</p>
    {overview.isPending && <Skeleton className="h-48" />}
    {overview.isError && <p role="alert" className="text-danger">{overview.error.message}</p>}
    {overview.data && <><Card className="mb-5"><CardHeader title="模拟品种" description="所有价格均为演示数据" />{overview.data.items.length === 0 ? <EmptyState title="尚无行情" description="点击同步模拟行情开始体验。" /> : <div className="overflow-x-auto"><table className="w-full text-left text-sm"><thead><tr><th className="p-4">品种</th><th className="p-4">模拟价格</th><th className="p-4">变动</th><th className="p-4">数据时间</th></tr></thead><tbody>{overview.data.items.map((quote) => <tr key={quote.symbol} className="border-t border-[var(--border)]"><td className="p-4">{quote.name}<small className="block text-[var(--muted)]">{quote.symbol}</small></td><td className="p-4">{(quote.priceCents / 100).toFixed(2)}</td><td className="p-4">{(quote.changeBps / 100).toFixed(2)}%</td><td className="p-4">{new Date(quote.asOf).toLocaleString()}</td></tr>)}</tbody></table></div>}</Card>
    <Card><CardHeader title="AI 行情摘要" description={overview.data.summary ? `生成于 ${new Date(overview.data.summary.createdAt).toLocaleString()}` : "按需生成，保存在工作空间"} /><p className="whitespace-pre-wrap p-5 text-sm">{overview.data.summary?.content ?? "尚未生成摘要。AI 未启用时，仍可浏览和同步行情。"}</p></Card></>}
  </div>;
}
