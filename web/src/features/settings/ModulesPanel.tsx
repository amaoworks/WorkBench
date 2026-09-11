import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { useModules } from "../../app/queries";
import { Button } from "../../components/ui/Button";
import { Skeleton } from "../../components/ui/States";
import { ModuleSettingsDialog } from "./ModuleSettingsDialog";

export function ModulesPanel() {
  const modules = useModules();
  const client = useQueryClient();
  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api(`/api/modules/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) }),
    onSuccess: async (_, { id, enabled }) => {
      // Cancel old requests before clearing business caches so disabled data cannot reappear.
      await client.cancelQueries();
      client.removeQueries({ queryKey: [id] });
      client.removeQueries({ queryKey: ["widget"] });
      await Promise.all([client.invalidateQueries({ queryKey: ["modules"] }), client.invalidateQueries({ queryKey: ["dashboard"] }), client.invalidateQueries({ queryKey: ["ai"] })]);
      toast.success(enabled ? "业务已启用" : "业务已停用，历史数据已保留");
    }, onError: (err) => toast.error(err.message)
  });
  if (modules.isPending) return <Skeleton className="h-32" />;
  if (modules.isError) return <p role="alert">模块加载失败：{modules.error.message}</p>;
  return <div className="studio-form"><div className="control-heading"><div><h3>业务模块</h3><p>停用后隐藏页面和总览，数据会保留。</p></div></div>
    {modules.data.items.map((module) => <div key={module.id} className="module-row"><div><h3>{module.name}</h3><p className="text-sm text-[var(--muted)]">版本 {module.version} · {module.enabled ? "已启用" : "已停用"}</p></div><div className="module-actions"><ModuleSettingsDialog module={module} /><Button variant="secondary" role="switch" aria-label={module.name} aria-checked={module.enabled} disabled={toggle.isPending} onClick={() => toggle.mutate({ id: module.id, enabled: !module.enabled })}>{module.enabled ? "停用" : "启用"}</Button></div></div>)}
  </div>;
}
