import { useState } from "react";
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
  { id: "articles", label: "文章" },
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
  const [addSource, setAddSource] = useState(false);
  function selectTab(tab: Tab, { add = false }: { add?: boolean } = {}) {
    setAddSource(add);
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
          {stats.data.sources === 0 && stats.data.articles === 0 ? (
            <p className="mb-4 text-xs text-[var(--muted)]">
              今日 AI {stats.data.aiCallsToday} / {stats.data.aiLimit}
            </p>
          ) : (
            <div className="mb-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
              <Metric label="订阅源" value={stats.data.sources} />
              <Metric label="文章" value={stats.data.articles} />
              <Metric label="未读" value={stats.data.unread} />
              <Metric label="收藏" value={stats.data.starred} />
              <Metric label="今日 AI" value={stats.data.aiCallsToday} />
              <Metric label="AI 上限" value={stats.data.aiLimit} />
            </div>
          )}
          {stats.data.lastDigestError && (
            <Notice className="mb-4" tone="danger" title="最近简报失败">
              {stats.data.lastDigestError}
            </Notice>
          )}
        </>
      )}
      {active === "articles" && <ArticlesTab onAddSource={() => selectTab("sources", { add: true })} />}
      {active === "sources" && <SourcesTab startAdding={addSource} />}
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
