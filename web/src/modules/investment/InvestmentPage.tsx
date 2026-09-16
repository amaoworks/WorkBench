import { useContext, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Expand, ExternalLink, Maximize, Minimize } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { PageHeader } from "../../components/ui/PageHeader";
import { Skeleton } from "../../components/ui/States";
import { ThemeContext, type Theme } from "../../shared/theme";
import { refreshSchwabToken, useRefreshInvestment, useSchwabSettings } from "./queries";

export default function InvestmentPage() {
  const schwab = useSchwabSettings(true);
  const refresh = useRefreshInvestment();
  const retry = useMutation({ mutationFn: refreshSchwabToken, onSettled: refresh });
  const [searchParams, setSearchParams] = useSearchParams();
  const expanded = searchParams.get("view") === "terminal";
  const terminal = useRef<HTMLElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [fullscreenError, setFullscreenError] = useState("");
  const theme = useContext(ThemeContext);
  const connected = schwab.data?.connected === true;
  const reauthorizationRequired = schwab.data?.reauthorizationRequired === true;

  useEffect(() => {
    const update = () => setFullscreen(Boolean(terminal.current && document.fullscreenElement === terminal.current));
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);

  async function toggleExpanded() {
    if (document.fullscreenElement) await document.exitFullscreen();
    const next = new URLSearchParams(searchParams);
    if (expanded) next.delete("view"); else next.set("view", "terminal");
    setSearchParams(next, { replace: true });
  }

  async function toggleFullscreen() {
    setFullscreenError("");
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await terminal.current?.requestFullscreen();
    } catch {
      setFullscreenError("浏览器未能进入全屏，可以使用页面铺满。");
    }
  }

  return <div className="page investment-page" data-expanded={expanded}>
    <div hidden={expanded}>
      <PageHeader title="投资" description="查看 Schwab 行情、持仓和订单，通过 TradingView 终端交易。" />
    </div>
    <section ref={terminal} className="investment-view" aria-label="投资终端">
      <div className="investment-toolbar">
        <span className="investment-toolbar-title">TradingView</span>
        <div className="investment-toolbar-actions">
          <Button variant="ghost" size="sm" onClick={() => { void toggleExpanded(); }} aria-pressed={expanded}>
            {expanded ? <ArrowLeft size={15} /> : <Expand size={15} />}{expanded ? "返回工作台" : "页面铺满"}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => { void toggleFullscreen(); }} disabled={!document.fullscreenEnabled} aria-pressed={fullscreen}>
            {fullscreen ? <Minimize size={15} /> : <Maximize size={15} />}{fullscreen ? "退出全屏" : "全屏"}
          </Button>
          <ButtonLink variant="ghost" size="sm" to="/investment?view=terminal" target="_blank" rel="noopener noreferrer"><ExternalLink size={15} />新窗口</ButtonLink>
        </div>
      </div>
      {fullscreenError && <p role="alert" className="investment-message text-danger">{fullscreenError}</p>}
      {schwab.isPending && <Skeleton className="m-5 h-24" />}
      {schwab.isError && <p role="alert" className="investment-message text-danger">{schwab.error.message}</p>}
      {reauthorizationRequired && <Card role="alert" className="m-5 p-5">
        <h2 className="font-medium">Schwab 授权已失效</h2>
        <p className="mt-2 text-sm text-[var(--muted)]">行情、持仓和交易连接已暂停。请重新登录 Schwab 并确认授权，完成后会自动返回投资页。</p>
        <Button className="mt-3" onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>重新授权</Button>
      </Card>}
      {connected && schwab.data?.lastError && <Card role="alert" className="m-5 p-5">
        <p className="font-medium">Schwab 连接出现问题</p>
        <p className="mt-2 text-sm text-[var(--muted)]">{schwab.data.lastError}</p>
        <Button className="mt-3" variant="secondary" disabled={retry.isPending} onClick={() => retry.mutate()}>{retry.isPending ? "重试中…" : "重试连接"}</Button>
      </Card>}
      {schwab.data && !connected && !reauthorizationRequired && <Card className="m-5 p-5">
        <p className="text-sm">尚未连接 Schwab。请先授权，账户关联页点击 Done。</p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Link className="studio-button secondary" to="/settings?tab=modules">打开设置</Link>
          {schwab.data.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && <Button variant="secondary" onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>登录 Schwab</Button>}
        </div>
      </Card>}
      {connected && <InvestmentTerminal theme={theme} />}
    </section>
  </div>;
}

function InvestmentTerminal({ theme }: { theme: Theme }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const client = useQueryClient();
  // Keep the chart document and its selected symbol/interval across theme changes.
  const [src] = useState(() => `/investment/terminal?theme=${theme}`);
  useEffect(() => {
    frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }, [theme]);
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== frame.current?.contentWindow || event.data?.type !== "workbench.schwab.status_changed") return;
      void client.invalidateQueries({ queryKey: ["investment", "schwab"] });
      void client.invalidateQueries({ queryKey: ["widget", "investment.overview"] });
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [client]);
  return <iframe ref={frame} title="TradingView 投资终端" className="investment-terminal" allowFullScreen src={src} onLoad={() => {
    frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }} />;
}
