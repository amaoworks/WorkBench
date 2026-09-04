import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Bell, CheckCheck } from "lucide-react";
import { Link } from "react-router-dom";
import { api } from "../../shared/api";
import { notificationsPageSchema, type Notification } from "../../shared/schema";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";

export default function NotificationsPage() {
  const queryClient = useQueryClient();
  const notifications = useQuery({ queryKey: ["notifications", "list"], queryFn: async () => notificationsPageSchema.parse(await api<unknown>("/api/notifications?limit=100")) });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["notifications"] });
  const markAll = useMutation({ mutationFn: () => api("/api/notifications/read-all", { method: "PUT", body: "{}" }), onSuccess: refresh });
  const markRead = useMutation({ mutationFn: (id: string) => api("/api/notifications/read", { method: "PUT", body: JSON.stringify({ ids: [id] }) }), onSuccess: refresh });
  const archive = useMutation({ mutationFn: (id: string) => api("/api/notifications/archive", { method: "PUT", body: JSON.stringify({ ids: [id] }) }), onSuccess: refresh });
  return <div className="mx-auto max-w-4xl"><div className="mb-6"><h1 className="text-2xl font-semibold tracking-tight">通知中心</h1><p className="mt-1 text-[var(--muted)]">所有提醒持久保存在本地工作空间中。</p></div><Card><CardHeader title="最近通知" action={<Button variant="secondary" size="sm" onClick={() => markAll.mutate()}><CheckCheck size={15} />全部已读</Button>} />{notifications.isLoading && <div className="space-y-3 p-4"><Skeleton className="h-20" /><Skeleton className="h-20" /></div>}{notifications.data?.items.length === 0 && <EmptyState title="没有通知" description="待办到期或模块产生重要变化时会出现在这里。" />}<div className="divide-y divide-[var(--border)]">{notifications.data?.items.map((item) => <NotificationRow key={item.id} item={item} onRead={() => markRead.mutate(item.id)} onArchive={() => archive.mutate(item.id)} />)}</div></Card></div>;
}

function NotificationRow({ item, onRead, onArchive }: { item: Notification; onRead: () => void; onArchive: () => void }) {
  return <div className={cn("flex gap-3 px-5 py-4", !item.readAt && "bg-indigo-50/50 dark:bg-indigo-500/5")}><div className={cn("mt-0.5 grid size-9 shrink-0 place-items-center rounded-lg", severityClass[item.severity])}><Bell size={17} /></div><div className="min-w-0 flex-1"><div className="flex items-start justify-between gap-3"><div><p className="font-medium">{item.title}</p><p className="mt-1 text-sm text-[var(--muted)]">{item.content}</p></div><span className="shrink-0 text-xs text-[var(--muted)]">{new Date(item.createdAt).toLocaleString()}</span></div><div className="mt-3 flex gap-2">{item.actionRoute && <Link to={item.actionRoute} onClick={onRead} className="text-xs font-medium text-indigo-500 hover:underline">{item.actionLabel ?? "查看"}</Link>}{!item.readAt && <button onClick={onRead} className="text-xs text-[var(--muted)] hover:text-indigo-500">标为已读</button>}<button onClick={onArchive} className="flex items-center gap-1 text-xs text-[var(--muted)] hover:text-red-500"><Archive size={12} />归档</button></div></div></div>;
}

const severityClass = { info: "bg-blue-50 text-blue-500 dark:bg-blue-500/10", success: "bg-emerald-50 text-emerald-500 dark:bg-emerald-500/10", warning: "bg-amber-50 text-amber-500 dark:bg-amber-500/10", error: "bg-red-50 text-red-500 dark:bg-red-500/10" };

