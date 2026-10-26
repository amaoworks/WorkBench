import { useQuery } from "@tanstack/react-query";
import { Bell, KeyRound, Plug, Target } from "lucide-react";
import { Link } from "react-router-dom";
import { apiValidated } from "../../shared/api";
import { notificationsPageSchema, type Widget } from "../../shared/schema";
import { Badge } from "../../components/ui/Badge";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { WidgetHeader } from "../../components/dashboard/WidgetHeader";
import { cn } from "../../shared/cn";
import { formatClock, formatRelative, formatSignedPct } from "../../shared/format";
import { useOverviewAlerts } from "./queries";
import type { OverviewAlerts } from "./schema";

const DAY = 86_400_000;
// Schwab refresh tokens live seven days from authorization.
const REFRESH_LIFETIME = 7 * DAY;

function SchwabHealth({ schwab, now }: { schwab: OverviewAlerts["schwab"]; now: number }) {
  if (schwab.status === "reauth")
    return (
      <div className="ov-health-box">
        <div className="ov-health-head">
          <span><KeyRound size={13} />Schwab</span>
          <Badge tone="danger">需重新授权</Badge>
        </div>
        <a className="ov-link" href="/api/modules/investment/schwab/oauth/login">去重新授权</a>
      </div>
    );
  const expires = schwab.refreshExpiresAt ? new Date(schwab.refreshExpiresAt).getTime() : NaN;
  const left = expires - now;
  const days = Math.max(0, Math.ceil(left / DAY));
  const known = !Number.isNaN(left);
  return (
    <div className="ov-health-box">
      <div className="ov-health-head">
        <span><KeyRound size={13} />Schwab</span>
        {known ? <Badge tone={left < 2 * DAY ? "warning" : "success"}>剩 {days} 天</Badge> : <Badge tone="success">已连接</Badge>}
      </div>
      {known ? (
        <>
          <div className="ov-meter" aria-hidden="true">
            <i className={left < 2 * DAY ? "is-warn" : ""} style={{ width: `${Math.min(100, Math.max(4, (left / REFRESH_LIFETIME) * 100))}%` }} />
          </div>
          <p className="ov-note">
            刷新令牌 {formatRelativeDate(expires)} 到期{left < 2 * DAY && <> · <a className="ov-link" href="/api/modules/investment/schwab/oauth/login">提前重新授权</a></>}
          </p>
        </>
      ) : (
        <p className="ov-note">授权到期时间未知，重新授权一次后显示倒计时。</p>
      )}
      {schwab.lastError && <p className="ov-note ov-down">{schwab.lastError}</p>}
    </div>
  );
}

const formatRelativeDate = (value: number) => {
  const date = new Date(value);
  return `${date.getMonth() + 1}/${date.getDate()} ${formatClock(date)}`;
};

function FutuHealth({ futu }: { futu: OverviewAlerts["futu"] }) {
  const online = futu.connected && futu.qotLogined;
  return (
    <div className="ov-health-box">
      <div className="ov-health-head">
        <span><Plug size={13} />富途 OpenD</span>
        <Badge tone={online ? "success" : futu.connected ? "warning" : "danger"}>{online ? "在线" : futu.connected ? "行情未登录" : "离线"}</Badge>
      </div>
      <p className="ov-note">{futu.lastError || (online ? "行情已登录" : futu.serviceState ? `服务状态：${futu.serviceState}` : "未连接到 OpenD")}</p>
    </div>
  );
}

export default function InvestmentAlertsWidget({ widget }: { widget: Widget }) {
  const alerts = useOverviewAlerts(widget);
  const notifications = useQuery({
    queryKey: ["notifications", "unread", 5],
    queryFn: () => apiValidated("/api/notifications?unread=true&limit=5", notificationsPageSchema),
    refetchInterval: 60_000,
  });
  if (alerts.isPending) return <Skeleton className="h-32" />;
  if (alerts.isError)
    return (
      <Notice tone="danger" title="提醒加载失败">
        {alerts.error.message}
      </Notice>
    );
  const { schwab, futu, triggers } = alerts.data;
  const unread = notifications.data?.items ?? [];
  const showSchwab = schwab.status === "connected" || schwab.status === "reauth";
  if (triggers.length === 0 && unread.length === 0 && !showSchwab && !futu.enabled) return null;
  return (
    <>
      <WidgetHeader>{unread.length > 0 && <Badge>{unread.length >= 5 ? "5+" : unread.length} 条未读</Badge>}</WidgetHeader>
      {triggers.length > 0 || unread.length > 0 ? (
        <ul className="ov-list" aria-label="提醒">
          {triggers.map((trigger) => (
            <li key={trigger.id} className="ov-item">
              <span className={cn("ov-icon", trigger.direction === "up" ? "ov-icon-up" : "ov-icon-down")}>
                <Target size={15} />
              </span>
              <div className="ov-item-text">
                <Link to={`/investment?tab=monitor`} className="ov-item-title">
                  {trigger.symbol} {trigger.direction === "up" ? "涨幅" : "跌幅"}超过 {trigger.thresholdPercent}% · {trigger.price.toFixed(2)} ({formatSignedPct(trigger.changePercent)})
                </Link>
                <p className="ov-note">价格监控 · {formatRelative(trigger.triggeredAt)}</p>
              </div>
            </li>
          ))}
          {unread.map((item) => (
            <li key={item.id} className="ov-item">
              <span className="ov-icon">
                <Bell size={15} />
              </span>
              <div className="ov-item-text">
                <Link to={item.actionRoute || "/notifications"} className="ov-item-title">
                  {item.title}
                </Link>
                <p className="ov-note">通知 · {formatRelative(item.createdAt)}</p>
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="ov-empty-line">暂无新提醒。</p>
      )}
      {(showSchwab || futu.enabled) && (
        <>
          <p className="ov-section">连接健康</p>
          <div className="ov-health">
            {showSchwab && <SchwabHealth schwab={schwab} now={alerts.dataUpdatedAt} />}
            {futu.enabled && <FutuHealth futu={futu} />}
          </div>
        </>
      )}
    </>
  );
}
