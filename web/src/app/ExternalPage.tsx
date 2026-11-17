import { useMemo } from "react";
import { useParams } from "react-router-dom";
import { useModules } from "../shared/modules";
import { useSettings } from "../features/settings/queries";
import { ExternalFrame } from "../components/ExternalFrame";
import { Notice } from "../components/ui/Notice";
import { Skeleton } from "../components/ui/States";
import { ButtonLink } from "../components/ui/Button";

export function ExternalPage() {
  const { moduleId = "", pageName = "" } = useParams();
  const modules = useModules();
  const settings = useSettings();
  const theme =
    settings.data?.appearance.theme === "dark" ||
    (settings.data?.appearance.theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)
      ? "dark"
      : "light";
  const module = modules.data?.items.find((item) => item.id === moduleId);
  const page = useMemo(() => {
    if (!module || module.kind !== "external") return undefined;
    const key = `${moduleId}.${pageName}`;
    return module.pages?.find((item) => item.key === key) ?? module.navigation.find((item) => item.pageKey === key);
  }, [module, moduleId, pageName]);
  if (modules.isPending) return <Skeleton className="h-64" />;
  if (modules.isError)
    return (
      <Notice tone="danger" title="业务加载失败">
        {modules.error.message}
      </Notice>
    );
  if (!module || module.kind !== "external") return <UnknownExternal />;
  if (!module.enabled) return <Notice title="业务已停用" action={<ButtonLink to="/admin/settings?tab=modules">业务管理</ButtonLink>} />;
  if (module.pending) return <Notice title="正在启用" />;
  if (module.health === "offline" || module.health === "incompatible" || module.health === "unknown") {
    return (
      <Notice tone="danger" title="业务不可用">
        {statusCopy(module.health, module.lastError)}
      </Notice>
    );
  }
  const entry = page && "entry" in page && typeof page.entry === "string" && page.entry ? page.entry : "/ui/index.html";
  const path = entry.replace(/^\/ui\//, "");
  const query = new URLSearchParams({
    moduleId,
    apiPrefix: `/api/modules/${moduleId}/proxy`,
    configPrefix: `/api/modules/${moduleId}/config`,
    theme,
  });
  const src = `/modules/${moduleId}/ui/${path}?${query}`;
  return <ExternalFrame src={src} title={module.name} theme={theme} />;
}

function statusCopy(health?: string, lastError?: string) {
  if (health === "incompatible") return lastError || "业务协议不兼容，入口已关闭。";
  if (health === "offline") return lastError || "服务不可达。业务入口已关闭，设置仍可修改连接。";
  return lastError || "正在确认服务状态…";
}

function UnknownExternal() {
  return (
    <div className="py-20 text-center">
      <h1 className="text-xl font-semibold">页面不可用</h1>
      <ButtonLink className="mt-6" variant="secondary" to="/">返回总览</ButtonLink>
    </div>
  );
}
