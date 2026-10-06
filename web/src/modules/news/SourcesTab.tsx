import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState } from "../../components/ui/EmptyState";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { deleteSource, refreshAllSources, refreshSource, saveSource, useRefreshNews, useSources } from "./queries";
import type { Source, SourceInput } from "./schema";

const blank: SourceInput = { name: "", kind: "rss", url: "", query: "", topic: "", enabled: true, intervalMinutes: 60 };

export default function SourcesTab() {
  const sources = useSources();
  const refresh = useRefreshNews();
  const [editing, setEditing] = useState<Source | "new" | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const save = useMutation({
    mutationFn: saveSource,
    onSuccess: async () => {
      setEditing(null);
      await refresh();
      toast.success("订阅源已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  const remove = useMutation({
    mutationFn: deleteSource,
    onSuccess: async () => {
      await refresh();
      toast.success("订阅源已删除");
    },
    onError: (error) => toast.error(error.message),
  });
  const deletion = useConfirmDelete<Source>({
    title: "删除订阅源",
    description: (target) => `确定删除“${target.name}”吗？`,
    onConfirm: (target) => remove.mutateAsync(target.id),
    fallbackFocus: () => addButton.current,
  });
  const singleRefresh = useMutation({
    mutationFn: refreshSource,
    onSuccess: async (result) => {
      await refresh();
      toast.success(`刷新完成，新增 ${result.added} 条`);
    },
    onError: async (error) => {
      await refresh();
      toast.error(error.message);
    },
  });
  const allRefresh = useMutation({
    mutationFn: refreshAllSources,
    onSuccess: async (result) => {
      await refresh();
      toast.success(`刷新完成，新增 ${result.added} 条，失败 ${result.failed} 个`);
    },
    onError: (error) => toast.error(error.message),
  });
  const items = sources.data?.items ?? [];

  return (
    <Card>
      <CardHeader
        title="订阅源"
        description={sources.data ? `${items.length} 个订阅源` : "正在读取"}
        action={
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="secondary" disabled={allRefresh.isPending} onClick={() => allRefresh.mutate()}>
              <RefreshCw size={15} />
              {allRefresh.isPending ? "刷新中…" : "全部刷新"}
            </Button>
            <Button ref={addButton} size="sm" onClick={() => setEditing("new")}>
              <Plus size={15} />
              添加订阅源
            </Button>
          </div>
        }
      />
      {editing && (
        <SourceForm
          key={editing === "new" ? "new" : editing.id}
          initial={editing === "new" ? blank : editing}
          id={editing === "new" ? undefined : editing.id}
          busy={save.isPending}
          onCancel={() => setEditing(null)}
          onSave={(input) => save.mutate(input)}
        />
      )}
      {sources.isPending && (
        <div className="space-y-3 p-4">
          <Skeleton className="h-20" />
          <Skeleton className="h-20" />
        </div>
      )}
      {sources.isError && (
        <Notice className="m-4" tone="danger" title="订阅源读取失败">
          {sources.error.message}
        </Notice>
      )}
      {sources.data && items.length === 0 && !editing && <EmptyState title="暂无订阅源" description="添加 RSS 或新闻搜索订阅源。" />}
      <div className="divide-y divide-[var(--border)]">
        {items.map((source) => (
          <div key={source.id} className="flex min-w-0 flex-col gap-3 p-4 sm:flex-row sm:items-center">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-medium">{source.name}</h3>
                <Badge>{source.kind.toUpperCase()}</Badge>
                {!source.enabled && <Badge>已停用</Badge>}
              </div>
              <p className="mt-1 break-all text-xs text-[var(--muted)]">
                {source.topic || "未分类"} · {source.itemCount} 条 · 每 {source.intervalMinutes} 分钟
              </p>
              {(source.query || source.url) && <p className="mt-1 truncate text-xs text-[var(--muted)]">{source.query || source.url}</p>}
              {source.lastFetchedAt && (
                <p className="mt-1 text-xs text-[var(--muted)]">最近刷新：{new Date(source.lastFetchedAt).toLocaleString()}</p>
              )}
              {source.lastError && (
                <Notice className="mt-2" tone="danger" title="订阅源刷新失败">
                  {source.lastError}
                </Notice>
              )}
            </div>
            <div className="flex flex-wrap gap-1">
              <Button
                size="sm"
                variant="ghost"
                disabled={save.isPending}
                onClick={() => save.mutate({ ...pickInput(source), enabled: !source.enabled, id: source.id })}
              >
                {source.enabled ? "停用" : "启用"}
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={`刷新 ${source.name}`}
                disabled={singleRefresh.isPending}
                onClick={() => singleRefresh.mutate(source.id)}
              >
                <RefreshCw size={16} />
              </Button>
              <Button size="icon" variant="ghost" aria-label={`编辑 ${source.name}`} onClick={() => setEditing(source)}>
                <Pencil size={16} />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={`删除 ${source.name}`}
                onClick={(event) => deletion.request(source, event.currentTarget)}
              >
                <Trash2 size={16} />
              </Button>
            </div>
          </div>
        ))}
      </div>
      <ConfirmDialog {...deletion.dialogProps} />
    </Card>
  );
}

function SourceForm({
  initial,
  id,
  busy,
  onCancel,
  onSave,
}: {
  initial: SourceInput;
  id?: string;
  busy: boolean;
  onCancel: () => void;
  onSave: (input: SourceInput & { id?: string }) => void;
}) {
  const [form, setForm] = useState(initial);
  const change = (patch: Partial<SourceInput>) => setForm((current) => ({ ...current, ...patch }));
  const needsURL = form.kind === "rss" || form.kind === "rsshub";
  const needsQuery = form.kind === "google";
  function submit(event: FormEvent) {
    event.preventDefault();
    onSave({
      ...form,
      name: form.name.trim(),
      url: needsURL ? form.url.trim() : "",
      query: needsQuery ? form.query.trim() : "",
      topic: form.topic.trim(),
      id,
    });
  }
  return (
    <form onSubmit={submit} className="m-4 grid grid-cols-1 gap-3 rounded-lg border border-[var(--border)] p-4 sm:grid-cols-2">
      <Field label="名称">
        <input autoFocus required value={form.name} onChange={(event) => change({ name: event.target.value })} placeholder="订阅源名称" />
      </Field>
      <Field label="类型">
        <select value={form.kind} onChange={(event) => change({ kind: event.target.value as SourceInput["kind"] })}>
          <option value="rss">RSS</option>
          <option value="rsshub">RSSHub</option>
          <option value="google">Google 新闻</option>
          <option value="yahoo">Yahoo 财经</option>
        </select>
      </Field>
      {needsURL && (
        <Field label="订阅地址" className="sm:col-span-2">
          <input
            type="url"
            required
            value={form.url}
            onChange={(event) => change({ url: event.target.value })}
            placeholder="https://example.com/feed.xml"
          />
        </Field>
      )}
      {needsQuery && (
        <Field label="查询词" className="sm:col-span-2">
          <input required value={form.query} onChange={(event) => change({ query: event.target.value })} placeholder="新闻关键词" />
        </Field>
      )}
      <Field label="主题">
        <input value={form.topic} onChange={(event) => change({ topic: event.target.value })} placeholder="科技" />
      </Field>
      <Field label="刷新间隔（分钟）">
        <input
          type="number"
          min={5}
          max={10080}
          required
          value={form.intervalMinutes}
          onChange={(event) => change({ intervalMinutes: event.target.valueAsNumber })}
        />
      </Field>
      <div className="flex items-center gap-2 text-sm">
        <Switch label="启用订阅源" checked={form.enabled} onChange={(enabled) => change({ enabled })} />
        启用
      </div>
      <div className="flex justify-end gap-2 sm:col-span-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          取消
        </Button>
        <Button disabled={busy}>{busy ? "保存中…" : "保存"}</Button>
      </div>
    </form>
  );
}

function pickInput(source: Source): SourceInput {
  return {
    name: source.name,
    kind: source.kind,
    url: source.url,
    query: source.query,
    topic: source.topic,
    enabled: source.enabled,
    intervalMinutes: source.intervalMinutes,
  };
}
