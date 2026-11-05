import { Badge } from "../../components/ui/Badge";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useIsAdmin } from "../../shared/auth";
import { useSettingsDialog } from "../../shared/settingsDialog";
import { SchwabConnectionSection } from "./SchwabSettings";
import { useMarketDataStatus, useSchwabSettings } from "./queries";

export default function InvestmentSettings({ enabled }: { enabled: boolean }) {
  const isAdmin = useIsAdmin();
  const dialog = useSettingsDialog();
  const schwab = useSchwabSettings(enabled);
  const market = useMarketDataStatus(enabled);
  if (!enabled) {
    return (
      <Notice
        title="投资业务已停用"
        action={isAdmin ? <ButtonLink to="/admin/settings?tab=modules" variant="secondary">业务管理</ButtonLink> : undefined}
      >
        {isAdmin ? "在后台设置中启用投资业务。" : "联系管理员启用投资业务。"}
      </Notice>
    );
  }
  if (schwab.isPending || market.isPending) return <Skeleton className="h-64" />;
  return (
    <div className="investment-settings space-y-8">
      {schwab.isError ? (
        <Notice tone="danger" title="Schwab 设置读取失败">
          {schwab.error.message}
        </Notice>
      ) : (
        schwab.data && <SchwabConnectionSection initial={schwab.data} canManageApp={isAdmin} />
      )}
      {market.isError ? (
        <Notice tone="danger" title="行情配置读取失败">{market.error.message}</Notice>
      ) : market.data && (
        <div className="space-y-4 text-sm">
          <div className="flex items-center justify-between gap-3">
            <span>主行情源</span>
            <Badge>{market.data.provider === "schwab" ? "Schwab" : market.data.provider === "futu" ? "富途" : "未配置"}</Badge>
          </div>
          <div className="flex items-center justify-between gap-3">
            <span>共享夜盘行情</span>
            <Badge tone={market.data.overnightEnabled ? "info" : "neutral"}>
              {market.data.overnightEnabled ? "已开放" : "未开放"}
            </Badge>
          </div>
        </div>
      )}
      {dialog && (
        <div className="settings-dialog-footer">
          <Button type="button" variant="secondary" onClick={dialog.requestClose}>
            关闭
          </Button>
        </div>
      )}
    </div>
  );
}
