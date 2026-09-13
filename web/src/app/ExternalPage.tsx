import { useEffect, useMemo, useRef } from "react";
import { useParams } from "react-router-dom";
import { useModules } from "./queries";
import { useSettings } from "../features/settings/queries";

export function ExternalPage() {
  const { moduleId = "", pageName = "" } = useParams();
  const modules = useModules();
  const settings = useSettings();
  const theme = settings.data?.appearance.theme === "dark" || (settings.data?.appearance.theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
  const module = modules.data?.items.find((item) => item.id === moduleId);
  const page = useMemo(() => {
    if (!module || module.kind !== "external") return undefined;
    const key = `${moduleId}.${pageName}`;
    return module.pages?.find((item) => item.key === key) ?? module.navigation.find((item) => item.pageKey === key);
  }, [module, moduleId, pageName]);
  if (modules.isPending) return <p role="status">正在加载模块…</p>;
  if (!module || module.kind !== "external") return <UnknownExternal />;
  if (!module.enabled) return <p role="status">业务已停用。可在设置中继续修改连接和配置。</p>;
  if (module.pending) return <p role="status">正在启用，等待服务确认…</p>;
  if (module.health === "offline" || module.health === "incompatible" || module.health === "unknown") {
    return <p role="alert">{statusCopy(module.health, module.lastError)}</p>;
  }
  const entry = "entry" in (page ?? {}) ? (page as { entry?: string }).entry ?? "/ui/index.html" : "/ui/index.html";
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
  return <iframe ref={frame} className="external-module-frame" title={title} src={src} onLoad={() => {
    frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }} />;
}

export function ExternalSettingsFrame({ moduleId, entry, theme }: { moduleId: string; entry: string; theme: string }) {
  const path = entry.replace(/^\/settings\//, "");
  const src = `/modules/${moduleId}/settings/${path}?moduleId=${encodeURIComponent(moduleId)}&apiPrefix=${encodeURIComponent(`/api/modules/${moduleId}/proxy`)}&configPrefix=${encodeURIComponent(`/api/modules/${moduleId}/config`)}&theme=${theme}`;
  return <ExternalFrame src={src} title="模块设置" theme={theme} />;
}

function statusCopy(health?: string, lastError?: string) {
  if (health === "incompatible") return lastError || "模块协议不兼容，业务入口已关闭。";
  if (health === "offline") return lastError || "服务不可达。业务入口已关闭，设置仍可修改连接。";
  return lastError || "正在确认服务状态…";
}

function UnknownExternal() {
  return <div className="py-20 text-center"><h1 className="text-xl font-semibold">页面不可用</h1><p className="mt-2 text-[var(--muted)]">外部模块未接入或当前不可用。</p></div>;
}
