import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Bell, CheckCheck } from "lucide-react";
import { api } from "../../shared/api";
import { notificationsPageSchema, type Notification } from "../../shared/schema";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { toast } from "sonner";

export default function NotificationsPage() {
  const queryClient = useQueryClient();
  const notifications = useQuery({ queryKey: ["notifications", "list"], queryFn: async () => notificationsPageSchema.parse(await api("/api/notifications?limit=100")) });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["notifications"] });
  const onError = (error: Error) => toast.error(error.message);
  const markAll = useMutation({ mutationFn: () => api("/api/notifications/read-all", { method: "PUT", body: "{}" }), onSuccess: refresh, onError });
  const markRead = useMutation({ mutationFn: (id: string) => api("/api/notifications/read", { method: "PUT", body: JSON.stringify({ ids: [id] }) }), onSuccess: refresh, onError });
  const archive = useMutation({ mutationFn: (id: string) => api("/api/notifications/archive", { method: "PUT", body: JSON.stringify({ ids: [id] }) }), onSuccess: refresh, onError });
  return <div className="page notifications-page">
    <PageHeader title="通知" />
    <Card><CardHeader title="最近通知" action={<Button variant="secondary" size="sm" disabled={markAll.isPending || !notifications.data?.items.some((item) => !item.readAt)} onClick={() => markAll.mutate()}><CheckCheck size={15} />全部已读</Button>} />
      {notifications.isLoading && <div className="space-y-3 p-4"><Skeleton className="h-20" /><Skeleton className="h-20" /></div>}
      {notifications.isError && <p role="alert" className="p-5 text-danger">通知加载失败：{notifications.error.message}</p>}
      {notifications.data?.items.length === 0 && <EmptyState title="暂无通知" description="待办到期或工作空间有重要变化时，会在这里提醒你。" />}
      <div className="divide-y divide-[var(--border)]">{notifications.data?.items.map((item) => <NotificationRow key={item.id} item={item} onRead={() => markRead.mutate(item.id)} onArchive={() => archive.mutate(item.id)} />)}</div>
    </Card>
  </div>;
}

function NotificationRow({ item, onRead, onArchive }: { item: Notification; onRead: () => void; onArchive: () => void }) {
  return <div className={cn("notification-row flex gap-3 px-5 py-4", !item.readAt && "notification-unread")}>
    <div className={cn("icon-tile mt-0.5", severityClass[item.severity])}><Bell size={17} /></div>
    <div className="min-w-0 flex-1"><div className="notification-heading"><div><p className="font-medium break-words">{item.title}</p><p className="mt-1 text-sm text-[var(--muted)] break-words">{item.content}</p></div><time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time></div>
      <div className="notification-actions mt-3 flex flex-wrap gap-2">{item.actionRoute && <ButtonLink to={item.actionRoute} onClick={onRead} variant="ghost" size="sm">{item.actionLabel ?? "查看"}</ButtonLink>}{!item.readAt && <Button onClick={onRead} variant="ghost" size="sm">标为已读</Button>}<Button onClick={onArchive} variant="ghost" size="sm"><Archive size={13} />归档</Button></div>
    </div>
  </div>;
}

const severityClass = { info: "status-info", success: "status-success", warning: "status-warning", error: "status-error" };
