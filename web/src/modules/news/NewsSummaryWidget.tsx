import type { Widget } from "../../shared/schema";
import { ButtonLink } from "../../components/ui/Button";
import { StatCard } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { cn } from "../../shared/cn";
import { formatDateTime } from "../../shared/format";
import { useLatestArticles, useNewsWidget } from "./queries";

const isToday = (value?: string) => !!value && new Date(value).toDateString() === new Date().toDateString();

export default function NewsSummaryWidget({ widget }: { widget: Widget }) {
  const stats = useNewsWidget(widget);
  const latest = useLatestArticles(4);
  if (stats.isPending) return <Skeleton className="h-32" />;
  if (stats.isError)
    return (
      <Notice tone="danger" title={`${widget.title}加载失败`}>
        {stats.error.message}
      </Notice>
    );
  if (stats.data.sources === 0)
    return (
      <EmptyState
        title="暂无订阅源"
        description="添加订阅源后，这里会显示未读数和最新资讯。"
        action={
          <ButtonLink size="sm" to="/news?tab=sources">
            添加订阅源
          </ButtonLink>
        }
      />
    );
  const items = latest.data?.items ?? [];
  return (
    <div className="space-y-4">
      <div className="dashboard-stats">
        <StatCard label="未读" value={stats.data.unread} />
        <StatCard label="收藏" value={stats.data.starred} accent="warning" />
      </div>
      {latest.isPending && <Skeleton className="h-20" />}
      {latest.isError && (
        <Notice tone="danger" title="最新资讯读取失败">
          {latest.error.message}
        </Notice>
      )}
      {latest.data && items.length === 0 && <p className="text-sm text-[var(--muted)]">暂无资讯，订阅源刷新后会显示在这里。</p>}
      {items.length > 0 && (
        <ul className="space-y-2" aria-label="最新资讯">
          {items.map((article) => (
            <li key={article.id} className="min-w-0">
              <p className={cn("truncate text-sm", !article.read && "font-medium")}>{article.translatedTitle || article.title}</p>
              <p className="truncate text-xs text-[var(--muted)]">
                {article.sourceName} · {formatDateTime(article.publishedAt, { seconds: false })}
              </p>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap gap-2">
        <ButtonLink size="sm" variant="secondary" to="/news">
          查看资讯
        </ButtonLink>
        <ButtonLink size="sm" variant="secondary" to="/news?tab=digests">
          {isToday(stats.data.lastDigestAt) ? "今日简报" : "简报"}
        </ButtonLink>
      </div>
    </div>
  );
}
