import { useState, type FormEvent, type ReactNode } from "react";
import { useMutation } from "@tanstack/react-query";
import { Send, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { EmptyState } from "../../components/ui/EmptyState";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Select } from "../../components/ui/Select";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { cn } from "../../shared/cn";
import { formatDateTime } from "../../shared/format";
import { DigestLinks } from "./DigestLinks";
import { NewsDialog } from "./NewsDialog";
import { createDigest, pushDigest, useDigests, useRefreshNews } from "./queries";
import type { Digest } from "./schema";

const PERIODS = [
  { value: "6", label: "最近 6 小时" },
  { value: "12", label: "最近 12 小时" },
  { value: "24", label: "最近 24 小时" },
  { value: "72", label: "最近 3 天" },
  { value: "168", label: "最近 7 天" },
];

export default function DigestsTab({ nav, notices }: { nav: ReactNode; notices: ReactNode }) {
  const digests = useDigests();
  const refresh = useRefreshNews();
  const [creating, setCreating] = useState(false);
  const [topic, setTopic] = useState("");
  const [periodHours, setPeriodHours] = useState(24);
  const [maxItems, setMaxItems] = useState(20);
  const [push, setPush] = useState(false);

  const create = useMutation({
    mutationFn: () => createDigest({ topic: topic.trim(), periodHours, maxItems, push }),
    onSuccess: async (digest) => {
      setCreating(false);
      await refresh();
      toast.success(digest.notificationId ? "简报已生成，推送已提交" : "简报已生成");
    },
    onError: (error) => toast.error(error.message),
  });

  const items = digests.data?.pages.flatMap((page) => page.items) ?? [];
  const [latest, ...older] = items;

  function submit(event: FormEvent) {
    event.preventDefault();
    create.mutate();
  }

  return (
    <>
      <div className="toolbar news-toolbar">
        {nav}
        <div className="news-toolbar-actions">
          <Button size="sm" onClick={() => setCreating(true)}>
            <Sparkles size={15} />
            生成简报
          </Button>
        </div>
      </div>
      {notices}
      <section className="sec" aria-labelledby="news-digest-history">
        <h2 id="news-digest-history" className="sr-only">简报</h2>
        {digests.isPending && (
          <div className="space-y-3">
            <Skeleton className="h-36 rounded-2xl" />
            <Skeleton className="h-28 rounded-2xl" />
          </div>
        )}
        {digests.isError && (
          <Notice className="mb-4" tone="danger" title="简报读取失败">
            {digests.error.message}
          </Notice>
        )}
        {digests.data && items.length === 0 && (
          <EmptyState
            title="暂无简报"
            action={
              <Button onClick={() => setCreating(true)}>
                <Sparkles size={15} />
                生成简报
              </Button>
            }
          />
        )}
        {latest && <DigestRow digest={latest} featured />}
        {older.length > 0 && (
          <div className="ui-card news-digest-history">
            {older.map((digest) => (
              <DigestRow key={digest.id} digest={digest} />
            ))}
          </div>
        )}
        {digests.hasNextPage && (
          <div className="mt-4 flex justify-center">
            <Button variant="secondary" disabled={digests.isFetchingNextPage} onClick={() => digests.fetchNextPage()}>
              {digests.isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </section>
      <NewsDialog open={creating} title="生成简报" onOpenChange={setCreating}>
        <form onSubmit={submit}>
          <fieldset className="form-fields" disabled={create.isPending}>
            <Field label="话题">
              <input autoFocus value={topic} onChange={(event) => setTopic(event.target.value)} placeholder="全部话题" />
            </Field>
            <div className="field-pair">
              <Field label="时间范围">
                <Select value={String(periodHours)} onValueChange={(value) => setPeriodHours(Number(value))} options={PERIODS} />
              </Field>
              <Field label="最多条数">
                <input
                  type="number"
                  min={1}
                  max={30}
                  required
                  value={maxItems}
                  onChange={(event) => setMaxItems(event.target.valueAsNumber)}
                />
              </Field>
            </div>
            <label className="inline-switch">
              <Switch label="提交推送" checked={push} onChange={setPush} />
              <span aria-hidden="true">提交推送</span>
            </label>
          </fieldset>
          <div className="settings-dialog-footer">
            <Button type="button" variant="secondary" disabled={create.isPending} onClick={() => setCreating(false)}>
              取消
            </Button>
            <Button disabled={create.isPending}>
              <Sparkles size={15} />
              {create.isPending ? "生成中…" : "生成简报"}
            </Button>
          </div>
        </form>
      </NewsDialog>
    </>
  );
}

function DigestRow({ digest, featured = false }: { digest: Digest; featured?: boolean }) {
  const refresh = useRefreshNews();
  const push = useMutation({
    mutationFn: () => pushDigest(digest.id),
    onSuccess: () => {
      void refresh();
      toast.success("推送已提交");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <article className={cn("news-digest", featured && "ui-card news-brief")}>
      <div className="news-brief-head">
        {featured && (
          <span className="news-brief-mark" aria-hidden="true">
            <Sparkles size={16} />
          </span>
        )}
        <div className="min-w-0 flex-1">
          <b className="block break-words">{digest.title}</b>
          <span>
            {digest.topic || "全部话题"} · {formatDateTime(digest.createdAt, { seconds: false })} · {digest.aiUsed ? "AI 简报" : "普通简报"}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {digest.notificationId && <Badge tone="info">推送已提交</Badge>}
          <Button size="sm" variant="secondary" disabled={push.isPending} onClick={() => push.mutate()}>
            <Send size={14} />
            {push.isPending ? "提交中…" : "发送推送"}
          </Button>
        </div>
      </div>
      {digest.content && (digest.aiUsed || digest.items.length === 0) && <p className="news-brief-text">{digest.content}</p>}
      {digest.items.length > 0 && <DigestLinks items={digest.items} />}
    </article>
  );
}
