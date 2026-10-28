import { useEffect } from "react";
import type { Widget } from "../../shared/schema";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { WidgetHeader, WidgetSetup } from "../../components/dashboard/WidgetHeader";
import { useDashboardSignals } from "../../shared/dashboard";
import { cn } from "../../shared/cn";
import { formatClock, formatSignedPct, formatSignedUSD, formatUSD } from "../../shared/format";
import { useOverviewSnapshot } from "./queries";
import { NetWorthHistory } from "./NetWorthHistory";

const tone = (value: number) => (value > 0 ? "ov-up" : value < 0 ? "ov-down" : "");

export default function InvestmentSnapshotWidget({ widget }: { widget: Widget }) {
  const snapshot = useOverviewSnapshot(widget);
  const { setHeldSymbols } = useDashboardSignals();
  const symbols = snapshot.data?.symbols;
  useEffect(() => {
    if (symbols) setHeldSymbols(symbols);
  }, [symbols, setHeldSymbols]);

  if (snapshot.isPending) return <Skeleton className="h-36" />;
  if (snapshot.isError)
    return (
      <Notice tone="danger" title="资产快照加载失败">
        {snapshot.error.message}
      </Notice>
    );
  const data = snapshot.data;
  if (data.status === "disconnected")
    return (
      <WidgetSetup action={<ButtonLink size="sm" variant="secondary" to="/settings?tab=modules">去配置</ButtonLink>}>
        未连接 Schwab
      </WidgetSetup>
    );
  if (data.status === "reauth")
    return (
      <WidgetSetup action={<Button size="sm" onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>重新授权</Button>}>
        Schwab 授权已失效
      </WidgetSetup>
    );
  const hasData = data.accounts > 0;
  if (!hasData)
    return (
      <Notice tone="danger" title="暂时无法读取账户">
        {data.message || "请稍后重试。"}
      </Notice>
    );
  const maxMove = Math.max(1, ...data.movers.map((mover) => Math.abs(mover.dayPnl)));
  return (
    <>
      <WidgetHeader>
        <span className="ov-sub">
          Schwab · {data.accounts} 个账户{data.updatedAt ? ` · ${formatClock(data.updatedAt)} 更新` : ""}
        </span>
        {data.stale && <Badge tone="warning">数据可能滞后</Badge>}
      </WidgetHeader>
      <div className="ov-snap">
        <div>
          <div className="ov-label">账户净值 (USD)</div>
          <div className="ov-nlv">{formatUSD(data.netWorth)}</div>
          <div className="text-sm">
            <span className={cn("font-semibold", tone(data.dayPnl))}>
              {formatSignedUSD(data.dayPnl)} ({formatSignedPct(data.dayPnlPct)})
            </span>{" "}
            <span className="text-[var(--muted)]">今日</span>
          </div>
          <div className="ov-kv">
            <div>
              <span>现金</span>
              <b>{formatUSD(data.cash)}</b>
            </div>
            <div>
              <span>购买力</span>
              <b>{formatUSD(data.buyingPower)}</b>
            </div>
            <div>
              <span>持仓市值</span>
              <b>{formatUSD(data.invested)}</b>
            </div>
          </div>
        </div>
        <div>
          <div className="ov-label ov-row-between">
            <span>今日贡献最大</span>
            <span>当日盈亏</span>
          </div>
          {data.movers.length === 0 ? (
            <p className="text-sm text-[var(--muted)]">{data.positions === 0 ? "暂无持仓" : "今日无明显波动"}</p>
          ) : (
            <ul className="ov-movers" aria-label="今日贡献最大">
              {data.movers.map((mover) => (
                <li key={mover.symbol} title={mover.description}>
                  <span className="ov-sym">{mover.symbol}</span>
                  <span className="ov-bar" aria-hidden="true">
                    <i className={mover.dayPnl >= 0 ? "ov-bg-up" : "ov-bg-down"} style={{ width: `${(Math.abs(mover.dayPnl) / maxMove) * 100}%` }} />
                  </span>
                  <span className={cn("ov-num", tone(mover.dayPnl))}>{formatSignedUSD(mover.dayPnl, { whole: true })}</span>
                  <span className={cn("ov-num ov-pct", tone(mover.dayPnlPct))}>{formatSignedPct(mover.dayPnlPct)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
      <NetWorthHistory />
    </>
  );
}
