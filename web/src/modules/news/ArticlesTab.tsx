import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, ExternalLink, Plus, RefreshCw, Search, Sparkles, Star } from "lucide-react";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import {
  analyzeArticle,
  refreshAllSources,
  updateArticle,
  useArticle,
  useArticles,
  useDigests,
  useRefreshNews,
  useSources,
  type ArticleFilters,
} from "./queries";
import type { Article } from "./schema";
import { cn } from "../../shared/cn";
import { formatDateTime, formatRelative } from "../../shared/format";
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
  const filtered = filtersActive(filters);
  const items = articles.data?.pages.flatMap((page) => page.items) ?? [];
  const sourceOptions = [{ value: "", label: "全部订阅源" }, ...(sources.data?.items.map((source) => ({ value: source.id, label: source.name })) ?? [])];
  // Topic chips come from what is loaded; the active topic stays even when the page no longer contains it.
  const topics = [...new Set([filters.topic, ...items.map((article) => article.topic)].filter(Boolean))].slice(0, 8);
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
  function updateImmediate(patch: Partial<ArticleFilters>) {
    setDraft((current) => ({ ...current, ...patch }));
    setFilters((current) => ({ ...current, ...patch }));
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    setFilters({ ...draft, q: draft.q.trim(), topic: draft.topic.trim() });
  }

  return (
    <>
      {!filtered && <LatestBrief />}
      {!filtered && items.length >= 4 && (
        <section className="sec" aria-labelledby="news-latest">
          <h2 id="news-latest" className="sec-title">最新文章</h2>
          <ul className="news-grid">
            {items.slice(0, 8).map((article) => (
              <li key={article.id}>
                <button
                  type="button"
                  className="news-card focus-ring"
                  onClick={() => {
                    setExpanded(article.id);
                    document.getElementById(`article-${article.id}`)?.scrollIntoView({ block: "center", behavior: "smooth" });
                  }}
                >
                  <span className="news-card-meta">
                    <SourceMark name={article.sourceName} />
                    <span className="truncate">{article.sourceName}</span>
                    <span aria-hidden="true">·</span>
                    <span className="shrink-0">{formatRelative(article.publishedAt)}</span>
                  </span>
                  <span className={cn("news-card-title", !article.read && "is-unread")}>{article.translatedTitle || article.title}</span>
                  {article.topic && <span className="news-tag">{article.topic}</span>}
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
      <section className="sec" aria-labelledby="news-all">
        <h2 id="news-all" className="sec-title">全部文章</h2>
        <form onSubmit={submit} className="news-filters">
          <label className="news-search">
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
            className="news-source-select"
            value={draft.sourceId}
            onValueChange={(sourceId) => updateImmediate({ sourceId })}
            options={sourceOptions}
          />
          <label className="news-topic-input">
            <span className="sr-only">文章话题</span>
            <input
              value={draft.topic}
              onChange={(event) => setDraft((current) => ({ ...current, topic: event.target.value }))}
              placeholder="全部话题"
              className="ui-input w-full"
            />
          </label>
          <Button variant="secondary">搜索</Button>
        </form>
        <div className="chip-row news-chips" role="group" aria-label="筛选文章">
          <button
            type="button"
            className={cn("chip focus-ring", !filters.topic && "is-active")}
            aria-pressed={!filters.topic}
            onClick={() => updateImmediate({ topic: "" })}
          >
            全部话题
          </button>
          {topics.map((topic) => (
            <button
              key={topic}
              type="button"
              className={cn("chip focus-ring", filters.topic === topic && "is-active")}
              aria-pressed={filters.topic === topic}
              onClick={() => updateImmediate({ topic: filters.topic === topic ? "" : topic })}
            >
              {topic}
            </button>
          ))}
          <span className="grow" aria-hidden="true" />
          <FilterChip label="只看未读" pressed={filters.unread} onChange={(unread) => updateImmediate({ unread })} />
          <FilterChip label="收藏" pressed={filters.starred} onChange={(starred) => updateImmediate({ starred })} />
          <FilterChip label="已匹配" pressed={filters.matched} onChange={(matched) => updateImmediate({ matched })} />
        </div>
        {articles.isPending && (
          <div className="space-y-2">
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
            <Skeleton className="h-12" />
          </div>
        )}
        {articles.isError && (
          <Notice tone="danger" title="资讯读取失败">
            {articles.error.message}
          </Notice>
        )}
        {articles.data && items.length === 0 && (
          <ArticlesEmpty
            noSources={sources.data?.items.length === 0}
            filtered={filtered}
            refreshing={refreshAll.isPending}
            onAddSource={onAddSource}
            onClearFilters={clearFilters}
            onRefresh={() => refreshAll.mutate()}
          />
        )}
        {items.length > 0 && (
          <div className="news-table">
            <div className="news-head" aria-hidden="true">
              <span>时间</span>
              <span className="news-col-src">来源</span>
              <span>标题</span>
              <span className="news-col-tag">话题</span>
              <span />
            </div>
            {items.map((article) => (
              <ArticleRow
                key={article.id}
                article={article}
                expanded={expanded === article.id}
                onToggle={() => setExpanded((current) => (current === article.id ? null : article.id))}
              />
            ))}
          </div>
        )}
        {articles.hasNextPage && (
          <div className="mt-4 flex justify-center">
            <Button variant="secondary" disabled={articles.isFetchingNextPage} onClick={() => articles.fetchNextPage()}>
              {articles.isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </section>
    </>
  );
}

/** The newest digest, shown like the prototype's AI 简报 card; nothing when none exists yet. */
function LatestBrief() {
  const digests = useDigests();
  const digest = digests.data?.pages[0]?.items[0];
  if (!digest) return null;
  return (
    <section className="sec" aria-labelledby="news-brief">
      <h2 id="news-brief" className="sec-title">简报</h2>
      <div className="news-brief">
        <div className="news-brief-head">
          <span className="news-brief-mark" aria-hidden="true">
            <Sparkles size={16} />
          </span>
          <div className="min-w-0 flex-1">
            <b className="block truncate">{digest.title}</b>
            <span>
              {digest.aiUsed ? "AI 摘要" : "按规则整理"} · {digest.items.length} 篇 · {formatRelative(digest.createdAt)}
            </span>
          </div>
        </div>
        {digest.items.length > 0 ? (
          <ol className="news-brief-list">
            {digest.items.slice(0, 6).map((item) => {
              const href = safeExternalURL(item.url);
              return (
                <li key={item.id}>
                  {href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer">
                      {item.title}
                    </a>
                  ) : (
                    item.title
                  )}
                  <span className="news-note"> · {item.sourceName}</span>
                </li>
              );
            })}
          </ol>
        ) : (
          <p className="news-brief-text">{digest.content}</p>
        )}
      </div>
    </section>
  );
}

/** Letter mark standing in for a source favicon (no third-party favicon requests). */
function SourceMark({ name }: { name: string }) {
  const letter = Array.from(name.trim())[0]?.toUpperCase() ?? "?";
  let hue = 0;
  for (const char of name) hue = (hue * 31 + char.charCodeAt(0)) % 360;
  return (
    <span className="news-mark" style={{ backgroundColor: `hsl(${hue} 55% 42%)` }} aria-hidden="true">
      {letter}
    </span>
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

function FilterChip({ label, pressed, onChange }: { label: string; pressed: boolean; onChange: (pressed: boolean) => void }) {
  return (
    <button type="button" className={cn("chip focus-ring", pressed && "is-active")} aria-pressed={pressed} onClick={() => onChange(!pressed)}>
      {label}
    </button>
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
    <article id={`article-${article.id}`} className={cn("news-row", !article.read && "is-unread", expanded && "is-open")}>
      <div className="news-row-main">
        <button className="news-row-toggle focus-ring" onClick={onToggle} aria-expanded={expanded}>
          <span className="news-col-time">{formatRelative(article.publishedAt)}</span>
          <span className="news-col-src">
            <SourceMark name={article.sourceName} />
            <span className="truncate">{article.sourceName}</span>
          </span>
          <span className="news-col-title">
            <span className="news-title-line">
              <span className="news-title" title={article.translatedTitle || article.title}>{article.translatedTitle || article.title}</span>
              {article.matched && <Badge className="shrink-0">匹配</Badge>}
            </span>
            {article.translatedTitle && article.translatedTitle !== article.title && (
              <span className="news-subtitle">{article.title}</span>
            )}
          </span>
          <span className="news-col-tag">{article.topic ? <span className="news-tag">{article.topic}</span> : null}</span>
        </button>
        <div className="news-row-actions">
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
        <div className="news-detail">
          <p className="news-note">
            {article.sourceName} · {article.topic || "未分类"} · {formatDateTime(article.publishedAt, { seconds: false })}
          </p>
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
