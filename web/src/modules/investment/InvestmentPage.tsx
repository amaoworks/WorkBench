import { useContext, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Expand, ExternalLink, Maximize, Minimize } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { PageHeader } from "../../components/ui/PageHeader";
import { Skeleton } from "../../components/ui/States";
import { ThemeContext, type Theme } from "../../shared/theme";
import { refreshSchwabToken, useInvestmentView, useRefreshInvestment, useSchwabSettings } from "./queries";
import { APIError } from "../../shared/api";
import { moduleQueryKey } from "../../shared/queryKeys";
import type { InvestmentView } from "./schema";
import { InvestmentMonitor } from "./InvestmentMonitor";

export default function InvestmentPage() {
  const viewSettings = useInvestmentView();
  if (viewSettings.query.isPending) return <Skeleton className="h-24" />;
  if (viewSettings.query.isError) return (
    <Notice tone="danger" title="投资设置加载失败" action={<Button onClick={() => viewSettings.query.refetch()}>重新加载</Button>}>
      {viewSettings.query.error.message}
    </Notice>
  );
  return <InvestmentContent viewSettings={viewSettings} />;
}

function InvestmentContent({ viewSettings }: { viewSettings: ReturnType<typeof useInvestmentView> }) {
  const schwab = useSchwabSettings(true);
  const refresh = useRefreshInvestment();
  const retry = useMutation({ mutationFn: refreshSchwabToken, onSettled: refresh });
  const [searchParams, setSearchParams] = useSearchParams();
  const restoredView = useRef(false);
  const expanded = searchParams.get("view") === "terminal";
  const requestedSymbol = searchParams.get("symbol")?.trim().toUpperCase() ?? "";
  const symbol = /^[A-Z$/][A-Z0-9./-]{0,14}$/.test(requestedSymbol) ? requestedSymbol : undefined;
  const monitoring = !expanded && searchParams.get("tab") === "monitor";
  const terminal = useRef<HTMLElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [fullscreenError, setFullscreenError] = useState("");
  const theme = useContext(ThemeContext);
  const connected = schwab.data?.connected === true;
  const reauthorizationRequired = schwab.data?.reauthorizationRequired === true;

  useEffect(() => {
    if (restoredView.current) return;
    restoredView.current = true;
    if (searchParams.has("view") || searchParams.has("tab")) return;
    const next = new URLSearchParams(searchParams);
    if (viewSettings.view?.expanded) next.set("view", "terminal");
    else if (viewSettings.view?.tab === "monitor") next.set("tab", "monitor");
    else return;
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams, viewSettings.view]);

  function changeView(value: InvestmentView) {
    viewSettings.setView(value);
    const next = new URLSearchParams(searchParams);
    if (value.expanded) next.set("view", "terminal");
    else next.delete("view");
    if (value.tab === "monitor") next.set("tab", "monitor");
    else next.delete("tab");
    setSearchParams(next);
  }

  useEffect(() => {
    const update = () => setFullscreen(Boolean(terminal.current && document.fullscreenElement === terminal.current));
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);

  async function toggleExpanded() {
    if (document.fullscreenElement) await document.exitFullscreen();
    changeView({ tab: "terminal", expanded: !expanded });
  }

  async function toggleFullscreen() {
    setFullscreenError("");
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await terminal.current?.requestFullscreen();
    } catch {
      setFullscreenError("浏览器未能进入全屏，可切换为页面铺满。");
    }
  }

  return (
    <div className="page investment-page" data-expanded={expanded}>
      {viewSettings.save.isError && (
        <Notice tone="warning" title="投资视图尚未保存" className="m-5" action={
          <Button variant="secondary" onClick={() => {
            if (viewSettings.save.error instanceof APIError && viewSettings.save.error.status === 409) window.location.reload();
            else if (viewSettings.save.variables) viewSettings.save.mutate(viewSettings.save.variables);
          }}>
            {viewSettings.save.error instanceof APIError && viewSettings.save.error.status === 409 ? "重新加载" : "重试保存"}
          </Button>
        }>{viewSettings.save.error.message}</Notice>
      )}
      <div hidden={expanded}>
        <PageHeader
          title="投资"
          action={
            <div className="flex gap-2" aria-label="投资视图">
              <Button
                size="sm"
                variant={monitoring ? "secondary" : "primary"}
                aria-pressed={!monitoring}
                onClick={() => changeView({ tab: "terminal", expanded: false })}
              >
                交易终端
              </Button>
              <Button
                size="sm"
                variant={monitoring ? "primary" : "secondary"}
                aria-pressed={monitoring}
                onClick={() => changeView({ tab: "monitor", expanded: false })}
              >
                价格监控
              </Button>
            </div>
          }
        />
      </div>
      <section ref={terminal} className="investment-view" aria-label="投资终端" style={monitoring ? { display: "none" } : undefined}>
        <div className="investment-toolbar">
          <span className="investment-toolbar-title">TradingView</span>
          <div className="investment-toolbar-actions">
            <Button
              variant="ghost"
              size="sm"
              title={expanded ? "恢复侧栏与顶栏" : "隐藏工作台导航并撑满窗口"}
              onClick={() => {
                void toggleExpanded();
              }}
              aria-pressed={expanded}
            >
              {expanded ? <ArrowLeft size={15} /> : <Expand size={15} />}
              {expanded ? "返回工作台" : "页面铺满"}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              title={fullscreen ? "退出全屏" : "进入浏览器全屏"}
              onClick={() => {
                void toggleFullscreen();
              }}
              disabled={!document.fullscreenEnabled}
              aria-pressed={fullscreen}
            >
              {fullscreen ? <Minimize size={15} /> : <Maximize size={15} />}
              {fullscreen ? "退出全屏" : "全屏"}
            </Button>
            <ButtonLink variant="ghost" size="sm" to="/investment?view=terminal" target="_blank" rel="noopener noreferrer">
              <ExternalLink size={15} />
              新窗口
            </ButtonLink>
          </div>
        </div>
        {fullscreenError && (
          <Notice tone="warning" title="全屏不可用" className="m-5">
            {fullscreenError}
          </Notice>
        )}
        {schwab.isPending && <Skeleton className="m-5 h-24" />}
        {schwab.isError && (
          <Notice tone="danger" title="投资配置加载失败" className="m-5">
            {schwab.error.message}
          </Notice>
        )}
        {reauthorizationRequired && (
          <Notice
            tone="warning"
            title="Schwab 授权已失效"
            className="m-5"
            action={
              <Button
                onClick={() => {
                  window.location.href = "/api/modules/investment/schwab/oauth/login";
                }}
              >
                重新授权
              </Button>
            }
          >
            行情和交易已暂停。
          </Notice>
        )}
        {connected && schwab.data?.lastError && (
          <Notice
            tone="danger"
            title="Schwab 连接异常"
            className="m-5"
            action={
              <Button variant="secondary" disabled={retry.isPending} onClick={() => retry.mutate()}>
                {retry.isPending ? "重试中…" : "重试连接"}
              </Button>
            }
          >
            {schwab.data.lastError}
          </Notice>
        )}
        {schwab.data && !connected && !reauthorizationRequired && (
          <Notice
            title="未连接 Schwab"
            className="m-5"
            action={
              <>
                <ButtonLink variant="secondary" to="/settings?tab=modules">
                  打开设置
                </ButtonLink>
                {schwab.data.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && (
                  <Button
                    variant="secondary"
                    onClick={() => {
                      window.location.href = "/api/modules/investment/schwab/oauth/login";
                    }}
                  >
                    登录 Schwab
                  </Button>
                )}
              </>
            }
          >
            授权后可使用交易终端。
          </Notice>
        )}
        {connected && <InvestmentTerminal theme={theme} symbol={symbol} />}
      </section>
      {monitoring && <InvestmentMonitor />}
    </div>
  );
}

function InvestmentTerminal({ theme, symbol }: { theme: Theme; symbol?: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const client = useQueryClient();
  // Keep the chart document and its selected symbol/interval across theme changes.
  const [src] = useState(() => `/investment/terminal?theme=${theme}${symbol ? `&symbol=${encodeURIComponent(symbol)}` : ""}`);
  useEffect(() => {
    frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }, [theme]);
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (
        event.origin !== window.location.origin ||
        event.source !== frame.current?.contentWindow ||
        event.data?.type !== "workbench.schwab.status_changed"
      )
        return;
      void client.invalidateQueries({ queryKey: moduleQueryKey("investment", "schwab") });
      void client.invalidateQueries({ queryKey: moduleQueryKey("investment", "widget") });
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [client]);
  return (
    <iframe
      ref={frame}
      title="TradingView 投资终端"
      className="investment-terminal"
      allowFullScreen
      src={src}
      onLoad={() => {
        frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
      }}
    />
  );
}
