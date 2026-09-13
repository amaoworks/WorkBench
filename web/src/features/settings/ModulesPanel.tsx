import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";
import { api, apiResponse } from "../../shared/api";
import type { ExternalModule, WorkbenchModule } from "../../shared/schema";
import { useModules } from "../../app/queries";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Skeleton } from "../../components/ui/States";
import { ModuleSettingsDialog } from "./ModuleSettingsDialog";

export function ModulesPanel() {
  const modules = useModules();
  const client = useQueryClient();
  const refresh = async () => {
    await Promise.all([client.invalidateQueries({ queryKey: ["modules"] }), client.invalidateQueries({ queryKey: ["dashboard"] }), client.invalidateQueries({ queryKey: ["ai"] })]);
  };
  const toggle = useMutation({
    mutationFn: async ({ id, enabled }: { id: string; enabled: boolean }) => {
      const response = await apiResponse(`/api/modules/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) });
      return { status: response.status, body: await response.json() as WorkbenchModule };
    },
    onSuccess: async ({ status, body }, { id, enabled }) => {
      await client.cancelQueries();
      client.removeQueries({ queryKey: [id] });
      client.removeQueries({ queryKey: ["widget"] });
      await refresh();
      if (status === 202 || body.pending) {
        toast.message(enabled ? "正在启用" : "业务入口已关闭，等待服务确认停用");
        return;
      }
      toast.success(enabled ? "业务已启用" : "业务已停用，历史数据已保留");
    }, onError: (err) => toast.error(err.message)
  });
  if (modules.isPending) return <Skeleton className="h-32" />;
  if (modules.isError) return <p role="alert">模块加载失败：{modules.error.message}</p>;
  return <div className="studio-form">
    <div className="control-heading"><div><h3>业务模块</h3><p>停用后隐藏页面和总览，数据会保留。外部模块可在不停用宿主的情况下接入。</p></div></div>
    <AttachForm onAttached={refresh} />
    {modules.data.items.map((module) => <div key={module.id} className="module-row">
      <div>
        <h3>{module.name}</h3>
        <p className="text-sm text-[var(--muted)]">版本 {module.version} · {moduleStatus(module)}</p>
        {module.kind === "external" && module.lastError && <p className="text-sm text-[var(--danger)]" role="status">{module.lastError}</p>}
        {module.kind === "external" && module.connectionNote && <p className="text-sm text-[var(--muted)]">{module.connectionNote}</p>}
      </div>
      <div className="module-actions">
        <ModuleSettingsDialog module={module} />
        {module.kind === "external" && <ExternalActions module={module} onChange={refresh} />}
        <Button variant="secondary" role="switch" aria-label={module.name} aria-checked={module.enabled} disabled={toggle.isPending} onClick={() => toggle.mutate({ id: module.id, enabled: !module.enabled })}>{module.enabled ? "停用" : "启用"}</Button>
      </div>
    </div>)}
  </div>;
}

function moduleStatus(module: WorkbenchModule) {
  if (module.kind !== "external") return module.enabled ? "已启用" : "已停用";
  if (module.pending && module.enabled) return "正在启用";
  if (module.pending && !module.enabled) return "业务入口已关闭，等待服务确认停用";
  if (module.health === "offline") return "已停用 · 服务不可达";
  if (module.health === "incompatible") return "协议不兼容";
  return module.enabled ? "已启用" : "已停用";
}

function AttachForm({ onAttached }: { onAttached: () => Promise<void> }) {
  const [baseUrl, setBaseUrl] = useState("");
  const [serviceToken, setServiceToken] = useState("");
  const [allowNonLocal, setAllowNonLocal] = useState(false);
  const attach = useMutation({
    mutationFn: () => api("/api/modules/external", { method: "POST", body: JSON.stringify({ baseUrl, serviceToken, allowNonLocal }) }),
    onSuccess: async () => { setBaseUrl(""); setServiceToken(""); toast.success("已接入外部模块"); await onAttached(); },
    onError: (err) => toast.error(err.message)
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    attach.mutate();
  }
  return <form onSubmit={submit} className="external-attach">
    <h3>接入外部服务</h3>
    <label className="studio-field"><span>服务地址</span><input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="http://127.0.0.1:8091" required /></label>
    <label className="studio-field"><span>服务凭据</span><input type="password" autoComplete="new-password" value={serviceToken} onChange={(event) => setServiceToken(event.target.value)} required /></label>
    <label className="check-option"><input type="checkbox" checked={allowNonLocal} onChange={(event) => setAllowNonLocal(event.target.checked)} />允许非本机地址</label>
    <Button type="submit" disabled={attach.isPending}>{attach.isPending ? "正在接入…" : "接入"}</Button>
  </form>;
}

function ExternalActions({ module, onChange }: { module: ExternalModule; onChange: () => Promise<void> }) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const refresh = useMutation({
    mutationFn: () => api(`/api/modules/${module.id}/refresh`, { method: "POST", body: "{}" }),
    onSuccess: async () => { toast.success("已刷新模块描述"); await onChange(); },
    onError: (err) => toast.error(err.message)
  });
  const unregister = useMutation({
    mutationFn: () => apiResponse(`/api/modules/external/${module.id}`, { method: "DELETE" }),
    onSuccess: async () => { setConfirmOpen(false); toast.success("已解除接入，远端数据未删除"); await onChange(); },
    onError: (err) => toast.error(err.message)
  });
  return <>
    <Button variant="secondary" onClick={() => refresh.mutate()} disabled={refresh.isPending}>刷新</Button>
    <Button variant="secondary" onClick={() => setConnectionOpen((value) => !value)} disabled={module.enabled}>连接</Button>
    <Button variant="danger" onClick={() => setConfirmOpen(true)} disabled={module.enabled || unregister.isPending}>解除接入</Button>
    {connectionOpen && <ConnectionForm module={module} onSaved={async () => { setConnectionOpen(false); await onChange(); }} />}
    <ConfirmDialog open={confirmOpen} title="解除外部模块" description="将删除本机连接和目录，不会删除外部服务中的业务数据。" busy={unregister.isPending} onCancel={() => setConfirmOpen(false)} onConfirm={() => unregister.mutate()} onClosed={() => setConfirmOpen(false)} />
  </>;
}

function ConnectionForm({ module, onSaved }: { module: ExternalModule; onSaved: () => Promise<void> }) {
  const [baseUrl, setBaseUrl] = useState(module.baseUrl ?? "");
  const [serviceToken, setServiceToken] = useState("");
  const [allowNonLocal, setAllowNonLocal] = useState(Boolean(module.allowNonLocal));
  const save = useMutation({
    mutationFn: () => api(`/api/modules/${module.id}/connection`, { method: "PUT", body: JSON.stringify({ baseUrl, serviceToken, allowNonLocal }) }),
    onSuccess: async () => { toast.success("连接已更新"); await onSaved(); },
    onError: (err) => toast.error(err.message)
  });
  return <form className="external-connect" onSubmit={(event) => { event.preventDefault(); save.mutate(); }}>
    <label className="studio-field"><span>服务地址</span><input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} required /></label>
    <label className="studio-field"><span>服务凭据</span><input type="password" autoComplete="new-password" value={serviceToken} onChange={(event) => setServiceToken(event.target.value)} placeholder={module.hasServiceToken ? "更换地址时必填" : ""} /></label>
    <label className="check-option"><input type="checkbox" checked={allowNonLocal} onChange={(event) => setAllowNonLocal(event.target.checked)} />允许非本机地址</label>
    <Button type="submit" disabled={save.isPending}>保存连接</Button>
  </form>;
}
