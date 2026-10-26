import { useQuery } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { notificationsPageSchema, unreadCountSchema, type Widget } from "../../shared/schema";
import { ButtonLink } from "../../components/ui/Button";
import { StatCard } from "../../components/ui/Card";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { formatDateTime } from "../../shared/format";

/** Overview card for platform notifications: unread count plus the latest few items. */
export function NotificationsSummary({ widget }: { widget: Widget }) {
  // Shares the sidebar badge query, so the live notification stream keeps both in sync.
  const unread = useQuery({
    queryKey: ["notifications", "count"],
    queryFn: () => apiValidated("/api/notifications/unread-count", unreadCountSchema),
  });
  const latest = useQuery({
    queryKey: ["notifications", "latest"],
    queryFn: async () => notificationsPageSchema.parse(await api("/api/notifications?limit=3")),
  });
  if (unread.isPending || latest.isPending) return <Skeleton className="h-32" />;
  if (unread.isError || latest.isError)
    return (
      <Notice tone="danger" title={`${widget.title}加载失败`}>
        {(unread.error ?? latest.error)?.message}
      </Notice>
    );
  const items = latest.data.items;
  // Empty overview cards hide themselves.
  if (items.length === 0) return null;
  return (
    <div className="space-y-4">
      <div className="dashboard-stats">
        <StatCard label="未读" value={unread.data.count} accent={unread.data.count > 0 ? "warning" : "success"} />
      </div>
      <ul className="space-y-2" aria-label="最新通知">
        {items.map((item) => (
          <li key={item.id} className="min-w-0">
            <p className={cn("truncate text-sm", !item.readAt && "font-medium")}>{item.title}</p>
            <p className="truncate text-xs text-[var(--muted)]">{formatDateTime(item.createdAt, { seconds: false })}</p>
          </li>
        ))}
      </ul>
      <ButtonLink size="sm" variant="secondary" to="/notifications">
        查看通知
      </ButtonLink>
    </div>
  );
}
