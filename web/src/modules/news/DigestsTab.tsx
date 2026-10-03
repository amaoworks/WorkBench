import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { ExternalLink, Send } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { createDigest, pushDigest, useDigests, useRefreshNews } from "./queries";
import type { Digest } from "./schema";

export default function DigestsTab() {
  const digests = useDigests();
  const refresh = useRefreshNews();
  const [topic, setTopic] = useState("");
  const [periodHours, setPeriodHours] = useState(24);
  const [maxItems, setMaxItems] = useState(20);
  const [push, setPush] = useState(false);
  const create = useMutation({
    mutationFn: () => createDigest({ topic: topic.trim(), periodHours, maxItems, push }),
    onSuccess: (digest) => { void refresh(); toast.success(digest.notificationId ? "简报已生成，推送已提交" : "简报已生成"); },
    onError: (error) => toast.error(error.message)
  });
  const items = digests.data?.pages.flatMap((page) => page.items) ?? [];
  function submit(event: FormEvent) { event.preventDefault(); create.mutate(); }

  return <div className="space-y-4">
    <Card className="p-4">
      <form onSubmit={submit} className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(10rem,1fr)_9rem_9rem_auto]">
        <label className="block text-sm">主题<input value={topic} onChange={(event) => setTopic(event.target.value)} placeholder="全部主题" className="ui-input mt-1 w-full" /></label>
        <label className="block text-sm">时间范围<select value={periodHours} onChange={(event) => setPeriodHours(Number(event.target.value))} className="ui-input mt-1 w-full"><option value={6}>最近 6 小时</option><option value={12}>最近 12 小时</option><option value={24}>最近 24 小时</option><option value={72}>最近 3 天</option><option value={168}>最近 7 天</option></select></label>
        <label className="block text-sm">最多条数<input type="number" min={1} max={30} required value={maxItems} onChange={(event) => setMaxItems(event.target.valueAsNumber)} className="ui-input mt-1 w-full" /></label>
        <div className="flex flex-wrap items-end gap-3"><label className="mb-2 flex items-center gap-2 text-sm"><input type="checkbox" checked={push} onChange={(event) => setPush(event.target.checked)} />提交推送</label><Button disabled={create.isPending}>{create.isPending ? "生成中…" : "生成简报"}</Button></div>
      </form>
    </Card>
    <Card>
      <CardHeader title="历史简报" description={digests.data ? `已加载 ${items.length} 份` : "正在读取"} />
      {digests.isPending && <div className="space-y-3 p-4"><Skeleton className="h-32" /><Skeleton className="h-32" /></div>}
      {digests.isError && <p role="alert" className="p-5 text-danger">{digests.error.message}</p>}
      {digests.data && items.length === 0 && <EmptyState title="暂无简报" description="选择范围后生成第一份简报。" />}
      <div className="divide-y divide-[var(--border)]">{items.map((digest) => <DigestRow key={digest.id} digest={digest} />)}</div>
      {digests.hasNextPage && <div className="p-4"><Button variant="secondary" disabled={digests.isFetchingNextPage} onClick={() => digests.fetchNextPage()}>{digests.isFetchingNextPage ? "加载中…" : "加载更多"}</Button></div>}
    </Card>
  </div>;
}

function DigestRow({ digest }: { digest: Digest }) {
  const refresh = useRefreshNews();
  const push = useMutation({ mutationFn: () => pushDigest(digest.id), onSuccess: () => { void refresh(); toast.success("推送已提交"); }, onError: (error) => toast.error(error.message) });
  return <article className="min-w-0 space-y-3 p-4">
    <div className="flex flex-col gap-2 sm:flex-row sm:items-start">
      <div className="min-w-0 flex-1"><h3 className="break-words font-medium">{digest.title}</h3><p className="mt-1 text-xs text-[var(--muted)]">{digest.topic || "全部主题"} · {new Date(digest.createdAt).toLocaleString()} · {digest.aiUsed ? "AI 简报" : "普通简报"}</p></div>
      <div className="flex items-center gap-2">{digest.notificationId && <span className="text-xs text-[var(--muted)]">推送已提交</span>}<Button size="sm" variant="secondary" disabled={push.isPending} onClick={() => push.mutate()}><Send size={14} />{push.isPending ? "提交中…" : "发送推送"}</Button></div>
    </div>
    <p className="whitespace-pre-wrap break-words text-sm">{digest.content}</p>
    {digest.items.length > 0 && <ul className="space-y-1 text-sm">{digest.items.map((item) => { const href = safeExternalURL(item.url); return <li key={item.id} className="break-words"><span className="text-[var(--muted)]">{item.sourceName} · </span>{href ? <a className="text-[var(--brand)] hover:underline" href={href} target="_blank" rel="noopener noreferrer">{item.title}<ExternalLink className="ml-1 inline" size={13} /></a> : item.title}</li>; })}</ul>}
  </article>;
}

function safeExternalURL(value: string) {
  try { const url = new URL(value); return url.protocol === "http:" || url.protocol === "https:" ? url.href : null; } catch { return null; }
}
