import { Link, useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { PageHeader } from "../../components/ui/PageHeader";
import { Skeleton } from "../../components/ui/States";
import { Badge } from "../../components/ui/Badge";
import { cn } from "../../shared/cn";
import { formatDateTime, formatUSD } from "../../shared/format";
import { BrokerGate } from "./BrokerGate";
import { useOrders } from "./queries";

const RANGES = [
  { id: "1", label: "今天" },
  { id: "7", label: "7 天" },
  { id: "30", label: "30 天" },
  { id: "90", label: "90 天" },
] as const;

const STATUSES = [
  { id: "all", label: "全部" },
  { id: "working", label: "进行中" },
  { id: "filled", label: "已成交" },
  { id: "canceled", label: "已撤销" },
] as const;

const STATUS_LABEL: Record<string, string> = {
  FILLED: "已成交", WORKING: "进行中", QUEUED: "排队中", ACCEPTED: "已受理", PENDING_ACTIVATION: "待激活", NEW: "新订单",
  CANCELED: "已撤销", REJECTED: "已拒绝", EXPIRED: "已过期", REPLACED: "已改单", PENDING_CANCEL: "撤单中", PENDING_REPLACE: "改单中",
  AWAITING_PARENT_ORDER: "等待母单", AWAITING_CONDITION: "等待条件", AWAITING_STOP_CONDITION: "等待触发", AWAITING_MANUAL_REVIEW: "人工审核",
};
const INSTRUCTION_LABEL: Record<string, string> = {
  BUY: "买入", SELL: "卖出", BUY_TO_COVER: "买入平仓", SELL_SHORT: "卖空", BUY_TO_OPEN: "买入开仓", BUY_TO_CLOSE: "买入平仓",
  SELL_TO_OPEN: "卖出开仓", SELL_TO_CLOSE: "卖出平仓",
};
const TYPE_LABEL: Record<string, string> = { MARKET: "市价", LIMIT: "限价", STOP: "止损", STOP_LIMIT: "止损限价", TRAILING_STOP: "追踪止损", MARKET_ON_CLOSE: "收盘市价", LIMIT_ON_CLOSE: "收盘限价", NET_DEBIT: "净借", NET_CREDIT: "净贷", NET_ZERO: "净零" };
const DURATION_LABEL: Record<string, string> = { DAY: "当日", GOOD_TILL_CANCEL: "撤销前有效", FILL_OR_KILL: "全额即时", IMMEDIATE_OR_CANCEL: "即时或撤销", END_OF_WEEK: "本周", END_OF_MONTH: "本月", NEXT_END_OF_MONTH: "下月底" };

const statusTone = (group: string) => (group === "filled" ? "success" : group === "working" ? "info" : group === "canceled" ? "neutral" : "warning");
const qty = new Intl.NumberFormat("en-US", { maximumFractionDigits: 4 });

export default function OrdersPage() {
  return (
    <div className="page orders-page">
      <PageHeader
        title="订单"
        description="只读记录；下单、改单与撤单请在交易终端中操作。"
        action={
          <ButtonLink to="/investment" variant="secondary" size="sm">
            打开交易终端
          </ButtonLink>
        }
      />
      <Orders />
    </div>
  );
}

function Orders() {
  const [params, setParams] = useSearchParams();
  const range = RANGES.find((item) => item.id === params.get("range")) ?? RANGES[1];
  const status = STATUSES.find((item) => item.id === params.get("status")) ?? STATUSES[0];
  const orders = useOrders(Number(range.id), status.id);
  const update = (key: string, value: string, fallback: string) =>
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        if (value === fallback) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true },
    );
  const rows = orders.data?.items ?? [];
  return (
    <>
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <div className="rtabs" role="group" aria-label="时间范围">
          {RANGES.map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={item.id === range.id}
              className={cn("rtab sm focus-ring", item.id === range.id && "is-active")}
              onClick={() => update("range", item.id, RANGES[1].id)}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div className="chip-row" role="group" aria-label="订单状态">
          {STATUSES.map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={item.id === status.id}
              className={cn("chip focus-ring", item.id === status.id && "is-active")}
              onClick={() => update("status", item.id, "all")}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <span className="text-xs text-[var(--muted)]">
          {orders.data?.updatedAt && `更新于 ${formatDateTime(orders.data.updatedAt, { seconds: true })}`}
        </span>
        <Button variant="secondary" size="sm" disabled={orders.isFetching} onClick={() => orders.refetch()}>刷新</Button>
      </div>
      {orders.isPending ? (
        <Skeleton className="h-64" />
      ) : orders.isError ? (
        <Notice tone="danger" title="订单加载失败" action={<Button variant="secondary" onClick={() => orders.refetch()}>重试</Button>}>
          {orders.error.message}
        </Notice>
      ) : (
        <BrokerGate status={orders.data.status} message={orders.data.message} onRetry={() => orders.refetch()}>
        <Card className="data-table-card">
          {rows.length === 0 ? (
            <EmptyState title="没有符合条件的订单" description={`${range.label}内没有${status.id === "all" ? "" : `「${status.label}」的`}订单。`} />
          ) : (
            <div className="data-table-scroll" tabIndex={0} aria-label="订单表格">
              <table className="data-table">
                <thead>
                  <tr>
                    <th scope="col">时间</th>
                    <th scope="col">代码</th>
                    <th scope="col">方向</th>
                    <th scope="col">类型</th>
                    <th scope="col" className="num">数量</th>
                    <th scope="col" className="num">已成交</th>
                    <th scope="col" className="num">委托价</th>
                    <th scope="col" className="num">成交价 / 组合净价</th>
                    <th scope="col">有效期</th>
                    <th scope="col">状态</th>
                    <th scope="col">账户</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((order) => {
                    const state = order.status;
                    return (
                      <tr key={`${order.accountHash}:${order.id}`}>
                        <td className="whitespace-nowrap">{order.enteredAt ? formatDateTime(order.enteredAt, { seconds: false }) : "—"}</td>
                        <th scope="row">
                          {order.legs.map((leg, index) => (
                            <div key={index}>
                              <Link className="symbol-link focus-ring" to={`/investment?symbol=${encodeURIComponent(leg.symbol)}`}>
                                {leg.symbol || "—"}
                              </Link>
                              {order.legs.length > 1 && (
                                <span className="data-table-sub">
                                  委托 {leg.quantity === null ? "—" : qty.format(leg.quantity)}
                                  {" · 成交 "}{leg.filledQuantity === null ? "—" : qty.format(leg.filledQuantity)}
                                  {leg.averageFill !== null && ` · ${formatUSD(leg.averageFill)}`}
                                </span>
                              )}
                            </div>
                          ))}
                        </th>
                        <td>
                          {order.legs.map((leg, index) => (
                            <div key={index}>{INSTRUCTION_LABEL[leg.instruction] ?? (leg.instruction || "—")}</div>
                          ))}
                        </td>
                        <td>{TYPE_LABEL[order.orderType ?? ""] ?? order.orderType ?? "—"}</td>
                        <td className="num">{order.quantity === null ? "—" : qty.format(order.quantity)}</td>
                        <td className="num">{order.filledQuantity === null ? "—" : qty.format(order.filledQuantity)}</td>
                        <td className="num">
                          {order.price === null && order.stopPrice === null ? "—" : (
                            <>
                              {order.price !== null && <span>{formatUSD(order.price)}</span>}
                              {order.stopPrice !== null && <span className="data-table-sub">触发 {formatUSD(order.stopPrice)}</span>}
                            </>
                          )}
                        </td>
                        <td className="num">
                          {order.averageFill === null ? "—" : order.legs.length > 1
                            ? `${order.averageFill < 0 ? "净收" : "净付"} ${formatUSD(Math.abs(order.averageFill))}`
                            : formatUSD(order.averageFill)}
                        </td>
                        <td>{DURATION_LABEL[order.duration ?? ""] ?? order.duration ?? "—"}</td>
                        <td>
                          <Badge tone={statusTone(order.group)}>{STATUS_LABEL[state] ?? (state || "未知")}</Badge>
                        </td>
                        <td>{order.accountLabel}</td>
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
    </>
  );
}
