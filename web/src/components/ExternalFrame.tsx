import { useEffect, useRef } from "react";

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
  const query = new URLSearchParams({
    moduleId,
    apiPrefix: `/api/modules/${moduleId}/proxy`,
    configPrefix: `/api/modules/${moduleId}/config`,
    theme,
  }).toString();
  return <ExternalFrame src={`/modules/${moduleId}/settings/${path}?${query}`} title="业务设置" theme={theme} />;
}
