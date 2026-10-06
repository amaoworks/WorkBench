import { Component, Suspense, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { dashboardSchema, widgetCatalogSchema, type ConfigurableWidget } from "../../shared/schema";
import { widgetRegistry } from "../../modules/registry";
import { Card } from "../../components/ui/Card";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Badge } from "../../components/ui/Badge";
import { PageHeader } from "../../components/ui/PageHeader";

export default function DashboardPage() {
  const [editing, setEditing] = useState(false);
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: async () => dashboardSchema.parse(await api("/api/dashboard")) });
  const catalog = useQuery({
    queryKey: ["dashboard", "catalog"],
    queryFn: async () => widgetCatalogSchema.parse(await api("/api/dashboard/widgets")),
    enabled: editing,
  });
  return (
    <div className="page">
      <PageHeader
        title="总览"
        action={
          <div className="flex flex-wrap gap-2">
            <Button variant="secondary" size="sm" onClick={() => setEditing(!editing)}>
              {editing ? "关闭配置" : "配置总览"}
            </Button>
            <ButtonLink variant="secondary" size="sm" to="/settings?tab=modules">
              管理业务
            </ButtonLink>
          </div>
        }
      />
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
      {dashboard.isPending && <Skeleton className="h-32" />}
      {dashboard.isError && (
        <Notice tone="danger" title="总览加载失败">
          {dashboard.error.message}
        </Notice>
      )}
      {dashboard.data?.widgets.length === 0 && (
        <Card>
          <EmptyState title="总览暂时没有卡片" description="在配置总览中选择卡片，或到设置启用业务。" />
        </Card>
      )}
      <div className="dashboard-grid">
        {dashboard.data?.widgets.map((widget) => {
          const Renderer = widgetRegistry[widget.widgetKind];
          return (
            <section key={widget.id} className={`dashboard-widget widget-${widget.size}`} aria-label={widget.title}>
              <h2 className="mb-3 font-medium">{widget.title}</h2>
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
            </section>
          );
        })}
      </div>
    </div>
  );
}

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
          <label className="flex flex-1 items-center gap-3">
            <input
              type="checkbox"
              checked={item.visible}
              onChange={(event) =>
                setItems(items.map((value) => (value.id === item.id ? { ...value, visible: event.target.checked } : value)))
              }
            />
            <span>
              {item.title}
              {!item.enabled && <Badge className="ml-2">业务已停用</Badge>}
            </span>
          </label>
          <select
            className="ui-input"
            aria-label={`${item.title}尺寸`}
            value={item.size}
            onChange={(event) =>
              setItems(
                items.map((value) => (value.id === item.id ? { ...value, size: event.target.value as ConfigurableWidget["size"] } : value)),
              )
            }
          >
            <option value="small">小</option>
            <option value="medium">中</option>
            <option value="large">大</option>
          </select>
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
