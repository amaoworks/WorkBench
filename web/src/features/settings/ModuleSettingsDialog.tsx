import * as Dialog from "@radix-ui/react-dialog";
import { Suspense } from "react";
import { Settings2, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { settingsRegistry } from "../../modules/registry";

export function ModuleSettingsDialog({ module }: { module: { id: string; name: string; enabled: boolean } }) {
  const Settings = settingsRegistry[module.id];
  return <Dialog.Root>
    <Dialog.Trigger asChild><Button variant="secondary" aria-label={`${module.name}设置`}><Settings2 size={16} aria-hidden="true" />设置</Button></Dialog.Trigger>
    <Dialog.Portal>
      <Dialog.Overlay className="command-overlay z-40" />
      <Dialog.Content className="module-settings-dialog z-50" aria-describedby={undefined}>
        <div className="module-settings-header">
          <div><Dialog.Title>{module.name}设置</Dialog.Title></div>
          <Dialog.Close asChild><Button variant="ghost" size="icon" aria-label="关闭模块设置"><X size={18} /></Button></Dialog.Close>
        </div>
        <div className="module-settings-body">
          {!module.enabled && <p className="module-settings-notice">业务已停用，后台任务暂停；已有数据保留。</p>}
          <Suspense fallback={<p role="status">正在加载设置…</p>}>
            {Settings ? <Settings enabled={module.enabled} /> : <p className="text-sm text-[var(--muted)]">此业务暂无可配置项。</p>}
          </Suspense>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>;
}
