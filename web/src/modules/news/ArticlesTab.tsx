import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, ExternalLink, Plus, RefreshCw, Search, Sparkles, Star } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import {
  analyzeArticle,
  refreshAllSources,
  updateArticle,
  useArticle,
  useArticles,
  useRefreshNews,
  useSources,
  type ArticleFilters,
} from "./queries";
import type { Article } from "./schema";
import { cn } from "../../shared/cn";
import { formatDateTime } from "../../shared/format";
import { Checkbox } from "../../components/ui/Checkbox";
import { Select } from "../../components/ui/Select";

const emptyFilters: ArticleFilters = { q: "", sourceId: "", topic: "", unread: false, starred: false, matched: false };

const filtersActive = (filters: ArticleFilters) =>
  (Object.keys(emptyFilters) as (keyof ArticleFilters)[]).some((key) => filters[key] !== emptyFilters[key]);

export default function ArticlesTab({ onAddSource }: { onAddSource: () => void }) {
  const [draft, setDraft] = useState(emptyFilters);
  const [filters, setFilters] = useState(emptyFilters);
  const [expanded, setExpanded] = useState<string | null>(null);
  const articles = useArticles(filters);
  const sources = useSources();
  const items = articles.data?.pages.flatMap((page) => page.items) ?? [];
  const sourceOptions = [{ value: "", label: "全部订阅源" }, ...(sources.data?.items.map((source) => ({ value: source.id, label: source.name })) ?? [])];
  const refreshNews = useRefreshNews();
  const refreshAll = useMutation({
    mutationFn: refreshAllSources,
    onSuccess: async (result) => {
      await refreshNews();
      toast.success(
        result.failed > 0
          ? `刷新完成，新增 ${result.added} 篇，失败 ${result.failed} 个订阅源`
          : `刷新完成，新增 ${result.added} 篇`,
      );
    },
    onError: (error) => toast.error(error.message),
  });
  function clearFilters() {
    setDraft(emptyFilters);
    setFilters(emptyFilters);
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    setFilters({ ...draft, q: draft.q.trim(), topic: draft.topic.trim() });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <form onSubmit={submit} className="space-y-3">
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(10rem,0.45fr)_minmax(9rem,0.35fr)_auto]">
            <label className="relative block">
              <span className="sr-only">搜索资讯</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--muted)]" size={17} />
              <input
                value={draft.q}
                onChange={(event) => setDraft((current) => ({ ...current, q: event.target.value }))}
                placeholder="搜索标题或内容"
                className="ui-input w-full pl-9!"
              />
            </label>
            <Select
              aria-label="订阅源"
              value={draft.sourceId}
              onValueChange={(sourceId) => setDraft((current) => ({ ...current, sourceId }))}
              options={sourceOptions}
            />
            <label>
              <span className="sr-only">主题</span>
              <input
                value={draft.topic}
                onChange={(event) => setDraft((current) => ({ ...current, topic: event.target.value }))}
                placeholder="全部主题"
                className="ui-input w-full"
              />
            </label>
            <Button>筛选</Button>
          </div>
          <div className="flex flex-wrap gap-4 text-sm">
            <FilterCheck label="未读" checked={draft.unread} onChange={(unread) => setDraft((current) => ({ ...current, unread }))} />
            <FilterCheck label="收藏" checked={draft.starred} onChange={(starred) => setDraft((current) => ({ ...current, starred }))} />
            <FilterCheck label="已匹配" checked={draft.matched} onChange={(matched) => setDraft((current) => ({ ...current, matched }))} />
          </div>
        </form>
      </Card>
      <Card>
        {articles.isPending && (
          <div className="space-y-3 p-4">
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
          </div>
        )}
        {articles.isError && (
          <Notice className="m-4" tone="danger" title="资讯读取失败">
            {articles.error.message}
          </Notice>
        )}
        {articles.data && items.length === 0 && (
          <ArticlesEmpty
            noSources={sources.data?.items.length === 0}
            filtered={filtersActive(filters)}
            refreshing={refreshAll.isPending}
            onAddSource={onAddSource}
            onClearFilters={clearFilters}
            onRefresh={() => refreshAll.mutate()}
          />
        )}
        <div className="divide-y divide-[var(--border)]">
          {items.map((article) => (
            <ArticleRow
              key={article.id}
              article={article}
              expanded={expanded === article.id}
              onToggle={() => setExpanded((current) => (current === article.id ? null : article.id))}
            />
          ))}
        </div>
        {articles.hasNextPage && (
          <div className="p-4">
            <Button variant="secondary" disabled={articles.isFetchingNextPage} onClick={() => articles.fetchNextPage()}>
              {articles.isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </Card>
    </div>
  );
}

function ArticlesEmpty({
  noSources,
  filtered,
  refreshing,
  onAddSource,
  onClearFilters,
  onRefresh,
}: {
  noSources: boolean;
  filtered: boolean;
  refreshing: boolean;
  onAddSource: () => void;
  onClearFilters: () => void;
  onRefresh: () => void;
}) {
  if (noSources)
    return (
      <EmptyState
        title="暂无订阅源"
        description="添加 RSS 或新闻搜索订阅源后，资讯会显示在这里。"
        action={
          <Button onClick={onAddSource}>
            <Plus size={15} />
            添加订阅源
          </Button>
        }
      />
    );
  if (filtered)
    return (
      <EmptyState
        icon={<Search size={22} />}
        title="没有符合条件的资讯"
        description="调整或清除筛选条件后重试。"
        action={
          <Button variant="secondary" onClick={onClearFilters}>
            清除筛选
          </Button>
        }
      />
    );
  return (
    <EmptyState
      title="暂无资讯"
      description="订阅源尚未抓取到资讯，可立即刷新。"
      action={
        <Button variant="secondary" disabled={refreshing} onClick={onRefresh}>
          <RefreshCw size={15} />
          {refreshing ? "刷新中…" : "刷新"}
        </Button>
      }
    />
  );
}

function FilterCheck({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <Checkbox checked={checked} onChange={onChange}>
      {label}
    </Checkbox>
  );
}

function ArticleRow({ article, expanded, onToggle }: { article: Article; expanded: boolean; onToggle: () => void }) {
  const detail = useArticle(expanded ? article.id : null);
  const refresh = useRefreshNews();
  const update = useMutation({
    mutationFn: (patch: { read?: boolean; starred?: boolean }) => updateArticle(article.id, patch),
    onSuccess: () => void refresh(),
    onError: (error) => toast.error(error.message),
  });
  const analyze = useMutation({
    mutationFn: () => analyzeArticle(article.id),
    onSuccess: () => {
      void refresh();
      toast.success("AI 分析已完成");
    },
    onError: (error) => {
      void refresh();
      toast.error(error.message);
    },
  });
  const shown = detail.data ?? article;
  const externalURL = safeExternalURL(shown.url);
  return (
    <article className={cn("p-4", !article.read && "bg-[var(--brand-soft)]/30")}>
      <div className="flex min-w-0 items-start gap-3">
        <button className="focus-ring min-w-0 flex-1 rounded text-left" onClick={onToggle} aria-expanded={expanded}>
          <div className="flex min-w-0 items-start gap-2">
            <h2 className={cn("min-w-0 flex-1 break-words", !article.read && "font-semibold")}>
              {article.translatedTitle || article.title}
            </h2>
            {article.matched && <Badge className="shrink-0">匹配</Badge>}
          </div>
          {article.translatedTitle && article.translatedTitle !== article.title && (
            <p className="mt-1 truncate text-xs text-[var(--muted)]">{article.title}</p>
          )}
          <p className="mt-2 text-xs text-[var(--muted)]">
            {article.sourceName} · {article.topic || "未分类"} · {formatDateTime(article.publishedAt, { seconds: false })}
          </p>
        </button>
        <div className="flex shrink-0 gap-1">
          <Button
            size="icon"
            variant="ghost"
            aria-label={article.starred ? "取消收藏" : "收藏"}
            disabled={update.isPending}
            onClick={() => update.mutate({ starred: !article.starred })}
          >
            <Star size={17} fill={article.starred ? "currentColor" : "none"} />
          </Button>
          <Button size="icon" variant="ghost" aria-label={expanded ? "收起" : "展开"} onClick={onToggle}>
            {expanded ? <ChevronUp size={17} /> : <ChevronDown size={17} />}
          </Button>
        </div>
      </div>
      {expanded && (
        <div className="mt-4 min-w-0 space-y-4 border-t border-[var(--border)] pt-4">
          {detail.isPending && <Skeleton className="h-28" />}
          {detail.isError && (
            <Notice tone="danger" title="资讯详情读取失败">
              {detail.error.message}
            </Notice>
          )}
          {!detail.isPending && (
            <>
              {shown.summary && (
                <section>
                  <h3 className="text-sm font-medium">摘要</h3>
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm">{shown.summary}</p>
                </section>
              )}
              {shown.translatedContent && (
                <section>
                  <h3 className="text-sm font-medium">翻译</h3>
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm text-[var(--muted)]">{shown.translatedContent}</p>
                </section>
              )}
              {shown.content && (
                <section>
                  <h3 className="text-sm font-medium">原文</h3>
                  <p className="mt-1 whitespace-pre-wrap break-words text-sm text-[var(--muted)]">{shown.content}</p>
                </section>
              )}
              {shown.origins.length > 0 && (
                <section>
                  <h3 className="text-sm font-medium">文章来源</h3>
                  <ul className="mt-1 space-y-1 text-sm">
                    {shown.origins.map((origin, index) => {
                      const href = safeExternalURL(origin.url);
                      return (
                        <li key={`${origin.sourceId}-${index}`} className="break-all">
                          {href ? (
                            <a className="text-[var(--brand)] hover:underline" href={href} target="_blank" rel="noopener noreferrer">
                              {origin.sourceName}
                              <ExternalLink className="ml-1 inline" size={13} />
                            </a>
                          ) : (
                            origin.sourceName
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}
              {shown.aiStatus === "running" && <Badge>AI 分析中…</Badge>}
              {shown.aiStatus === "error" && (
                <Notice tone="danger" title="AI 分析失败">
                  {shown.aiError}
                </Notice>
              )}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="secondary" disabled={update.isPending} onClick={() => update.mutate({ read: !article.read })}>
                  {article.read ? "标为未读" : "标为已读"}
                </Button>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={analyze.isPending || shown.aiStatus === "running"}
                  onClick={() => analyze.mutate()}
                >
                  <Sparkles size={15} />
                  {analyze.isPending ? "分析中…" : shown.aiStatus === "error" ? "重试分析" : "AI 分析"}
                </Button>
                {externalURL && (
                  <a className="studio-button secondary" href={externalURL} target="_blank" rel="noopener noreferrer">
                    查看原文
                    <ExternalLink size={14} />
                  </a>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </article>
  );
}

function safeExternalURL(value: string) {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
  } catch {
    return null;
  }
}
