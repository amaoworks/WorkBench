import { useQuery } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { notificationsPageSchema, unreadCountSchema } from "../../shared/schema";
import { formatRelative } from "../../shared/format";
import { useOverviewSummary } from "../../shared/dashboard";

/**
 * Publishes the 通知 tile of 今日概况. This is the only place notifications appear on
 * the overview; the full list lives on /notifications.
 */
export function NotificationsOverview() {
  // Shares the navigation badge query, so the live notification stream keeps both in sync.
  const unread = useQuery({
    queryKey: ["notifications", "count"],
    queryFn: () => apiValidated("/api/notifications/unread-count", unreadCountSchema),
  });
  const latest = useQuery({
    queryKey: ["notifications", "latest"],
    queryFn: async () => notificationsPageSchema.parse(await api("/api/notifications?limit=3")),
  });
  const newest = latest.data?.items[0];
  useOverviewSummary(
    "notifications.summary",
    unread.data && newest
      ? {
          title: "通知",
          value: unread.data.count,
          unit: "条未读",
          detail: `${newest.title} · ${formatRelative(newest.createdAt)}`,
          to: unread.data.count > 0 ? "/notifications?filter=unread" : "/notifications",
        }
      : null,
  );
  return null;
}
