import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Archive, Bell, CheckCheck } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../../shared/api";
import { notificationsPageSchema, type Notification } from "../../shared/schema";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { toast } from "sonner";
import { formatDateTime } from "../../shared/format";
import { useModules } from "../../app/queries";
import { notificationDayGroup, useLocalNow } from "./notificationTime";

const PLATFORM_SOURCES: Record<string, string> = { core: "平台", platform: "平台", system: "系统" };

export default function NotificationsPage() {
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const unreadOnly = params.get("filter") === "unread";
  const source = params.get("source");
  const modules = useModules();
  const notifications = useInfiniteQuery({
    queryKey: ["notifications", "list", unreadOnly ? "unread" : "all", source ?? "all-sources"],
    queryFn: async ({ pageParam }) => {
      const query = new URLSearchParams({ limit: "50" });
      if (unreadOnly) query.set("unread", "true");
      if (source) query.set("source", source);
      if (pageParam) query.set("cursor", pageParam);
      return notificationsPageSchema.parse(await api(`/api/notifications?${query}`));
    },
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.nextCursor,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["notifications"] });
  const onError = (error: Error) => toast.error(error.message);
  const markAll = useMutation({
    mutationFn: () => api("/api/notifications/read-all", { method: "PUT", body: "{}" }),
    onSuccess: refresh,
    onError,
  });
  const markRead = useMutation({
    mutationFn: (id: string) => api("/api/notifications/read", { method: "PUT", body: JSON.stringify({ ids: [id] }) }),
    onSuccess: refresh,
    onError,
  });
  const archive = useMutation({
    mutationFn: (id: string) => api("/api/notifications/archive", { method: "PUT", body: JSON.stringify({ ids: [id] }) }),
    onSuccess: refresh,
    onError,
  });
  const sourceName = (id: string) => modules.data?.items.find((item) => item.id === id)?.name ?? PLATFORM_SOURCES[id] ?? id;
  const items = notifications.data?.pages.flatMap((page) => page.items) ?? [];
  const sources = [
    ...new Set([
      ...(modules.data?.items.map((item) => item.id) ?? []),
      ...items.map((item) => item.sourceModule),
      ...(source ? [source] : []),
    ]),
  ];
  const now = useLocalNow();
  const groups = ["今天", "昨天", "更早"]
    .map((label) => ({ label, items: items.filter((item) => notificationDayGroup(item.createdAt, now) === label) }))
    .filter((group) => group.items.length > 0);
  const nextParams = (next: { filter?: string | null; source?: string | null }) => {
    const value = new URLSearchParams(params);
    for (const [key, entry] of Object.entries(next)) {
      if (entry) value.set(key, entry);
      else value.delete(key);
    }
    return value;
  };
  const link = (next: { filter?: string | null }) => {
    const query = nextParams(next).toString();
    return query ? `/notifications?${query}` : "/notifications";
  };
  return (
    <div className="page notifications-page">
      <PageHeader
        title="通知"
        action={
          <Button
            variant="secondary"
            size="sm"
            disabled={markAll.isPending || !items.some((item) => !item.readAt)}
            onClick={() => markAll.mutate()}
          >
            <CheckCheck size={15} />
            全部已读
          </Button>
        }
      />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <nav className="pill-tabs" aria-label="通知筛选">
          <Link to={link({ filter: null })} className={cn("pill-tab focus-ring", !unreadOnly && "is-active")} aria-current={!unreadOnly ? "page" : undefined}>
            全部
          </Link>
          <Link to={link({ filter: "unread" })} className={cn("pill-tab focus-ring", unreadOnly && "is-active")} aria-current={unreadOnly ? "page" : undefined}>
            未读
          </Link>
        </nav>
        {(source || sources.length > 1) && (
          <div className="chip-row" role="group" aria-label="来源">
            <button
              type="button" className={cn("chip focus-ring", !source && "is-active")} aria-pressed={!source}
              onClick={() => setParams(nextParams({ source: null }), { replace: true })}
            >
              全部来源
            </button>
            {sources.map((id) => (
              <button
                key={id}
                type="button"
                className={cn("chip focus-ring", source === id && "is-active")}
                aria-pressed={source === id}
                onClick={() => setParams(nextParams({ source: source === id ? null : id }), { replace: true })}
              >
                {sourceName(id)}
              </button>
            ))}
          </div>
        )}
      </div>
      {notifications.isLoading && (
        <Card className="space-y-3 p-4">
          <Skeleton className="h-20" />
          <Skeleton className="h-20" />
        </Card>
      )}
      {notifications.isError && (
        <Notice tone="danger" title="通知加载失败">
          {notifications.error.message}
        </Notice>
      )}
      {notifications.data && items.length === 0 && (
        <Card>
          <EmptyState
            title={source ? (unreadOnly ? "此来源没有未读通知" : "此来源暂无通知") : unreadOnly ? "没有未读通知" : "暂无通知"}
            description={
              source ? undefined : unreadOnly ? "所有通知都已读过。" : "待办到期或工作空间有重要变化时，会在这里提醒你。"
            }
            action={
              source ? (
                <Button variant="secondary" size="sm" onClick={() => setParams(nextParams({ source: null }), { replace: true })}>
                  清除来源
                </Button>
              ) : undefined
            }
          />
        </Card>
      )}
      <div className="notification-groups">
        {groups.map((group) => (
          <section key={group.label} aria-label={group.label}>
            <h2 className="todo-group-title">
              {group.label}
              <span>{group.items.length}</span>
            </h2>
            <Card className="divide-y divide-[var(--border)]">
              {group.items.map((item) => (
                <NotificationRow
                  key={item.id}
                  item={item}
                  source={sourceName(item.sourceModule)}
                  onRead={() => markRead.mutate(item.id)}
                  onArchive={() => archive.mutate(item.id)}
                />
              ))}
            </Card>
          </section>
        ))}
      </div>
      {notifications.hasNextPage && (
        <div className="mt-5 flex justify-center">
          <Button variant="secondary" disabled={notifications.isFetchingNextPage} onClick={() => notifications.fetchNextPage()}>
            {notifications.isFetchingNextPage ? "加载中…" : "加载更多"}
          </Button>
        </div>
      )}
    </div>
  );
}

function NotificationRow({ item, source, onRead, onArchive }: { item: Notification; source: string; onRead: () => void; onArchive: () => void }) {
  return (
    <div className={cn("notification-row flex gap-3 px-5 py-4", !item.readAt && "notification-unread")}>
      <div className={cn("icon-tile mt-0.5", severityClass[item.severity])}>
        <Bell size={17} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="notification-heading">
          <div>
            <p className="font-medium break-words">
              {!item.readAt && <span className="unread-dot" aria-label="未读" />}
              {item.title}
            </p>
            <p className="mt-1 text-sm text-[var(--muted)] break-words">{item.content}</p>
          </div>
          <span className="notification-meta">
            <span className="notification-source">{source}</span>
            <time dateTime={item.createdAt}>{formatDateTime(item.createdAt, { seconds: false })}</time>
          </span>
        </div>
        <div className="notification-actions mt-3 flex flex-wrap gap-2">
          {item.actionRoute && (
            <ButtonLink to={item.actionRoute} onClick={onRead} variant="ghost" size="sm">
              {item.actionLabel ?? "查看"}
            </ButtonLink>
          )}
          {!item.readAt && (
            <Button onClick={onRead} variant="ghost" size="sm">
              标为已读
            </Button>
          )}
          <Button onClick={onArchive} variant="ghost" size="sm">
            <Archive size={13} />
            归档
          </Button>
        </div>
      </div>
    </div>
  );
}

const severityClass = { info: "status-info", success: "status-success", warning: "status-warning", error: "status-error" };
