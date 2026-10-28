import { useId, useState } from "react";
import { Button } from "../../components/ui/Button";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Select } from "../../components/ui/Select";
import { Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { formatUSD } from "../../shared/format";
import { useNetWorthHistory } from "./queries";
import { netWorthSeries } from "./netWorthSeries";

const RANGES = [{ id: "1m", label: "1 个月" }, { id: "3m", label: "3 个月" }, { id: "1y", label: "1 年" }, { id: "all", label: "全部" }];

export function NetWorthHistory() {
  const [range, setRange] = useState("1m");
  const [account, setAccount] = useState("");
  const history = useNetWorthHistory(range, account);
  const id = useId();
  const points = history.data?.points ?? [];
  const chart = netWorthSeries(points);
  const coverageChanged = new Set(points.filter((point) => point.netWorth !== null).map((point) => [...point.accountHashes].sort().join(","))).size > 1;
  return (
    <section className="mt-5 min-w-0 border-t border-[var(--border)] pt-4" aria-labelledby={id}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h3 id={id} className="font-medium">净值历史</h3>
        <div className="rtabs" role="group" aria-label="净值时间范围">
          {RANGES.map((item) => (
            <button
              key={item.id} type="button" className={cn("rtab sm focus-ring", range === item.id && "is-active")}
              aria-pressed={range === item.id} onClick={() => setRange(item.id)}
            >{item.label}</button>
          ))}
        </div>
      </div>
      {(account !== "" || (history.data?.accounts.length ?? 0) > 1) && (
        <Select
          aria-label="净值账户" className="mb-3 w-48" value={account || "all"} onValueChange={(value) => setAccount(value === "all" ? "" : value)}
          options={[
            { value: "all", label: "全部账户" },
            ...(history.data?.accounts ?? (account ? [account] : [])).map((hash) => ({ value: hash, label: "账户 " + hash.slice(0, 8) })),
          ]}
        />
      )}
      {history.isPending ? <Skeleton className="h-40" /> : history.isError ? (
        <Notice tone="danger" title="净值历史加载失败" action={<Button variant="secondary" onClick={() => history.refetch()}>重试</Button>}>
          {history.error.message}
        </Notice>
      ) : history.data.status !== "ready" ? (
        <Notice title={history.data.message ?? "净值历史暂不可用"} />
      ) : chart.dots.length === 0 ? (
        <EmptyState title="暂无净值记录" />
      ) : (
        <>
          <svg viewBox="0 0 600 180" className="block w-full text-[var(--brand)]" role="img" aria-labelledby={id + "-chart"}>
            <title id={id + "-chart"}>账户净值（USD），{points[0]?.day} 至 {points.at(-1)?.day}</title>
            {chart.paths.map((path, index) => <path key={index} d={path} fill="none" stroke="currentColor" strokeWidth="2" />)}
            {chart.dots.map((dot) => (
              <circle key={dot.day} cx={dot.x} cy={dot.y} r="3" fill="currentColor">
                <title>{dot.day + "：" + formatUSD(dot.value)}</title>
              </circle>
            ))}
          </svg>
          <div className="flex flex-wrap justify-between gap-2 text-xs text-[var(--muted)]">
            <span>{points[0]?.day}</span><span>{formatUSD(chart.min)} — {formatUSD(chart.max)}</span><span>{points.at(-1)?.day}</span>
          </div>
          {coverageChanged && <p className="mt-2 text-xs text-[var(--muted)]">账户范围发生变化，曲线已分段。</p>}
          <details className="mt-3 text-xs">
            <summary className="focus-ring cursor-pointer text-[var(--muted)]">每日明细</summary>
            <div className="data-table-scroll mt-2">
              <table className="data-table">
                <thead><tr><th scope="col">日期</th><th scope="col">账户数</th><th scope="col" className="num">净值 (USD)</th></tr></thead>
                <tbody>{points.map((point) => (
                  <tr key={point.day}><th scope="row">{point.day}</th><td>{point.accountHashes.length}</td>
                    <td className="num">{point.netWorth === null ? "—" : formatUSD(point.netWorth)}</td></tr>
                ))}</tbody>
              </table>
            </div>
          </details>
        </>
      )}
    </section>
  );
}
