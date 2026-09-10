import { Link } from "react-router-dom";
import type { Widget } from "../../shared/schema";
import { Card } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { useInvestmentWidget } from "./queries";
export default function InvestmentWidget({ widget }: { widget: Widget }) {
  const overview = useInvestmentWidget(widget);
  if (overview.isPending) return <Skeleton className="h-32" />;
  if (overview.isError) return <Card className="p-5 text-danger">模拟行情加载失败</Card>;
  return <Card className="p-5"><p className="mb-3 text-sm text-[var(--muted)]">虚构模拟数据</p>{overview.data.items.map((quote) => <div key={quote.symbol} className="flex justify-between gap-3 py-2"><span>{quote.name}</span><span>{(quote.priceCents / 100).toFixed(2)} · {(quote.changeBps / 100).toFixed(2)}%</span></div>)}{overview.data.items.length === 0 && <p className="mb-3 text-sm">尚未同步行情</p>}<Link className="text-sm underline" to="/investment">查看模拟行情</Link></Card>;
}
