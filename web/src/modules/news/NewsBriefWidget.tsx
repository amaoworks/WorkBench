import type { Widget } from "../../shared/schema";
import { ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { WidgetHeader } from "../../components/dashboard/WidgetHeader";
import { useDashboardSignals, useOverviewSummary } from "../../shared/dashboard";
import { cn } from "../../shared/cn";
import { formatRelative } from "../../shared/format";
import { useNewsBrief } from "./queries";
import type { NewsBrief } from "./schema";

type Article = NewsBrief["latest"][number];

function ArticleList({ items, label, tags }: { items: Article[]; label: string; tags?: boolean }) {
  return (
    <ul className="ov-news" aria-label={label}>
      {items.map((article) => (
        <li key={article.id}>
          {tags && article.symbols?.slice(0, 2).map((symbol) => <span key={symbol} className="ov-tag">{symbol}</span>)}
          <a className={cn("ov-news-title", !article.read && "is-unread")} href={article.url} target="_blank" rel="noreferrer" title={article.title}>
            {article.title}
          </a>
          <span className="ov-note ov-nowrap">{formatRelative(article.publishedAt)}</span>
        </li>
      ))}
    </ul>
  );
}

export default function NewsBriefWidget({ widget }: { widget: Widget }) {
  const { heldSymbols } = useDashboardSignals();
  const brief = useNewsBrief(widget, heldSymbols);
  const summaryData = brief.data;
  useOverviewSummary(
    widget.widgetKind,
    summaryData && summaryData.sources > 0
      ? {
          title: "资讯",
          value: summaryData.unread,
          unit: "篇未读文章",
          phrase: summaryData.unread > 0 ? `${summaryData.unread} 篇未读文章` : undefined,
          detail: summaryData.digest ? "AI 简报已生成" : `${summaryData.sources} 个订阅源`,
          to: "/news",
        }
      : null,
  );
  if (brief.isPending) return <Skeleton className="h-32" />;
  if (brief.isError)
    return (
      <Notice tone="danger" title="资讯加载失败">
        {brief.error.message}
      </Notice>
    );
  const data = brief.data;
  // Without any source the brief has nothing to say; the card disappears instead of nagging.
  if (data.sources === 0) return null;
  const digest = data.digest;
  const left = digest ? null : data.latest;
  const right = data.related.length > 0 ? data.related : digest ? data.latest : [];
  if (!digest && data.latest.length === 0) return null;
  return (
    <>
      <WidgetHeader>
        <span className="ov-sub">
          {data.sources} 个订阅源 · 未读 {data.unread}
        </span>
        <ButtonLink size="sm" variant="secondary" to="/news" className="ov-hide-sm">
          查看资讯
        </ButtonLink>
      </WidgetHeader>
      <div className={cn("ov-brief", right.length > 0 && (digest || left) && "has-two")}>
        <div className="min-w-0">
          {digest ? (
            <>
              <p className="ov-label">
                简报 · {digest.title} · {formatRelative(digest.createdAt)}
                {digest.aiUsed ? "（AI 摘要）" : ""}
              </p>
              {digest.items.length > 0 ? (
                <ul className="ov-bullets">
                  {digest.items.map((item) => (
                    <li key={item.id}>
                      <a href={item.url} target="_blank" rel="noreferrer">
                        {item.title}
                      </a>
                      <span className="ov-note"> · {item.sourceName}</span>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="ov-digest-text">{digest.content}</p>
              )}
              <ButtonLink size="sm" variant="ghost" to="/news?tab=digests" className="mt-1">
                查看完整简报
              </ButtonLink>
            </>
          ) : (
            left && (
              <>
                <p className="ov-label">最新资讯</p>
                <ArticleList items={left} label="最新资讯" />
              </>
            )
          )}
        </div>
        {right.length > 0 && (digest || left) && (
          <div className="min-w-0">
            <p className="ov-label">{data.related.length > 0 ? "与持仓相关" : "最新资讯"}</p>
            <ArticleList items={right} label={data.related.length > 0 ? "与持仓相关" : "最新资讯"} tags={data.related.length > 0} />
          </div>
        )}
      </div>
    </>
  );
}
