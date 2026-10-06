import type { Widget } from "../../shared/schema";
import { Card } from "../../components/ui/Card";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useInvestmentWidget } from "./queries";

export default function InvestmentWidget({ widget }: { widget: Widget }) {
  const schwab = useInvestmentWidget(widget);
  if (schwab.isPending) return <Skeleton className="h-32" />;
  if (schwab.isError)
    return (
      <Notice tone="danger" title="投资加载失败">
        {schwab.error.message}
      </Notice>
    );
  return (
    <Card className="p-5">
      <p className="mb-3 text-sm font-medium">
        {schwab.data.reauthorizationRequired ? "Schwab 授权已失效" : schwab.data.connected ? "Schwab 已连接" : "尚未连接 Schwab"}
      </p>
      {schwab.data.reauthorizationRequired && <p className="mb-3 text-xs text-[var(--muted)]">请重新授权以恢复行情与交易。</p>}
      {!schwab.data.connected && !schwab.data.reauthorizationRequired && (
        <p className="mb-3 text-xs text-[var(--muted)]">请在设置中完成授权。</p>
      )}
      {schwab.data.lastError && !schwab.data.reauthorizationRequired && (
        <Notice tone="danger" title="Schwab 连接异常" className="mb-3">
          {schwab.data.lastError}
        </Notice>
      )}
      {schwab.data.reauthorizationRequired ? (
        <Button
          onClick={() => {
            window.location.href = "/api/modules/investment/schwab/oauth/login";
          }}
        >
          重新授权
        </Button>
      ) : (
        <ButtonLink variant="secondary" size="sm" to={schwab.data.connected ? "/investment" : "/settings?tab=modules"}>
          {schwab.data.connected ? "打开投资终端" : "连接 Schwab"}
        </ButtonLink>
      )}
    </Card>
  );
}
