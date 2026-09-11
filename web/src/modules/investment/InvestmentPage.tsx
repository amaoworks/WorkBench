import { Link } from "react-router-dom";
import { Button } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { PageHeader } from "../../components/ui/PageHeader";
import { Skeleton } from "../../components/ui/States";
import { useSchwabSettings } from "./queries";

export default function InvestmentPage() {
  const schwab = useSchwabSettings(true);
  const dark = document.documentElement.classList.contains("dark");
  const connected = schwab.data?.connected === true;
  return <div className="page">
    <PageHeader title="投资" description="查看 Schwab 行情、持仓和订单，通过 TradingView 终端交易。" action={<Link className="text-sm underline" to="/settings?tab=modules">Schwab 设置</Link>} />
    {schwab.isPending && <Skeleton className="mb-5 h-24" />}
    {schwab.isError && <p role="alert" className="mb-5 text-danger">{schwab.error.message}</p>}
    {schwab.data && !connected && <Card className="mb-5 p-5">
      <p className="text-sm">尚未连接 Schwab。请先授权，账户关联页点击 Done。</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <Link className="studio-button secondary" to="/settings?tab=modules">打开设置</Link>
        {schwab.data.hasAppSecret && schwab.data.callbackUrl.startsWith("https://") && <Button variant="secondary" onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>登录 Schwab</Button>}
      </div>
    </Card>}
    {connected && <iframe title="TradingView 投资终端" className="investment-terminal mb-5" src={`/investment/terminal?theme=${dark ? "dark" : "light"}`} />}
  </div>;
}
