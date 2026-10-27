import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { api, apiResponse } from "../../shared/api";
import { moduleSchema, type ExternalModule, type WorkbenchModule } from "../../shared/schema";
import { useModules } from "../../app/queries";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Skeleton } from "../../components/ui/States";
import { ModuleSettingsDialog } from "./ModuleSettingsDialog";
import { Badge } from "../../components/ui/Badge";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { clearModuleQueries } from "../../shared/queryKeys";

export function ModulesPanel() {
  const modules = useModules();
  const client = useQueryClient();
  const heading = useRef<HTMLHeadingElement>(null);
  const refresh = async () => {
    await Promise.all([
      client.invalidateQueries({ queryKey: ["modules"] }),
      client.invalidateQueries({ queryKey: ["dashboard"] }),
      client.invalidateQueries({ queryKey: ["ai"] }),
    ]);
  };
  const toggle = useMutation({
    mutationFn: async ({ id, enabled }: { id: string; enabled: boolean }) => {
      const response = await apiResponse(`/api/modules/${id}/enabled`, { method: "PUT", body: JSON.stringify({ enabled }) });
      return { status: response.status, body: moduleSchema.parse(await response.json()) };
    },
    onSuccess: async ({ status, body }, { id, enabled }) => {
      await clearModuleQueries(client, id);
      await refresh();
      if (status === 202 || body.pending) {
        toast.message(enabled ? "正在启用" : "业务入口已关闭，等待服务确认停用");
        return;
      }
      toast.success(enabled ? "业务已启用" : "业务已停用");
    },
    onError: async (err) => {
      await refresh();
      toast.error(err.message);
    },
  });
  if (modules.isPending) return <Skeleton className="h-32" />;
  if (modules.isError)
    return (
      <Notice tone="danger" title="业务加载失败">
        {modules.error.message}
      </Notice>
    );
  return (
    <div className="studio-form">
      {/* The settings tab already says 业务; keep the heading for structure and as a focus fallback only. */}
      <h3 ref={heading} tabIndex={-1} className="sr-only">
        业务
      </h3>
      {modules.data.items.map((module) => (
        <div key={module.id} className="module-row">
          <div>
            <h3>{module.name}</h3>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-[var(--muted)]">
              <Badge tone={module.lastError ? "danger" : module.pending ? "warning" : module.observedEnabled ? "info" : "neutral"}>
                {moduleStatus(module)}
              </Badge>
            </div>
            {module.lastError && (
              <Notice tone="danger" title="业务运行异常" className="mt-2">
                {module.lastError}
              </Notice>
            )}
            {module.kind === "external" && module.connectionNote && <p className="text-sm text-[var(--muted)]">{module.connectionNote}</p>}
          </div>
          <div className="module-actions">
            <ModuleSettingsDialog module={module} />
            {module.kind === "external" && <ExternalActions module={module} onChange={refresh} fallbackFocus={() => heading.current} />}
            {module.kind === "builtin" && module.lastError && (
              <Button
                variant="secondary"
                disabled={toggle.isPending}
                onClick={() => toggle.mutate({ id: module.id, enabled: module.enabled })}
              >
                重试
              </Button>
            )}
            <Switch
              label={module.name}
              checked={module.enabled}
              disabled={toggle.isPending}
              onChange={(enabled) => toggle.mutate({ id: module.id, enabled })}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

function moduleStatus(module: WorkbenchModule) {
  if (module.kind === "builtin" && module.lastError) return module.enabled ? "启用失败 · 业务入口已关闭" : "业务入口已关闭 · 停用未完成";
  if (module.kind === "builtin" && module.pending) return module.enabled ? "正在启用" : "正在停用";
  if (module.kind !== "external") return module.observedEnabled ? "已启用" : "已停用";
  if (module.pending && module.enabled) return "正在启用";
  if (module.pending && !module.enabled) return "业务入口已关闭，等待服务确认停用";
  if (module.health === "offline") return "已停用 · 服务不可达";
  if (module.health === "incompatible") return "协议不兼容";
  return module.enabled ? "已启用" : "已停用";
}

function ExternalActions({
  module,
  onChange,
  fallbackFocus,
}: {
  module: ExternalModule;
  onChange: () => Promise<void>;
  fallbackFocus: () => HTMLElement | null;
}) {
  const [connectionOpen, setConnectionOpen] = useState(false);
  const refresh = useMutation({
    mutationFn: () => api(`/api/modules/${module.id}/refresh`, { method: "POST", body: "{}" }),
    onSuccess: async () => {
      toast.success("业务信息已刷新");
      await onChange();
    },
    onError: (err) => toast.error(err.message),
  });
  const unregister = useMutation({
    mutationFn: (id: string) => apiResponse(`/api/modules/external/${id}`, { method: "DELETE" }),
    onSuccess: async () => {
      await onChange();
      toast.success("接入已解除");
    },
    onError: (err) => toast.error(err.message),
  });
  const deletion = useConfirmDelete<ExternalModule>({
    title: "解除外部业务",
    description: (target) => `确定解除“${target.name}”的接入吗？远端业务数据将保留。`,
    onConfirm: (target) => unregister.mutateAsync(target.id),
    fallbackFocus,
  });
  return (
    <>
      <Button variant="secondary" onClick={() => refresh.mutate()} disabled={refresh.isPending}>
        刷新
      </Button>
      <Button variant="secondary" onClick={() => setConnectionOpen((value) => !value)} disabled={module.enabled}>
        连接
      </Button>
      <Button
        variant="danger"
        onClick={(event) => deletion.request(module, event.currentTarget)}
        disabled={module.enabled || unregister.isPending}
      >
        解除接入
      </Button>
      {connectionOpen && (
        <ConnectionForm
          module={module}
          onSaved={async () => {
            setConnectionOpen(false);
            await onChange();
          }}
        />
      )}
      <ConfirmDialog {...deletion.dialogProps} />
    </>
  );
}

function ConnectionForm({ module, onSaved }: { module: ExternalModule; onSaved: () => Promise<void> }) {
  const [baseUrl, setBaseUrl] = useState(module.baseUrl ?? "");
  const [serviceToken, setServiceToken] = useState("");
  const [allowNonLocal, setAllowNonLocal] = useState(Boolean(module.allowNonLocal));
  const save = useMutation({
    mutationFn: () =>
      api(`/api/modules/${module.id}/connection`, { method: "PUT", body: JSON.stringify({ baseUrl, serviceToken, allowNonLocal }) }),
    onSuccess: async () => {
      toast.success("连接已更新");
      await onSaved();
    },
    onError: (err) => toast.error(err.message),
  });
  return (
    <form
      className="external-connect"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <Field label="服务地址">
        <input value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} required />
      </Field>
      <Field label="服务凭据" meta={module.hasServiceToken ? <Badge>已保存</Badge> : undefined}>
        <input
          type="password"
          autoComplete="new-password"
          value={serviceToken}
          onChange={(event) => setServiceToken(event.target.value)}
          placeholder={module.hasServiceToken ? "更换地址时必填" : ""}
        />
      </Field>
      <div className="flex items-center justify-between gap-3 text-sm">
        <span>允许非本机地址</span>
        <Switch label="允许非本机地址" checked={allowNonLocal} onChange={setAllowNonLocal} />
      </div>
      <Button type="submit" disabled={save.isPending}>
        {save.isPending ? "保存中…" : "保存"}
      </Button>
    </form>
  );
}
