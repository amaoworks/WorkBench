import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState, Skeleton } from "../../components/ui/States";
import { deleteSource, refreshAllSources, refreshSource, saveSource, useRefreshNews, useSources } from "./queries";
import type { Source, SourceInput } from "./schema";

const blank: SourceInput = { name: "", kind: "rss", url: "", query: "", topic: "", enabled: true, intervalMinutes: 60 };

export default function SourcesTab() {
  const sources = useSources();
  const refresh = useRefreshNews();
  const [editing, setEditing] = useState<Source | "new" | null>(null);
  const [deleting, setDeleting] = useState<Source | null>(null);
  const deleteTrigger = useRef<HTMLElement | null>(null);
  const save = useMutation({
    mutationFn: saveSource,
    onSuccess: () => { setEditing(null); void refresh(); toast.success("订阅源已保存"); },
    onError: (error) => toast.error(error.message)
  });
  const remove = useMutation({
    mutationFn: deleteSource,
    onSuccess: () => { setDeleting(null); void refresh(); toast.success("订阅源已删除"); },
    onError: (error) => toast.error(error.message)
  });
  const singleRefresh = useMutation({
    mutationFn: refreshSource,
    onSuccess: (result) => { void refresh(); toast.success(`刷新完成，新增 ${result.added} 条`); },
    onError: (error) => { void refresh(); toast.error(error.message); }
  });
  const allRefresh = useMutation({
    mutationFn: refreshAllSources,
    onSuccess: (result) => { void refresh(); toast.success(`刷新完成，新增 ${result.added} 条，失败 ${result.failed} 个`); },
    onError: (error) => toast.error(error.message)
  });
  const items = sources.data?.items ?? [];

  return <div className="space-y-4">
    <Card>
      <CardHeader title="订阅源" description={sources.data ? `${items.length} 个来源` : "正在读取"} action={<div className="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" disabled={allRefresh.isPending} onClick={() => allRefresh.mutate()}><RefreshCw size={15} />{allRefresh.isPending ? "刷新中…" : "全部刷新"}</Button>
        <Button size="sm" onClick={() => setEditing("new")}><Plus size={15} />添加来源</Button>
      </div>} />
      {editing && <SourceForm key={editing === "new" ? "new" : editing.id} initial={editing === "new" ? blank : editing} id={editing === "new" ? undefined : editing.id} busy={save.isPending} onCancel={() => setEditing(null)} onSave={(input) => save.mutate(input)} />}
      {sources.isPending && <div className="space-y-3 p-4"><Skeleton className="h-20" /><Skeleton className="h-20" /></div>}
      {sources.isError && <p role="alert" className="p-5 text-danger">{sources.error.message}</p>}
      {sources.data && items.length === 0 && !editing && <EmptyState title="暂无订阅源" description="添加 RSS 或新闻搜索来源。" />}
      <div className="divide-y divide-[var(--border)]">
        {items.map((source) => <div key={source.id} className="flex min-w-0 flex-col gap-3 p-4 sm:flex-row sm:items-center">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2"><h3 className="font-medium">{source.name}</h3><span className="rounded bg-[var(--soft)] px-2 py-0.5 text-xs uppercase">{source.kind}</span>{!source.enabled && <span className="text-xs text-[var(--muted)]">已停用</span>}</div>
            <p className="mt-1 break-all text-xs text-[var(--muted)]">{source.topic || "未分类"} · {source.itemCount} 条 · 每 {source.intervalMinutes} 分钟</p>
            {(source.query || source.url) && <p className="mt-1 truncate text-xs text-[var(--muted)]">{source.query || source.url}</p>}
            {source.lastFetchedAt && <p className="mt-1 text-xs text-[var(--muted)]">最近刷新：{new Date(source.lastFetchedAt).toLocaleString()}</p>}
            {source.lastError && <p role="alert" className="mt-1 break-words text-xs text-danger">{source.lastError}</p>}
          </div>
          <div className="flex flex-wrap gap-1">
            <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => save.mutate({ ...pickInput(source), enabled: !source.enabled, id: source.id })}>{source.enabled ? "停用" : "启用"}</Button>
            <Button size="icon" variant="ghost" aria-label={`刷新 ${source.name}`} disabled={singleRefresh.isPending} onClick={() => singleRefresh.mutate(source.id)}><RefreshCw size={16} /></Button>
            <Button size="icon" variant="ghost" aria-label={`编辑 ${source.name}`} onClick={() => setEditing(source)}><Pencil size={16} /></Button>
            <Button size="icon" variant="ghost" aria-label={`删除 ${source.name}`} onClick={() => { deleteTrigger.current = document.activeElement as HTMLElement; setDeleting(source); }}><Trash2 size={16} /></Button>
          </div>
        </div>)}
      </div>
    </Card>
    <ConfirmDialog open={deleting !== null} title="删除订阅源" description={`确定删除“${deleting?.name ?? ""}”吗？`} busy={remove.isPending} onCancel={() => setDeleting(null)} onConfirm={() => { if (deleting) remove.mutate(deleting.id); }} onClosed={() => deleteTrigger.current?.focus()} />
  </div>;
}

function SourceForm({ initial, id, busy, onCancel, onSave }: { initial: SourceInput; id?: string; busy: boolean; onCancel: () => void; onSave: (input: SourceInput & { id?: string }) => void }) {
  const [form, setForm] = useState(initial);
  const change = (patch: Partial<SourceInput>) => setForm((current) => ({ ...current, ...patch }));
  const needsURL = form.kind === "rss" || form.kind === "rsshub";
  const needsQuery = form.kind === "google";
  function submit(event: FormEvent) { event.preventDefault(); onSave({ ...form, name: form.name.trim(), url: needsURL ? form.url.trim() : "", query: needsQuery ? form.query.trim() : "", topic: form.topic.trim(), id }); }
  return <form onSubmit={submit} className="m-4 grid grid-cols-1 gap-3 rounded-lg border border-[var(--border)] p-4 sm:grid-cols-2">
    <label className="block text-sm">名称<input autoFocus required value={form.name} onChange={(event) => change({ name: event.target.value })} placeholder="来源名称" className="ui-input mt-1 w-full" /></label>
    <label className="block text-sm">类型<select value={form.kind} onChange={(event) => change({ kind: event.target.value as SourceInput["kind"] })} className="ui-input mt-1 w-full"><option value="rss">RSS</option><option value="rsshub">RSSHub</option><option value="google">Google 新闻</option><option value="yahoo">Yahoo 财经</option></select></label>
    {needsURL && <label className="block text-sm sm:col-span-2">订阅地址<input type="url" required value={form.url} onChange={(event) => change({ url: event.target.value })} placeholder="https://example.com/feed.xml" className="ui-input mt-1 w-full" /></label>}
    {needsQuery && <label className="block text-sm sm:col-span-2">查询词<input required value={form.query} onChange={(event) => change({ query: event.target.value })} placeholder="新闻关键词" className="ui-input mt-1 w-full" /></label>}
    <label className="block text-sm">主题<input value={form.topic} onChange={(event) => change({ topic: event.target.value })} placeholder="科技" className="ui-input mt-1 w-full" /></label>
    <label className="block text-sm">刷新间隔（分钟）<input type="number" min={5} max={10080} required value={form.intervalMinutes} onChange={(event) => change({ intervalMinutes: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.enabled} onChange={(event) => change({ enabled: event.target.checked })} />启用</label>
    <div className="flex justify-end gap-2 sm:col-span-2"><Button type="button" variant="secondary" onClick={onCancel}>取消</Button><Button disabled={busy}>{busy ? "保存中…" : "保存"}</Button></div>
  </form>;
}

function pickInput(source: Source): SourceInput {
  return { name: source.name, kind: source.kind, url: source.url, query: source.query, topic: source.topic, enabled: source.enabled, intervalMinutes: source.intervalMinutes };
}
