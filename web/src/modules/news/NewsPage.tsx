import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { cn } from "../../shared/cn";
import { PageHeader } from "../../components/ui/PageHeader";
import { Notice } from "../../components/ui/Notice";
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

  const summary = stats.data
    ? [
        `来自 ${stats.data.sources} 个订阅源`,
        stats.data.unread > 0 ? `${stats.data.unread} 篇未读` : stats.data.articles > 0 ? "全部已读" : null,
        `今日 AI ${stats.data.aiCallsToday} / ${stats.data.aiLimit}`,
      ]
        .filter(Boolean)
        .join(" · ")
    : undefined;

  return (
    <div className="page news-page">
      <PageHeader title="资讯" description={summary} />
      <nav className="rtabs news-tabs" aria-label="资讯视图">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            type="button"
            className={cn("rtab sm focus-ring", active === tab.id && "is-active")}
            aria-pressed={active === tab.id}
            onClick={() => selectTab(tab.id)}
          >
            {tab.label}
          </button>
        ))}
      </nav>
      {stats.isError && (
        <Notice className="mb-4" tone="danger" title="资讯统计读取失败">
          {stats.error.message}
        </Notice>
      )}
      {stats.data?.lastDigestError && (
        <Notice className="mb-4" tone="danger" title="最近简报失败">
          {stats.data.lastDigestError}
        </Notice>
      )}
      {active === "articles" && <ArticlesTab onAddSource={() => selectTab("sources", { add: true })} />}
      {active === "sources" && <SourcesTab startAdding={addSource} />}
      {active === "rules" && <RulesTab />}
      {active === "digests" && <DigestsTab />}
    </div>
  );
}
