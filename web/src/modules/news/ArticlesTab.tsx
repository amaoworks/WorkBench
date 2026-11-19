import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, ExternalLink, Plus, RefreshCw, Search, Sparkles, Star } from "lucide-react";
import { toast } from "sonner";
import { Button, ButtonAnchor } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Select } from "../../components/ui/Select";
import { Skeleton } from "../../components/ui/States";
import {
  analyzeArticle,
  refreshAllSources,
  updateArticle,
  useArticle,
  useArticles,
  useDigests,
  useRefreshNews,
  useRules,
  useSources,
  type ArticleFilters,
} from "./queries";
import type { Article } from "./schema";
import { cn } from "../../shared/cn";
import { formatDateTime, formatRelative } from "../../shared/format";
import { safeExternalURL } from "../../shared/url";
import { DigestLinks } from "./DigestLinks";

const emptyFilters: ArticleFilters = { q: "", sourceId: "", topic: "", unread: false, starred: false, matched: false };

const filtersActive = (filters: ArticleFilters) =>
  (Object.keys(emptyFilters) as (keyof ArticleFilters)[]).some((key) => filters[key] !== emptyFilters[key]);

function filtersFromParams(params: URLSearchParams): ArticleFilters {
  return {
    q: params.get("q") ?? "",
    sourceId: params.get("source") ?? "",
    topic: params.get("topic") ?? "",
    unread: params.get("unread") === "1",
    starred: params.get("starred") === "1",
    matched: params.get("matched") === "1",
  };
}

function writeFilters(current: URLSearchParams, filters: ArticleFilters) {
  const next = new URLSearchParams(current);
  const set = (key: string, value: string) => {
    if (value) next.set(key, value);
    else next.delete(key);
  };
  set("q", filters.q);
  set("source", filters.sourceId);
  set("topic", filters.topic);
  set("unread", filters.unread ? "1" : "");
  set("starred", filters.starred ? "1" : "");
  set("matched", filters.matched ? "1" : "");
  return next;
}

export default function ArticlesTab({
  nav,
  notices,
  onAddSource,
  onOpenDigests,
}: {
  nav: ReactNode;
  notices: ReactNode;
  onAddSource: () => void;
  onOpenDigests: () => void;
}) {
  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => filtersFromParams(params), [params]);
  const [draftQ, setDraftQ] = useState(filters.q);
  const [syncedQ, setSyncedQ] = useState(filters.q);
  if (filters.q !== syncedQ) {
    setSyncedQ(filters.q);
    setDraftQ(filters.q);
  }
  const [expanded, setExpanded] = useState<string | null>(null);
  const articles = useArticles(filters);
  const sources = useSources();
  const rules = useRules();
  const filtered = filtersActive(filters);
  const items = articles.data?.pages.flatMap((page) => page.items) ?? [];

  const sourceOptions = [
    { value: "", label: "全部订阅源" },
    ...(sources.data?.items.map((source) => ({ value: source.id, label: source.name })) ?? []),
  ];
  const topicValues = [
    ...new Set(
      [
        filters.topic,
        ...(sources.data?.items.map((source) => source.topic) ?? []),
        ...(rules.data?.items.map((rule) => rule.topic) ?? []),
        ...items.map((article) => article.topic),
      ].filter(Boolean),
    ),
  ];
  const topicOptions = [{ value: "", label: "全部话题" }, ...topicValues.map((topic) => ({ value: topic, label: topic }))];

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

  function commit(next: ArticleFilters) {
    setParams((current) => writeFilters(current, next), { replace: true });
  }
  function clearFilters() {
    setDraftQ("");
    commit(emptyFilters);
  }
  function updateImmediate(patch: Partial<ArticleFilters>) {
    commit({ ...filters, ...patch });
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    commit({ ...filters, q: draftQ.trim() });
  }

  const allStatus = !filters.unread && !filters.starred && !filters.matched;

  return (
    <>
      <div className="toolbar news-toolbar">
        {nav}
        <div className="news-toolbar-actions">
          <Button type="button" size="sm" variant="secondary" disabled={refreshAll.isPending} onClick={() => refreshAll.mutate()}>
            <RefreshCw size={15} />
            {refreshAll.isPending ? "刷新中…" : "刷新"}
          </Button>
        </div>
      </div>
      {notices}
      {!filtered && <LatestBriefStrip onOpenDigests={onOpenDigests} />}
      <section className="sec" aria-labelledby="news-articles-heading">
        <h2 id="news-articles-heading" className="sr-only">全部文章</h2>
        <div className="news-controls">
          <form onSubmit={submit} className="news-filter-bar">
            <label className="news-search">
              <span className="sr-only">搜索资讯</span>
              <Search className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--muted)]" size={16} />
              <input
                value={draftQ}
                onChange={(event) => setDraftQ(event.target.value)}
                placeholder="搜索标题或内容"
                className="ui-input w-full pl-9!"
              />
            </label>
            <Select
              aria-label="订阅源"
              className="news-filter-select"
              value={filters.sourceId}
              onValueChange={(sourceId) => updateImmediate({ sourceId })}
              options={sourceOptions}
            />
            <Select
              aria-label="话题"
              className="news-filter-select"
              value={filters.topic}
              onValueChange={(topic) => updateImmediate({ topic })}
              options={topicOptions}
            />
            <Button size="sm" variant="secondary">搜索</Button>
          </form>
          <div className="chip-row news-status-row" role="group" aria-label="筛选文章">
            <FilterChip
              label="全部"
              pressed={allStatus}
              onChange={() => updateImmediate({ unread: false, starred: false, matched: false })}
            />
            <FilterChip label="未读" pressed={filters.unread} onChange={(unread) => updateImmediate({ unread })} />
            <FilterChip label="收藏" pressed={filters.starred} onChange={(starred) => updateImmediate({ starred })} />
            <FilterChip label="已匹配" pressed={filters.matched} onChange={(matched) => updateImmediate({ matched })} />
            {filtered && (
              <Button type="button" size="sm" variant="ghost" className="ml-auto" onClick={clearFilters}>
                清除筛选
              </Button>
            )}
          </div>
        </div>
        {articles.isPending && (
          <div className="space-y-2">
            <Skeleton className="h-16 rounded-xl" />
            <Skeleton className="h-16 rounded-xl" />
            <Skeleton className="h-16 rounded-xl" />
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
          <div className="ui-card news-list">
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

/** Compact collapsible bar for the newest digest; keeps the article stream immediately visible. */
function LatestBriefStrip({ onOpenDigests }: { onOpenDigests: () => void }) {
  const digests = useDigests();
  const [open, setOpen] = useState(false);
  const digest = digests.data?.pages[0]?.items[0];
  if (!digest) return null;
  return (
    <section className="ui-card news-brief-strip" aria-label="最新简报">
      <div className="news-brief-strip-head">
        <span className="news-brief-mark" aria-hidden="true">
          <Sparkles size={15} />
        </span>
        <div className="min-w-0 flex-1">
          <b className="block truncate text-sm font-semibold text-[var(--heading)]">{digest.title}</b>
          <span className="block truncate text-xs text-[var(--muted)]">
            {digest.aiUsed ? "AI 摘要" : "按规则整理"} · {digest.items.length} 篇 · {formatRelative(digest.createdAt)}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button type="button" size="sm" variant="ghost" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
            {open ? "收起" : "展开"}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={onOpenDigests}>
            全部简报
          </Button>
        </div>
      </div>
      {open && (
        <div className="news-brief-strip-body">
          {digest.items.length > 0 ? (
            <DigestLinks items={digest.items.slice(0, 6)} />
          ) : (
            <p className="news-brief-text">{digest.content}</p>
          )}
        </div>
      )}
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
        title="无匹配资讯"
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
  const hasAIContent = Boolean(shown.summary || shown.translatedContent);

  return (
    <article id={`article-${article.id}`} className={cn("news-row", !article.read && "is-unread", expanded && "is-open")}>
      <div className="news-row-main">
        <button type="button" className="news-row-toggle focus-ring" onClick={onToggle} aria-expanded={expanded}>
          <span className="news-title-line">
            {!article.read && <span className="unread-dot news-unread-dot" aria-hidden="true" />}
            <span className={cn("news-title", expanded && "is-expanded")} title={article.translatedTitle || article.title}>
              {article.translatedTitle || article.title}
            </span>
            {article.matched && <Badge tone="info" className="shrink-0">匹配</Badge>}
          </span>
          <span className="news-meta-line">
            <SourceMark name={article.sourceName} />
            <span className="truncate">{article.sourceName}</span>
            {article.topic && (
              <>
                <span aria-hidden="true">·</span>
                <span className="news-tag">{article.topic}</span>
              </>
            )}
            <span aria-hidden="true">·</span>
            <time className="shrink-0" dateTime={article.publishedAt}>
              {formatRelative(article.publishedAt)}
            </time>
          </span>
        </button>
        <div className="news-row-actions">
          <Button
            size="icon"
            variant="ghost"
            className={article.starred ? "text-[var(--heading)]" : "text-[var(--muted)]"}
            aria-label={article.starred ? "取消收藏" : "收藏"}
            disabled={update.isPending}
            onClick={() => update.mutate({ starred: !article.starred })}
          >
            <Star size={16} fill={article.starred ? "currentColor" : "none"} />
          </Button>
          <Button size="icon" variant="ghost" className="text-[var(--muted)]" aria-label={expanded ? "收起" : "展开"} onClick={onToggle}>
            {expanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
          </Button>
        </div>
      </div>
      {expanded && (
        <div className="news-detail">
          <div className="news-detail-inner">
            <div className="space-y-1">
              {article.translatedTitle && article.translatedTitle !== article.title && (
                <p className="break-words text-sm text-[var(--muted)]">{article.title}</p>
              )}
              <p className="news-note">
                {article.sourceName} · {article.topic || "未分类"} · {formatDateTime(article.publishedAt, { seconds: false })}
              </p>
            </div>
            {detail.isPending && <Skeleton className="h-24" />}
            {detail.isError && (
              <Notice tone="danger" title="资讯详情读取失败">
                {detail.error.message}
              </Notice>
            )}
            {!detail.isPending && (
              <>
                {shown.summary && (
                  <section className="news-summary">
                    <h3 className="text-xs font-semibold text-[var(--muted)]">摘要</h3>
                    <p className="mt-1 whitespace-pre-wrap break-words text-sm leading-relaxed text-[var(--heading)]">{shown.summary}</p>
                  </section>
                )}
                {shown.translatedContent && (
                  <section>
                    <h3 className="text-xs font-semibold text-[var(--muted)]">译文</h3>
                    <p className="mt-1 whitespace-pre-wrap break-words text-sm leading-relaxed text-[var(--ink)]">{shown.translatedContent}</p>
                  </section>
                )}
                {shown.content && (
                  hasAIContent ? (
                    <details className="news-original">
                      <summary className="focus-ring">展开原文</summary>
                      <p className="mt-2 whitespace-pre-wrap break-words text-sm leading-relaxed text-[var(--muted)]">{shown.content}</p>
                    </details>
                  ) : (
                    <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-[var(--ink)]">{shown.content}</p>
                  )
                )}
                {shown.origins.length > 0 && (
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--muted)]">
                    <span>来源</span>
                    {shown.origins.map((origin, index) => {
                      const href = safeExternalURL(origin.url);
                      return (
                        <span key={`${origin.sourceId}-${index}`} className="break-all">
                          {href ? (
                            <a className="text-[var(--brand)] hover:underline" href={href} target="_blank" rel="noopener noreferrer">
                              {origin.sourceName}
                              <ExternalLink className="ml-0.5 inline" size={12} aria-hidden="true" />
                            </a>
                          ) : (
                            origin.sourceName
                          )}
                        </span>
                      );
                    })}
                  </div>
                )}
                {shown.aiStatus === "running" && <Badge>AI 分析中…</Badge>}
                {shown.aiStatus === "error" && (
                  <Notice tone="danger" title="AI 分析失败">
                    {shown.aiError}
                  </Notice>
                )}
                <div className="news-detail-actions">
                  <Button size="sm" variant="secondary" disabled={update.isPending} onClick={() => update.mutate({ read: !article.read })}>
                    {article.read ? "标为未读" : "标为已读"}
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={analyze.isPending || shown.aiStatus === "running"}
                    onClick={() => analyze.mutate()}
                  >
                    <Sparkles size={14} />
                    {analyze.isPending ? "分析中…" : shown.aiStatus === "error" ? "重试分析" : "AI 分析"}
                  </Button>
                  {externalURL && (
                    <ButtonAnchor className="ml-auto" variant="secondary" size="sm" href={externalURL} target="_blank" rel="noopener noreferrer">
                      查看原文
                      <ExternalLink size={14} />
                    </ButtonAnchor>
                  )}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </article>
  );
}
