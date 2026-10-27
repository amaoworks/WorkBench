import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { cn } from "../../shared/cn";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { deleteSource, refreshAllSources, refreshSource, saveSource, useRefreshNews, useSources } from "./queries";
import type { Source, SourceInput } from "./schema";
import { formatDateTime } from "../../shared/format";
import { Select } from "../../components/ui/Select";

const SOURCE_KINDS = [
  { value: "rss", label: "RSS" },
  { value: "rsshub", label: "RSSHub" },
  { value: "google", label: "Google 新闻" },
  { value: "yahoo", label: "Yahoo 财经" },
];

const SOURCE_LABEL: Record<string, string> = Object.fromEntries(SOURCE_KINDS.map((kind) => [kind.value, kind.label]));

const blank: SourceInput = { name: "", kind: "rss", url: "", query: "", topic: "", enabled: true, intervalMinutes: 60 };

export default function SourcesTab({ startAdding = false }: { startAdding?: boolean }) {
  const sources = useSources();
  const refresh = useRefreshNews();
  const [editing, setEditing] = useState<Source | "new" | null>(startAdding ? "new" : null);
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
      toast.success(`刷新完成，新增 ${result.added} 篇`);
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
      toast.success(
        result.failed > 0
          ? `刷新完成，新增 ${result.added} 篇，失败 ${result.failed} 个订阅源`
          : `刷新完成，新增 ${result.added} 篇`,
      );
    },
    onError: (error) => toast.error(error.message),
  });
  const items = sources.data?.items ?? [];

  return (
    <section className="sec news-sources" aria-labelledby="news-sources-title">
      <h2 id="news-sources-title" className="sec-title">
        订阅源
        {sources.data && <span className="sec-count">{items.length}</span>}
        <span className="sec-action">
          <Button size="sm" variant="secondary" disabled={allRefresh.isPending} onClick={() => allRefresh.mutate()}>
            <RefreshCw size={15} />
            {allRefresh.isPending ? "刷新中…" : "全部刷新"}
          </Button>
        </span>
      </h2>
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
      {sources.isError && (
        <Notice className="mb-4" tone="danger" title="订阅源读取失败">
          {sources.error.message}
        </Notice>
      )}
      <div className="feeds">
        {sources.isPending && (
          <>
            <Skeleton className="h-[84px] rounded-2xl" />
            <Skeleton className="h-[84px] rounded-2xl" />
          </>
        )}
        {sources.data && items.length === 0 && (
          <div className="feed feed-empty">
            <div className="fd-t">
              <b>暂无订阅源</b>
              <span>添加 RSS 或新闻搜索订阅源。</span>
            </div>
          </div>
        )}
        {items.map((source) => (
          <div key={source.id} className={cn("feed news-source", !source.enabled && "is-off")}>
            <span className="ic-circ" aria-hidden="true">{source.name.trim().slice(0, 1).toUpperCase() || "#"}</span>
            <div className="fd-t">
              <b>{source.name}</b>
              <span>
                {[source.topic || "未分类", `${source.itemCount} 条`, `每 ${source.intervalMinutes} 分钟`, SOURCE_LABEL[source.kind] ?? source.kind].join(" · ")}
              </span>
              {source.lastError ? (
                <span className="fd-error" title={source.lastError}>刷新失败：{source.lastError}</span>
              ) : (
                source.lastFetchedAt && <span>最近刷新 {formatDateTime(source.lastFetchedAt, { seconds: false })}</span>
              )}
            </div>
            <div className="feed-actions">
              <Switch
                label={`启用 ${source.name}`}
                checked={source.enabled}
                disabled={save.isPending}
                onChange={(enabled) => save.mutate({ ...pickInput(source), enabled, id: source.id })}
              />
              <div className="feed-tools">
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`刷新 ${source.name}`}
                  disabled={singleRefresh.isPending}
                  onClick={() => singleRefresh.mutate(source.id)}
                >
                  <RefreshCw size={15} />
                </Button>
                <Button size="icon" variant="ghost" aria-label={`编辑 ${source.name}`} onClick={() => setEditing(source)}>
                  <Pencil size={15} />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`删除 ${source.name}`}
                  onClick={(event) => deletion.request(source, event.currentTarget)}
                >
                  <Trash2 size={15} />
                </Button>
              </div>
            </div>
          </div>
        ))}
        <button ref={addButton} type="button" className="feed feed-add focus-ring" onClick={() => setEditing("new")}>
          <Plus size={20} aria-hidden="true" />
          <span className="fd-t">
            <b>添加订阅源</b>
            <span aria-hidden="true">RSS · RSSHub · Google 新闻 · Yahoo 财经</span>
          </span>
        </button>
      </div>
      <ConfirmDialog {...deletion.dialogProps} />
    </section>
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
    <form onSubmit={submit} className="inline-form news-source-form">
      <Field label="名称">
        <input autoFocus required value={form.name} onChange={(event) => change({ name: event.target.value })} placeholder="订阅源名称" />
      </Field>
      <Field label="类型">
        <Select value={form.kind} onValueChange={(kind) => change({ kind: kind as SourceInput["kind"] })} options={SOURCE_KINDS} />
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
