import { useSearchParams } from "react-router-dom";
import { Card } from "../../components/ui/Card";
import { Button } from "../../components/ui/Button";
import { PageHeader } from "../../components/ui/PageHeader";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import ArticlesTab from "./ArticlesTab";
import DigestsTab from "./DigestsTab";
import RulesTab from "./RulesTab";
import SourcesTab from "./SourcesTab";
import { useNewsStats } from "./queries";

const tabs = [
  { id: "articles", label: "资讯" },
  { id: "sources", label: "订阅源" },
  { id: "rules", label: "关注规则" },
  { id: "digests", label: "简报" },
] as const;
type Tab = (typeof tabs)[number]["id"];

export default function NewsPage() {
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab");
  const active: Tab = tabs.some((tab) => tab.id === requested) ? (requested as Tab) : "articles";
  const stats = useNewsStats();
  function selectTab(tab: Tab) {
    const next = new URLSearchParams(params);
    if (tab === "articles") next.delete("tab");
    else next.set("tab", tab);
    setParams(next, { replace: true });
  }

  return (
    <div className="page news-page">
      <PageHeader
        title="资讯"
        action={
          <div className="flex max-w-full flex-wrap gap-2" aria-label="资讯视图">
            {tabs.map((tab) => (
              <Button
                key={tab.id}
                size="sm"
                variant={active === tab.id ? "primary" : "secondary"}
                aria-pressed={active === tab.id}
                onClick={() => selectTab(tab.id)}
              >
                {tab.label}
              </Button>
            ))}
          </div>
        }
      />
      {stats.isPending && <Skeleton className="mb-4 h-16" />}
      {stats.isError && (
        <Notice className="mb-4" tone="danger" title="资讯统计读取失败">
          {stats.error.message}
        </Notice>
      )}
      {stats.data && (
        <>
          <div className="mb-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
            <Metric label="订阅源" value={stats.data.sources} />
            <Metric label="资讯" value={stats.data.articles} />
            <Metric label="未读" value={stats.data.unread} />
            <Metric label="收藏" value={stats.data.starred} />
            <Metric label="今日 AI" value={stats.data.aiCallsToday} />
            <Metric label="AI 上限" value={stats.data.aiLimit} />
          </div>
          {stats.data.lastDigestError && (
            <Notice className="mb-4" tone="danger" title="最近简报失败">
              {stats.data.lastDigestError}
            </Notice>
          )}
        </>
      )}
      {active === "articles" && <ArticlesTab />}
      {active === "sources" && <SourcesTab />}
      {active === "rules" && <RulesTab />}
      {active === "digests" && <DigestsTab />}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <Card className="min-w-0 px-3 py-2">
      <p className="truncate text-xs text-[var(--muted)]">{label}</p>
      <p className="mt-1 text-lg font-semibold tabular-nums">{value}</p>
    </Card>
  );
}
