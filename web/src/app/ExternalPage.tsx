import { useEffect, useMemo, useRef } from "react";
import { useParams } from "react-router-dom";
import { useModules } from "./queries";
import { useSettings } from "../features/settings/queries";
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
  if (!module.enabled) return <Notice title="业务已停用" action={<ButtonLink to="/settings?tab=modules">打开设置</ButtonLink>} />;
  if (module.pending) return <Notice title="正在启用">等待服务确认。</Notice>;
  if (module.health === "offline" || module.health === "incompatible" || module.health === "unknown") {
    return (
      <Notice tone="danger" title="业务不可用">
        {statusCopy(module.health, module.lastError)}
      </Notice>
    );
  }
  const entry = "entry" in (page ?? {}) ? ((page as { entry?: string }).entry ?? "/ui/index.html") : "/ui/index.html";
  const path = entry.replace(/^\/ui\//, "");
  const src = `/modules/${moduleId}/ui/${path}?moduleId=${encodeURIComponent(moduleId)}&apiPrefix=${encodeURIComponent(`/api/modules/${moduleId}/proxy`)}&configPrefix=${encodeURIComponent(`/api/modules/${moduleId}/config`)}&theme=${theme}`;
  return <ExternalFrame src={src} title={module.name} theme={theme} />;
}

export function ExternalFrame({ src, title, theme }: { src: string; title: string; theme: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  useEffect(() => {
    const node = frame.current;
    if (!node?.contentWindow) return;
    node.contentWindow.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }, [theme]);
  return (
    <iframe
      ref={frame}
      className="external-module-frame"
      title={title}
      src={src}
      onLoad={() => {
        frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
      }}
    />
  );
}

export function ExternalSettingsFrame({ moduleId, entry, theme }: { moduleId: string; entry: string; theme: string }) {
  const path = entry.replace(/^\/settings\//, "");
  const src = `/modules/${moduleId}/settings/${path}?moduleId=${encodeURIComponent(moduleId)}&apiPrefix=${encodeURIComponent(`/api/modules/${moduleId}/proxy`)}&configPrefix=${encodeURIComponent(`/api/modules/${moduleId}/config`)}&theme=${theme}`;
  return <ExternalFrame src={src} title="业务设置" theme={theme} />;
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
      <p className="mt-2 text-[var(--muted)]">外部业务未接入或当前不可用。</p>
    </div>
  );
}
