import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Expand, ExternalLink, Maximize, Minimize } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { PageHeader } from "../../components/ui/PageHeader";
import { Skeleton } from "../../components/ui/States";
import { useSchwabSettings } from "./queries";

export default function InvestmentPage() {
  const schwab = useSchwabSettings(true);
  const [searchParams, setSearchParams] = useSearchParams();
  const expanded = searchParams.get("view") === "terminal";
  const terminal = useRef<HTMLElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [fullscreenError, setFullscreenError] = useState("");
  const dark = document.documentElement.classList.contains("dark");
  const connected = schwab.data?.connected === true;

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
      <PageHeader title="投资" description="查看 Schwab 行情、持仓和订单，通过 TradingView 终端交易。" action={<Link className="text-sm underline" to="/settings?tab=modules">Schwab 设置</Link>} />
    </div>
    <section ref={terminal} className="investment-view" aria-label="投资终端">
      <div className="investment-toolbar">
        <span className="investment-toolbar-title">Schwab · TradingView</span>
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
      {schwab.data && !connected && <Card className="m-5 p-5">
        <p className="text-sm">尚未连接 Schwab。请先授权，账户关联页点击 Done。</p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Link className="studio-button secondary" to="/settings?tab=modules">打开设置</Link>
          {schwab.data.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && <Button variant="secondary" onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>登录 Schwab</Button>}
        </div>
      </Card>}
      {connected && <iframe title="TradingView 投资终端" className="investment-terminal" allowFullScreen src={`/investment/terminal?theme=${dark ? "dark" : "light"}`} />}
    </section>
  </div>;
}
