import { useContext, useEffect, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Navigate, useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { ThemeContext, type Theme } from "../../shared/theme";
import { refreshSchwabToken, useRefreshInvestment, useSchwabSettings } from "./queries";
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
  const schwab = useSchwabSettings(true);
  const refresh = useRefreshInvestment();
  const retry = useMutation({
    mutationFn: refreshSchwabToken,
    onSettled: refresh,
  });
  const [searchParams] = useSearchParams();
  const requestedSymbol = searchParams.get("symbol")?.trim().toUpperCase() ?? "";
  const symbol = /^[A-Z$/][A-Z0-9./-]{0,14}$/.test(requestedSymbol) ? requestedSymbol : undefined;
  const theme = useContext(ThemeContext);
  const connected = schwab.data?.connected === true;
  const reauthorizationRequired = schwab.data?.reauthorizationRequired === true;

  const login = schwab.data?.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && (
    <Button
      onClick={() => {
        window.location.href = "/api/modules/investment/schwab/oauth/login";
      }}
    >
      登录 Schwab
    </Button>
  );
  const banner = connected && schwab.data?.lastError ? (
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
    <div className="investment-page">
      <h1 className="sr-only">投资</h1>
      <section className="investment-view" aria-label="投资终端">
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
            />
          </div>
        )}
        {connected && <InvestmentTerminal theme={theme} symbol={symbol} />}
        {banner && <div className="investment-banner">{banner}</div>}
      </section>
    </div>
  );
}

/** Parent-side limits: they hold even when the terminal document never runs its own loading script. */
const TERMINAL_SLOW_MS = 20_000;
const TERMINAL_STALL_MS = 90_000;
type TerminalLoad = "loading" | "slow" | "stalled" | "ready" | "failed";

function InvestmentTerminal({ theme, symbol }: { theme: Theme; symbol?: string }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const client = useQueryClient();
  const themeRef = useRef(theme);
  // Keep the chart document and its selected symbol/interval across theme changes.
  const [src] = useState(() => `/investment/terminal?theme=${theme}${symbol ? `&symbol=${encodeURIComponent(symbol)}` : ""}`);
  const [attempt, setAttempt] = useState(0);
  const [load, setLoad] = useState<TerminalLoad>("loading");
  const heardFromTerminal = useRef(false);
  const lastProgress = useRef(0);
  useEffect(() => {
    themeRef.current = theme;
    frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme }, window.location.origin);
  }, [theme]);
  useEffect(() => {
    // Restart the watchdog for every mounted frame, including a retry.
    heardFromTerminal.current = false;
    lastProgress.current = Date.now();
    const timer = window.setInterval(() => {
      setLoad((current) => {
        if (current === "ready" || current === "failed" || current === "stalled") return current;
        const idle = Date.now() - lastProgress.current;
        if (idle >= TERMINAL_STALL_MS) return "stalled";
        // The terminal shows its own "still loading" state; only cover for a silent frame.
        if (!heardFromTerminal.current && idle >= TERMINAL_SLOW_MS) return "slow";
        return current;
      });
    }, 1000);
    return () => window.clearInterval(timer);
  }, [attempt]);
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== frame.current?.contentWindow) return;
      const type: unknown = event.data?.type;
      if (type === "workbench.terminal.progress" || type === "workbench.terminal.slow") {
        heardFromTerminal.current = true;
        if (type === "workbench.terminal.progress") lastProgress.current = Date.now();
        setLoad((current) => (current === "slow" ? "loading" : current));
        return;
      }
      if (type === "workbench.terminal.ready") {
        setLoad("ready");
        return;
      }
      if (type === "workbench.terminal.failed") {
        heardFromTerminal.current = true;
        setLoad("failed");
        return;
      }
      if (type !== "workbench.schwab.status_changed") return;
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
    <div className="investment-terminal-shell">
      <iframe
        key={attempt}
        ref={frame}
        title="投资终端"
        className="investment-terminal"
        allowFullScreen
        src={src}
        onLoad={() => {
          frame.current?.contentWindow?.postMessage({ type: "workbench.theme", theme: themeRef.current }, window.location.origin);
        }}
      />
      {(load === "slow" || load === "stalled") && (
        <div className="investment-terminal-cover" role={load === "stalled" ? "alert" : "status"}>
          <strong>{load === "stalled" ? "图表加载失败" : "还在加载图表…"}</strong>
          {load === "stalled" && <p>超过 90 秒没有进展</p>}
          <Button variant={load === "stalled" ? "primary" : "secondary"} size="sm" onClick={() => {
            setLoad("loading");
            setAttempt((value) => value + 1);
          }}>
            重新加载
          </Button>
        </div>
      )}
    </div>
  );
}
