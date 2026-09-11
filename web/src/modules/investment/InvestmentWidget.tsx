import { Link } from "react-router-dom";
import type { Widget } from "../../shared/schema";
import { Card } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { useInvestmentWidget } from "./queries";

export default function InvestmentWidget({ widget }: { widget: Widget }) {
  const schwab = useInvestmentWidget(widget);
  if (schwab.isPending) return <Skeleton className="h-32" />;
  if (schwab.isError) return <Card className="p-5 text-danger">投资加载失败</Card>;
  return <Card className="p-5">
    <p className="mb-3 text-sm">{schwab.data.connected ? "Schwab 已连接" : "尚未连接 Schwab"}</p>
    <p className="mb-4 text-sm text-[var(--muted)]">{schwab.data.connected ? "查看行情、持仓和订单。" : "请先完成授权。"}</p>
    {schwab.data.lastError && <p role="alert" className="mb-3 text-sm text-danger">{schwab.data.lastError}</p>}
    <Link className="text-sm underline" to={schwab.data.connected ? "/investment" : "/settings?tab=modules"}>{schwab.data.connected ? "打开投资终端" : "连接 Schwab"}</Link>
  </Card>;
}
