import { Badge } from "../../components/ui/Badge";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useIsAdmin } from "../../shared/auth";
import { useSettingsDialog } from "../../shared/settingsDialog";
import { SchwabConnectionSection } from "./SchwabSettings";
import { useOvernightSettings, useSchwabSettings } from "./queries";

export default function InvestmentSettings({ enabled }: { enabled: boolean }) {
  const isAdmin = useIsAdmin();
  const dialog = useSettingsDialog();
  const schwab = useSchwabSettings(enabled);
  const overnight = useOvernightSettings(enabled);
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
  if (schwab.isPending || overnight.isPending) return <Skeleton className="h-64" />;
  return (
    <div className="investment-settings space-y-8">
      {schwab.isError ? (
        <Notice tone="danger" title="Schwab 设置读取失败">
          {schwab.error.message}
        </Notice>
      ) : (
        schwab.data && <SchwabConnectionSection initial={schwab.data} canManageApp={isAdmin} />
      )}
      {overnight.isError ? (
        <Notice tone="danger" title="夜盘设置读取失败">
          {overnight.error.message}
        </Notice>
      ) : (
        overnight.data && (
          <div className="flex items-center justify-between gap-3 text-sm">
            <span>共享夜盘行情</span>
            <Badge tone={overnight.data.enabled && overnight.data.providerEnabled ? "info" : "neutral"}>
              {overnight.data.enabled && overnight.data.providerEnabled ? "已开放" : "未开放"}
            </Badge>
          </div>
        )
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
