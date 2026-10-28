import { useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { PageHeader } from "../../components/ui/PageHeader";
import { Select } from "../../components/ui/Select";
import { Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { formatDateTime, formatSignedPct, formatSignedUSD, formatUSD } from "../../shared/format";
import { BrokerGate } from "./BrokerGate";
import { usePositions } from "./queries";

const ALL = "all";
const signClass = (value: number | null) => (value === null ? undefined : value > 0 ? "pnl-up" : value < 0 ? "pnl-down" : undefined);
const qty = new Intl.NumberFormat("en-US", { maximumFractionDigits: 4 });
const sum = (values: (number | null)[]) => values.some((value) => value === null)
  ? null : values.reduce<number>((total, value) => total + (value ?? 0), 0);
const usd = (value: number | null) => value === null ? "—" : formatUSD(value);
const signed = (value: number | null) => value === null ? "—" : formatSignedUSD(value);

export default function PositionsPage() {
  const positions = usePositions();
  const [selectedAccount, setAccount] = useState(ALL);
  const accounts = positions.data?.accounts ?? [];
  const account = accounts.some((item) => item.accountHash === selectedAccount) ? selectedAccount : ALL;
  const selected = accounts.filter((item) => account === ALL || item.accountHash === account);
  const rows = (positions.data?.items ?? []).filter((item) => account === ALL || item.accountHash === account);
  const marketValue = sum(selected.map((item) => item.invested));
  const totals = {
    netWorth: sum(selected.map((item) => item.netWorth)),
    cash: sum(selected.map((item) => item.cash)),
    day: sum(selected.map((item) => item.dayPnl)),
    open: sum(rows.map((item) => item.unrealizedPnl)),
  };
  const retry = <Button variant="secondary" size="sm" onClick={() => positions.refetch()}>重试</Button>;
  return (
    <div className="page positions-page">
      <PageHeader
        title="持仓"
        action={
          <Button
            variant="secondary"
            size="sm"
            disabled={positions.isFetching}
            title={positions.data?.updatedAt ? `更新于 ${formatDateTime(positions.data.updatedAt, { seconds: true })}` : undefined}
            onClick={() => positions.refetch()}
          >
            刷新
          </Button>
        }
      />
      {positions.isPending ? <Skeleton className="h-64" /> : positions.isError ? (
        <Notice tone="danger" title="持仓加载失败" action={retry}>{positions.error.message}</Notice>
      ) : (
        <BrokerGate status={positions.data.status} message={positions.data.message} onRetry={() => positions.refetch()}>
          <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
            <dl className="stat-tiles">
              <Stat label="账户总值" value={usd(totals.netWorth)} />
              <Stat label="持仓市值" value={usd(marketValue)} />
              <Stat label="现金" value={usd(totals.cash)} />
              <Stat label="今日盈亏" value={signed(totals.day)} tone={totals.day} />
              <Stat label="持仓盈亏" value={signed(totals.open)} tone={totals.open} />
            </dl>
            {accounts.length > 1 && (
              <Select
                aria-label="账户"
                className="w-44"
                value={account}
                onValueChange={setAccount}
                options={[{ value: ALL, label: "全部账户" }, ...accounts.map((item) => ({ value: item.accountHash, label: item.label }))]}
              />
            )}
          </div>
          <Card className="data-table-card">
            {rows.length === 0 ? <EmptyState title="暂无持仓" /> : (
              <div className="data-table-scroll" tabIndex={0} aria-label="持仓表格">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th scope="col">代码</th>
                      <th scope="col" className="num">数量</th>
                      <th scope="col" className="num">均价</th>
                      <th scope="col" className="num">现价</th>
                      <th scope="col" className="num">市值</th>
                      <th scope="col" className="num">今日盈亏</th>
                      <th scope="col" className="num">持仓盈亏</th>
                      <th scope="col" className="num">占比</th>
                      {account === ALL && accounts.length > 1 && <th scope="col">账户</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {rows.sort((a, b) => (b.marketValue ?? -Infinity) - (a.marketValue ?? -Infinity)).map((position) => {
                      const weight = position.marketValue !== null && marketValue !== null && marketValue !== 0
                        ? position.marketValue / marketValue * 100 : null;
                      return (
                        <tr key={position.accountHash + ":" + position.symbol}>
                          <th scope="row">
                            <Link className="symbol-link focus-ring" to={"/investment?symbol=" + encodeURIComponent(position.symbol)}>
                              {position.symbol}
                            </Link>
                            {position.description && <span className="data-table-sub">{position.description}</span>}
                          </th>
                          <td className="num">{position.quantity === null ? "—" : qty.format(position.quantity)}</td>
                          <td className="num">{usd(position.averagePrice)}</td>
                          <td className="num">{usd(position.lastPrice)}</td>
                          <td className="num">{usd(position.marketValue)}</td>
                          <td className={cn("num", signClass(position.dayPnl))}>
                            {signed(position.dayPnl)}
                            {position.dayPnlPct !== null && <span className="data-table-sub">{formatSignedPct(position.dayPnlPct)}</span>}
                          </td>
                          <td className={cn("num", signClass(position.unrealizedPnl))}>{signed(position.unrealizedPnl)}</td>
                          <td className="num">{weight === null ? "—" : weight.toFixed(1) + "%"}</td>
                          {account === ALL && accounts.length > 1 && <td>{position.accountLabel}</td>}
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </BrokerGate>
      )}
    </div>
  );
}

function Stat({ label, value, tone = null }: { label: string; value: string; tone?: number | null }) {
  return <div className="stat-tile"><dt>{label}</dt><dd className={signClass(tone)}>{value}</dd></div>;
}
