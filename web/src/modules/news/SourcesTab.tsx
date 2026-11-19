import { useRef, useState, type FormEvent, type ReactNode } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState } from "../../components/ui/EmptyState";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Select } from "../../components/ui/Select";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { cn } from "../../shared/cn";
import { formatDateTime } from "../../shared/format";
import { NewsDialog } from "./NewsDialog";
import { deleteSource, refreshAllSources, refreshSource, saveSource, useRefreshNews, useSources } from "./queries";
import type { Source, SourceInput } from "./schema";

const SOURCE_KINDS = [
  { value: "rss", label: "RSS" },
  { value: "rsshub", label: "RSSHub" },
  { value: "google", label: "Google 新闻" },
  { value: "yahoo", label: "Yahoo 财经" },
];

const SOURCE_LABEL: Record<string, string> = Object.fromEntries(SOURCE_KINDS.map((kind) => [kind.value, kind.label]));

const blank: SourceInput = { name: "", kind: "rss", url: "", query: "", topic: "", enabled: true, intervalMinutes: 60 };

export default function SourcesTab({
  nav,
  notices,
  startAdding = false,
}: {
  nav: ReactNode;
  notices: ReactNode;
  startAdding?: boolean;
}) {
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
    <>
      <div className="toolbar news-toolbar">
        {nav}
        <div className="news-toolbar-actions">
          <Button size="sm" variant="secondary" disabled={allRefresh.isPending || items.length === 0} onClick={() => allRefresh.mutate()}>
            <RefreshCw size={15} />
            {allRefresh.isPending ? "刷新中…" : "全部刷新"}
          </Button>
          <Button ref={addButton} size="sm" onClick={() => setEditing("new")}>
            <Plus size={15} />
            添加订阅源
          </Button>
        </div>
      </div>
      {notices}
      <section className="sec news-sources" aria-labelledby="news-sources-title">
        <h2 id="news-sources-title" className="sr-only">订阅源</h2>
        {sources.isError && (
          <Notice className="mb-4" tone="danger" title="订阅源读取失败">
            {sources.error.message}
          </Notice>
        )}
        {sources.isPending && (
          <div className="feeds">
            <Skeleton className="h-[88px] rounded-2xl" />
            <Skeleton className="h-[88px] rounded-2xl" />
          </div>
        )}
        {sources.data && items.length === 0 && (
          <EmptyState
            title="暂无订阅源"
            action={
              <Button onClick={() => setEditing("new")}>
                <Plus size={15} />
                添加订阅源
              </Button>
            }
          />
        )}
        {items.length > 0 && (
          <div className="feeds">
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
                  <label className="inline-switch">
                    <Switch
                      label={`启用 ${source.name}`}
                      checked={source.enabled}
                      disabled={save.isPending}
                      onChange={(enabled) => save.mutate({ ...pickInput(source), enabled, id: source.id })}
                    />
                    <span className="sr-only">启用 {source.name}</span>
                  </label>
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
          </div>
        )}
        <NewsDialog
          open={editing !== null}
          title={editing === "new" ? "添加订阅源" : "编辑订阅源"}
          onOpenChange={(open) => {
            if (!open && !save.isPending) setEditing(null);
          }}
        >
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
        </NewsDialog>
        <ConfirmDialog {...deletion.dialogProps} />
      </section>
    </>
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
    <form onSubmit={submit}>
      <fieldset className="form-fields" disabled={busy}>
        <div className="field-pair">
          <Field label="名称">
            <input autoFocus required value={form.name} onChange={(event) => change({ name: event.target.value })} placeholder="例如：路透社" />
          </Field>
          <Field label="类型">
            <Select value={form.kind} onValueChange={(kind) => change({ kind: kind as SourceInput["kind"] })} options={SOURCE_KINDS} />
          </Field>
        </div>
        {needsURL && (
          <Field label="订阅地址">
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
          <Field label="查询词">
            <input required value={form.query} onChange={(event) => change({ query: event.target.value })} placeholder="新闻关键词" />
          </Field>
        )}
        <div className="field-pair">
          <Field label="话题">
            <input value={form.topic} onChange={(event) => change({ topic: event.target.value })} placeholder="例如：科技" />
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
        </div>
        <label className="inline-switch">
          <Switch label="启用订阅源" checked={form.enabled} onChange={(enabled) => change({ enabled })} />
          <span aria-hidden="true">启用</span>
        </label>
      </fieldset>
      <div className="settings-dialog-footer">
        <Button type="button" variant="secondary" disabled={busy} onClick={onCancel}>
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
