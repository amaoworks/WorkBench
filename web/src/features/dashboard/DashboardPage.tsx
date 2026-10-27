import { Component, Suspense, useCallback, useMemo, useState, type ComponentType, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Bell, CheckSquare2, ChartNoAxesCombined, ChevronRight, LayoutGrid, Newspaper, Wallet } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../../shared/api";
import { dashboardSchema, widgetCatalogSchema, type ConfigurableWidget, type Widget } from "../../shared/schema";
import { DashboardSignalsContext, WidgetHeaderSlotContext, type OverviewSummary } from "../../shared/dashboard";
import { widgetRegistry } from "../../modules/registry";
import { Card } from "../../components/ui/Card";
import { Button } from "../../components/ui/Button";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Badge } from "../../components/ui/Badge";
import { NotificationsOverview } from "../notifications/NotificationsSummary";
import { Checkbox } from "../../components/ui/Checkbox";
import { Select } from "../../components/ui/Select";

// The platform notifications card renders only as a 今日概况 tile, never as a full card.
const TILE_ONLY = "notifications.summary";

const widgetIcons: Record<string, ComponentType<{ size?: number }>> = {
  "investment.snapshot": Wallet,
  "investment.quotes": ChartNoAxesCombined,
  "investment.alerts": Bell,
  "notifications.summary": Bell,
  "todo.today": CheckSquare2,
  "news.brief": Newspaper,
};

const WEEKDAYS = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
const todayLabel = (date = new Date()) => `${date.getMonth() + 1} 月 ${date.getDate()} 日 ${WEEKDAYS[date.getDay()]}`;
// Order of the 今日概况 tiles and of the phrases in the headline.
const SUMMARY_ORDER = ["todo.today", "notifications.summary", "news.brief"];
const summaryRank = (id: string) => (SUMMARY_ORDER.includes(id) ? SUMMARY_ORDER.indexOf(id) : SUMMARY_ORDER.length);
const sameSymbols = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((value, index) => value === b[index]);

export default function DashboardPage() {
  const [params, setParams] = useSearchParams();
  const editing = params.get("edit") === "1";
  function setEditing(next: boolean) {
    setParams((current) => {
      const value = new URLSearchParams(current);
      if (next) value.set("edit", "1");
      else value.delete("edit");
      return value;
    }, { replace: true });
  }
  // One request carries every visible card's data; cards then poll their own routes.
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: async () => dashboardSchema.parse(await api("/api/dashboard?include=data")) });
  const catalog = useQuery({
    queryKey: ["dashboard", "catalog"],
    queryFn: async () => widgetCatalogSchema.parse(await api("/api/dashboard/widgets")),
    enabled: editing,
  });
  const [heldSymbols, setHeld] = useState<readonly string[]>([]);
  const setHeldSymbols = useCallback((symbols: readonly string[]) => setHeld((current) => (sameSymbols(current, symbols) ? current : [...symbols])), []);
  const [summaries, setSummaries] = useState<Record<string, OverviewSummary>>({});
  const setSummary = useCallback((id: string, summary: OverviewSummary | null) => {
    setSummaries((current) => {
      if (!summary) {
        if (!(id in current)) return current;
        const next = { ...current };
        delete next[id];
        return next;
      }
      return JSON.stringify(current[id]) === JSON.stringify(summary) ? current : { ...current, [id]: summary };
    });
  }, []);
  const signals = useMemo(() => ({ heldSymbols, setHeldSymbols, summaries, setSummary }), [heldSymbols, setHeldSymbols, summaries, setSummary]);
  const configured = dashboard.data?.widgets ?? [];
  const showNotifications = configured.some((widget) => widget.widgetKind === TILE_ONLY);
  const widgets = configured.filter((widget) => widget.widgetKind !== TILE_ONLY);
  // Only the first configured card may lead, preserving the user's card order.
  const lead = widgets[0]?.size === "large" ? widgets[0] : undefined;
  const rest = widgets.filter((widget) => widget !== lead);
  const tiles = Object.entries(summaries).sort(([a], [b]) => summaryRank(a) - summaryRank(b));
  const phrases = tiles.map(([, summary]) => summary.phrase).filter(Boolean);
  return (
    <div className="page overview-page">
      <header className="overview-hero">
        <h1>今天，一目了然</h1>
        <p>
          {todayLabel()}
          {phrases.length > 0 && ` · ${phrases.join("、")}`}
        </p>
        <Button className="overview-edit" variant="ghost" size="sm" aria-pressed={editing} onClick={() => setEditing(!editing)}>
          <LayoutGrid size={14} />
          {editing ? "完成" : "配置卡片"}
        </Button>
      </header>
      {editing && (
        <Card className="mb-5 p-5">
          {catalog.isPending ? (
            <Skeleton className="h-32" />
          ) : catalog.isError ? (
            <Notice tone="danger" title="配置加载失败">
              {catalog.error.message}
            </Notice>
          ) : (
            <LayoutEditor key={JSON.stringify(catalog.data)} widgets={catalog.data.widgets} />
          )}
        </Card>
      )}
      {dashboard.isPending && (
        <div className="dashboard-grid" aria-busy="true">
          <Skeleton className="dashboard-skeleton widget-large h-48" />
          <Skeleton className="dashboard-skeleton widget-medium h-48" />
        </div>
      )}
      {dashboard.isError && (
        <Notice tone="danger" title="总览加载失败">
          {dashboard.error.message}
        </Notice>
      )}
      {dashboard.data && configured.length === 0 && (
        <Card>
          <EmptyState title="暂无卡片" description="可在配置总览中选择卡片，或前往设置启用业务。" />
        </Card>
      )}
      <DashboardSignalsContext.Provider value={signals}>
        {showNotifications && <NotificationsOverview />}
        {(widgets.length > 0 || tiles.length > 0) && (
          <div className="overview-today" data-has-tiles={tiles.length > 0} data-has-lead={Boolean(lead)}>
            {tiles.length > 0 && (
              <ul className="overview-tiles" aria-label="今日概况">
                {tiles.map(([id, summary]) => (
                  <SummaryTile key={id} id={id} summary={summary} />
                ))}
              </ul>
            )}
            {lead && (
              <div className="dashboard-grid overview-lead">
                <DashboardCard widget={lead} />
              </div>
            )}
          </div>
        )}
        <div className="dashboard-grid">
          {rest.map((widget) => (
            <DashboardCard key={widget.id} widget={widget} />
          ))}
        </div>
        {widgets.length > 0 && (
          <Card className="dashboard-all-empty">
            <EmptyState title="暂时没有需要关注的内容" description="连接券商、添加待办或订阅源后，相关卡片会自动出现。" />
          </Card>
        )}
      </DashboardSignalsContext.Provider>
    </div>
  );
}

function SummaryTile({ id, summary }: { id: string; summary: OverviewSummary }) {
  const Icon = widgetIcons[id];
  return (
    <li>
      <Link to={summary.to} className="overview-tile focus-ring">
        <span className="overview-tile-head">
          {Icon && (
            <span className="overview-tile-icon" aria-hidden="true">
              <Icon size={15} />
            </span>
          )}
          <span className="flex-1">{summary.title}</span>
          <ChevronRight size={15} className="muted-icon" aria-hidden="true" />
        </span>
        <span className="overview-tile-value">
          <b>{summary.value}</b>
          <span>{summary.unit}</span>
        </span>
        {summary.detail && <span className="overview-tile-detail">{summary.detail}</span>}
      </Link>
    </li>
  );
}

function DashboardCard({ widget }: { widget: Widget }) {
  const [slot, setSlot] = useState<HTMLDivElement | null>(null);
  const Renderer = widgetRegistry[widget.widgetKind];
  const Icon = widgetIcons[widget.widgetKind];
  return (
    <section className={`dashboard-widget widget-${widget.size}`} aria-label={widget.title}>
      <header className="dashboard-widget-head">
        <h2>
          {Icon && <Icon size={16} />}
          {widget.title}
        </h2>
        <div className="dashboard-widget-meta" ref={setSlot} />
      </header>
      {/* Renderers return null when they have nothing to show; CSS then hides the whole card. */}
      <div className="widget-body">
        <WidgetHeaderSlotContext.Provider value={slot}>
          <WidgetBoundary title={widget.title}>
            <Suspense fallback={<Skeleton className="h-32" />}>
              {Renderer ? (
                <Renderer widget={widget} />
              ) : (
                <Notice tone="warning" title="组件不可用">
                  {widget.widgetKind}
                </Notice>
              )}
            </Suspense>
          </WidgetBoundary>
        </WidgetHeaderSlotContext.Provider>
      </div>
    </section>
  );
}

const SIZE_OPTIONS = [
  { value: "small", label: "小" },
  { value: "medium", label: "中" },
  { value: "large", label: "大" },
];

function LayoutEditor({ widgets }: { widgets: ConfigurableWidget[] }) {
  const [items, setItems] = useState(widgets);
  const client = useQueryClient();
  const refresh = async () => {
    await client.invalidateQueries({ queryKey: ["dashboard"] });
  };
  const save = useMutation({
    mutationFn: () =>
      api("/api/dashboard/layout", {
        method: "PUT",
        body: JSON.stringify({ items: items.map((item, order) => ({ id: item.id, visible: item.visible, size: item.size, order })) }),
      }),
    onSuccess: async () => {
      await refresh();
      toast.success("总览配置已保存");
    },
    onError: (err) => toast.error(err.message),
  });
  const reset = useMutation({
    mutationFn: () => api("/api/dashboard/layout", { method: "DELETE", body: "{}" }),
    onSuccess: async () => {
      await refresh();
      toast.success("已恢复默认布局");
    },
    onError: (err) => toast.error(err.message),
  });
  const busy = save.isPending || reset.isPending;
  function move(index: number, delta: number) {
    setItems((current) => {
      const next = [...current];
      const item = next.splice(index, 1)[0];
      if (item) next.splice(index + delta, 0, item);
      return next;
    });
  }
  return (
    <fieldset disabled={busy}>
      <legend className="mb-3 font-medium">总览卡片</legend>
      {items.map((item, index) => (
        <div key={item.id} className="layout-row">
          <div className="flex flex-1 items-center gap-2">
            <Checkbox
              checked={item.visible}
              onChange={(visible) => setItems(items.map((value) => (value.id === item.id ? { ...value, visible } : value)))}
            >
              {item.title}
            </Checkbox>
            {!item.enabled && <Badge>业务已停用</Badge>}
          </div>
          {item.widgetKind === TILE_ONLY ? <span className="layout-size layout-size-tile">概况</span> : <Select
            className="layout-size"
            aria-label={`${item.title}尺寸`}
            value={item.size}
            options={SIZE_OPTIONS}
            onValueChange={(size) =>
              setItems(items.map((value) => (value.id === item.id ? { ...value, size: size as ConfigurableWidget["size"] } : value)))
            }
          />}
          <Button variant="ghost" disabled={index === 0} aria-label={`${item.title}上移`} onClick={() => move(index, -1)}>
            上移
          </Button>
          <Button variant="ghost" disabled={index === items.length - 1} aria-label={`${item.title}下移`} onClick={() => move(index, 1)}>
            下移
          </Button>
        </div>
      ))}
      <div className="mt-4 flex gap-3">
        <Button onClick={() => save.mutate()} disabled={busy}>
          {save.isPending ? "保存中…" : "保存"}
        </Button>
        <Button variant="secondary" onClick={() => reset.mutate()} disabled={busy}>
          恢复默认
        </Button>
      </div>
    </fieldset>
  );
}

class WidgetBoundary extends Component<{ title: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  render() {
    return this.state.failed ? (
      <Notice tone="danger" title={`${this.props.title}显示失败`}>
        刷新后重试。
      </Notice>
    ) : (
      this.props.children
    );
  }
}
