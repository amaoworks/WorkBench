import { useContext, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Maximize, Maximize2, Minimize, Minimize2 } from "lucide-react";
import { Link, Navigate, useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { ThemeContext, type Theme } from "../../shared/theme";
import { refreshSchwabToken, useInvestmentView, useRefreshInvestment, useSchwabSettings } from "./queries";
import { APIError } from "../../shared/api";
import { moduleQueryKey } from "../../shared/queryKeys";

export default function InvestmentPage() {
  const [searchParams] = useSearchParams();
  // Price monitoring moved to its own subpage; keep old links and notification routes working.
  if (searchParams.get("tab") === "monitor") {
    const next = new URLSearchParams(searchParams);
    next.delete("tab");
    next.delete("view");
    const query = next.toString();
    return <Navigate to={`/investment/monitor${query ? `?${query}` : ""}`} replace />;
  }
  return <InvestmentChart />;
}

function InvestmentChart() {
  const viewSettings = useInvestmentView();
  if (viewSettings.query.isPending)
    return (
      <div className="investment-page">
        <Skeleton className="investment-view" />
      </div>
    );
  if (viewSettings.query.isError)
    return (
      <div className="investment-page">
        <div className="investment-view investment-gate">
          <Notice tone="danger" title="投资设置加载失败" action={<Button onClick={() => viewSettings.query.refetch()}>重新加载</Button>}>
            {viewSettings.query.error.message}
          </Notice>
        </div>
      </div>
    );
  return <InvestmentContent viewSettings={viewSettings} />;
}

function InvestmentContent({ viewSettings }: { viewSettings: ReturnType<typeof useInvestmentView> }) {
  const schwab = useSchwabSettings(true);
  const refresh = useRefreshInvestment();
  const retry = useMutation({
    mutationFn: refreshSchwabToken,
    onSettled: refresh,
  });
  const [searchParams, setSearchParams] = useSearchParams();
  const restoredView = useRef(false);
  const expanded = searchParams.get("view") === "terminal";
  const requestedSymbol = searchParams.get("symbol")?.trim().toUpperCase() ?? "";
  const symbol = /^[A-Z$/][A-Z0-9./-]{0,14}$/.test(requestedSymbol) ? requestedSymbol : undefined;
  const terminal = useRef<HTMLElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [fullscreenError, setFullscreenError] = useState("");
  const theme = useContext(ThemeContext);
  const connected = schwab.data?.connected === true;
  const reauthorizationRequired = schwab.data?.reauthorizationRequired === true;

  useEffect(() => {
    if (restoredView.current) return;
    restoredView.current = true;
    if (searchParams.has("view") || !viewSettings.view?.expanded) return;
    const next = new URLSearchParams(searchParams);
    next.set("view", "terminal");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams, viewSettings.view]);

  useEffect(() => {
    const update = () => setFullscreen(Boolean(terminal.current && document.fullscreenElement === terminal.current));
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);

  async function toggleExpanded() {
    if (document.fullscreenElement) await document.exitFullscreen();
    // The preference schema keeps `tab`; the chart is the only view on this page.
    viewSettings.setView({ tab: "terminal", expanded: !expanded });
    const next = new URLSearchParams(searchParams);
    next.delete("tab");
    if (expanded) next.delete("view");
    else next.set("view", "terminal");
    setSearchParams(next);
  }

  async function toggleFullscreen() {
    setFullscreenError("");
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await terminal.current?.requestFullscreen();
    } catch {
      setFullscreenError("浏览器未能进入全屏，可改用页面铺满。");
    }
  }

  const login = schwab.data?.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && (
    <Button
      onClick={() => {
        window.location.href = "/api/modules/investment/schwab/oauth/login";
      }}
    >
      登录 Schwab
    </Button>
  );
  const banner = viewSettings.save.isError ? (
    <Notice
      tone="warning"
      title="投资视图尚未保存"
      action={
        <Button
          variant="secondary"
          onClick={() => {
            if (viewSettings.save.error instanceof APIError && viewSettings.save.error.status === 409) window.location.reload();
            else if (viewSettings.save.variables) viewSettings.save.mutate(viewSettings.save.variables);
          }}
        >
          {viewSettings.save.error instanceof APIError && viewSettings.save.error.status === 409 ? "重新加载" : "重试保存"}
        </Button>
      }
    >
      {viewSettings.save.error.message}
    </Notice>
  ) : fullscreenError ? (
    <Notice tone="warning" title="全屏不可用">
      {fullscreenError}
    </Notice>
  ) : connected && schwab.data?.lastError ? (
    <Notice
      tone="danger"
      title="Schwab 连接异常"
      action={
        <Button variant="secondary" disabled={retry.isPending} onClick={() => retry.mutate()}>
          {retry.isPending ? "重试中…" : "重试连接"}
        </Button>
      }
    >
      {schwab.data.lastError}
    </Notice>
  ) : null;

  return (
    <div className="investment-page" data-expanded={expanded}>
      <h1 className="sr-only">投资</h1>
      <section ref={terminal} className="investment-view" aria-label="投资终端">
        {schwab.isPending && <Skeleton className="investment-terminal" />}
        {schwab.isError && (
          <div className="investment-gate">
            <Notice
              tone="danger"
              title="投资配置加载失败"
              action={
                <Button variant="secondary" onClick={() => schwab.refetch()}>
                  重新加载
                </Button>
              }
            >
              {schwab.error.message}
            </Notice>
          </div>
        )}
        {reauthorizationRequired && (
          <div className="investment-gate">
            <Notice
              tone="warning"
              title="Schwab 授权已失效"
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
          </div>
        )}
        {schwab.data && !connected && !reauthorizationRequired && (
          <div className="investment-gate">
            <Notice
              title="未连接 Schwab"
              action={
                <>
                  <ButtonLink variant="secondary" to="/settings?tab=modules&module=investment">
                    券商连接
                  </ButtonLink>
                  {login}
                </>
              }
            >
              授权后可使用图表与交易。
            </Notice>
          </div>
        )}
        {connected && <InvestmentTerminal theme={theme} symbol={symbol} />}
        {banner && <div className="investment-banner">{banner}</div>}
        <div className="investment-float" role="toolbar" aria-label="图表窗口">
          <button
            type="button"
            className="investment-float-button focus-ring"
            title={expanded ? "返回工作台" : "页面铺满"}
            aria-label={expanded ? "返回工作台" : "页面铺满"}
            aria-pressed={expanded}
            onClick={() => {
              void toggleExpanded();
            }}
          >
            {expanded ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
          </button>
          <button
            type="button"
            className="investment-float-button focus-ring"
            title={fullscreen ? "退出全屏" : "全屏"}
            aria-label={fullscreen ? "退出全屏" : "全屏"}
            aria-pressed={fullscreen}
            disabled={!document.fullscreenEnabled}
            onClick={() => {
              void toggleFullscreen();
            }}
          >
            {fullscreen ? <Minimize size={15} /> : <Maximize size={15} />}
          </button>
          <Link
            className="investment-float-button focus-ring"
            to={`/investment?view=terminal${symbol ? `&symbol=${encodeURIComponent(symbol)}` : ""}`}
            target="_blank"
            rel="noopener noreferrer"
            title="新窗口"
            aria-label="新窗口"
          >
            <ExternalLink size={15} />
          </Link>
        </div>
      </section>
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
      void client.invalidateQueries({
        queryKey: moduleQueryKey("investment", "schwab"),
      });
      void client.invalidateQueries({
        queryKey: moduleQueryKey("investment", "widget"),
      });
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
