import * as Dialog from "@radix-ui/react-dialog";
import { Suspense } from "react";
import { Settings2, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { settingsRegistry } from "../../modules/registry";
import type { WorkbenchModule } from "../../shared/schema";
import { ExternalSettingsFrame } from "../../app/ExternalPage";
import { useSettings } from "./queries";

export function ModuleSettingsDialog({ module }: { module: WorkbenchModule }) {
  const Settings = settingsRegistry[module.id];
  const settings = useSettings();
  const theme = settings.data?.appearance.theme === "dark" || (settings.data?.appearance.theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
  return <Dialog.Root>
    <Dialog.Trigger asChild><Button variant="secondary" aria-label={`${module.name}设置`}><Settings2 size={16} aria-hidden="true" />设置</Button></Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="command-overlay z-40" />
      <Dialog.Content className={`module-settings-dialog z-50${module.kind === "external" ? " is-external" : ""}`} aria-describedby={undefined}>
        <div className="module-settings-header">
          <div><Dialog.Title>{module.name}设置</Dialog.Title></div>
          <Dialog.Close asChild><Button variant="ghost" size="icon" aria-label="关闭模块设置"><X size={18} /></Button></Dialog.Close>
        </div>
        <div className="module-settings-body">
          {!module.enabled && <p className="module-settings-notice">{module.kind === "external" ? "业务已停用，仍可修改配置和连接。" : "业务已停用，后台任务暂停；已有数据保留。"}</p>}
          {module.kind === "external" && module.pending && <p className="module-settings-notice" role="status">{module.enabled ? "正在启用，尚未确认成功。" : "业务入口已关闭，等待服务确认停用。"}</p>}
          {module.kind === "external" && module.health === "offline" && <p className="module-settings-notice" role="alert">服务当前不可达，可修改连接后重试。</p>}
          {module.kind === "external" && module.health === "incompatible" && <p className="module-settings-notice" role="alert">协议不兼容，业务入口已关闭。</p>}
          <Suspense fallback={<p role="status">正在加载设置…</p>}>
            {module.kind === "external" && module.settingsEntry
              ? <ExternalSettingsFrame moduleId={module.id} entry={module.settingsEntry} theme={theme} />
              : Settings ? <Settings enabled={module.enabled} /> : <p className="text-sm text-[var(--muted)]">此业务暂无可配置项。</p>}
          </Suspense>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
