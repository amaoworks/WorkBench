import type { ReactNode } from "react";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card } from "../../components/ui/Card";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import type { PortfolioPositions } from "./schema";

export function BrokerGate({ status, message, onRetry, children }: {
  status: PortfolioPositions["status"]; message?: string; onRetry: () => void; children: ReactNode;
}) {
  if (status === "error") return (
    <Notice tone="danger" title="券商数据加载失败" action={<Button variant="secondary" onClick={onRetry}>重试</Button>}>
      {message}
    </Notice>
  );
  if (status === "reauth") return (
    <Notice tone="warning" title="Schwab 授权已失效" action={
      <ButtonLink to="/api/modules/investment/schwab/oauth/login" reloadDocument variant="secondary" size="sm">重新授权</ButtonLink>
    }>
      重新授权后即可读取持仓与订单。
    </Notice>
  );
  if (status === "unconfigured") return (
    <Card><EmptyState title="尚未连接券商" action={
      <ButtonLink to="/settings?tab=modules&module=investment" variant="secondary">连接券商</ButtonLink>
    } /></Card>
  );
  return <>{message && <Notice tone="warning" title={message} className="mb-4" />}{children}</>;
}
