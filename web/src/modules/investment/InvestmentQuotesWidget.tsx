import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ExternalLink } from "lucide-react";
import type { Widget } from "../../shared/schema";
import { Badge } from "../../components/ui/Badge";
import { ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { WidgetHeader } from "../../components/dashboard/WidgetHeader";
import { cn } from "../../shared/cn";
import { formatClock, formatSignedPct } from "../../shared/format";
import { useOverviewQuotes } from "./queries";
import type { OverviewSession } from "./schema";

const sessions: Record<OverviewSession, { label: string; tone: "neutral" | "success" | "warning" | "danger" }> = {
  pre: { label: "盘前", tone: "warning" },
  regular: { label: "盘中", tone: "success" },
  post: { label: "盘后", tone: "warning" },
  overnight: { label: "夜盘", tone: "danger" },
  closed: { label: "休市", tone: "neutral" },
};

const terminalHref = (symbol: string) => `/investment?view=terminal&symbol=${encodeURIComponent(symbol)}`;

function Sparkline({ points, up }: { points: number[]; up: boolean }) {
  if (points.length < 2) return <span className="ov-spark" aria-hidden="true" />;
  const min = Math.min(...points);
  const span = Math.max(...points) - min || 1;
  const path = points.map((value, index) => `${((index / (points.length - 1)) * 96).toFixed(1)},${(25 - ((value - min) / span) * 22).toFixed(1)}`).join(" ");
  return (
    <svg className="ov-spark" viewBox="0 0 96 26" preserveAspectRatio="none" aria-hidden="true">
      <polyline points={path} fill="none" stroke={up ? "var(--ov-up)" : "var(--ov-down)"} strokeWidth="1.6" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

type Tab = "held" | "watched";

export default function InvestmentQuotesWidget({ widget }: { widget: Widget }) {
  const quotes = useOverviewQuotes(widget);
  const [tab, setTab] = useState<Tab>("held");
  const items = quotes.data?.items;
  const counts = useMemo(() => ({ held: items?.filter((item) => item.held).length ?? 0, watched: items?.filter((item) => item.watched && !item.held).length ?? 0 }), [items]);

  if (quotes.isPending) return <Skeleton className="h-40" />;
  if (quotes.isError)
    return (
      <Notice tone="danger" title="行情加载失败">
        {quotes.error.message}
      </Notice>
    );
  const data = quotes.data;
  // Setup and authorization prompts live on the snapshot card; an empty quote card stays hidden.
  if (data.items.length === 0) return null;
  const active: Tab = counts[tab] > 0 ? tab : counts.held > 0 ? "held" : "watched";
  const rows = data.items.filter((item) => (active === "held" ? item.held : item.watched && !item.held));
  const session = sessions[data.session];
  return (
    <>
      <WidgetHeader>
        <Badge tone={session.tone}>{session.label}</Badge>
        {counts.held > 0 && counts.watched > 0 && (
          <div className="ov-tabs" role="tablist" aria-label="行情分组">
            {(["held", "watched"] as const).map((key) => (
              <button key={key} type="button" role="tab" aria-selected={active === key} className={cn(active === key && "is-active")} onClick={() => setTab(key)}>
                {key === "held" ? "持仓" : "自选"}
              </button>
            ))}
          </div>
        )}
      </WidgetHeader>
      <div className="ov-table-wrap">
        <table className="ov-table">
          <thead>
            <tr>
              <th>代码</th>
              <th>现价</th>
              <th>涨跌幅</th>
              <th className="ov-hide-sm">时段</th>
              <th>今日走势</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((item) => {
              const badge = sessions[item.session];
              return (
                <tr key={item.symbol}>
                  <td>
                    <Link to={terminalHref(item.symbol)} className="ov-symbol focus-ring" title="在投资终端中打开">
                      <b>{item.symbol}</b>
                      {item.description && <span>{item.description}</span>}
                    </Link>
                  </td>
                  <td>{item.price.toFixed(2)}</td>
                  <td>
                    <span className={cn("ov-chg", item.changePct >= 0 ? "ov-chg-up" : "ov-chg-down")}>{formatSignedPct(item.changePct)}</span>
                  </td>
                  <td className="ov-hide-sm">
                    <Badge tone={badge.tone}>{badge.label}</Badge>
                    {item.extChangePct !== undefined && <span className={cn("ov-ext", item.extChangePct >= 0 ? "ov-up" : "ov-down")}>{formatSignedPct(item.extChangePct)}</span>}
                  </td>
                  <td>
                    <Sparkline points={item.spark ?? []} up={item.changePct >= 0} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="ov-card-foot">
        <span>
          报价来自 Schwab{data.updatedAt ? ` · ${formatClock(data.updatedAt)} 更新` : ""}
          {data.status !== "ok" ? " · 数据可能滞后" : ""} · 点击代码在终端中打开
        </span>
        <ButtonLink size="sm" to="/investment?view=terminal">
          打开终端
          <ExternalLink size={13} />
        </ButtonLink>
      </div>
    </>
  );
}
