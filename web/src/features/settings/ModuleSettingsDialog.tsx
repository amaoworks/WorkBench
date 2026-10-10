import * as Dialog from "@radix-ui/react-dialog";
import { Suspense, useMemo, useRef, useState } from "react";
import { Settings2, X } from "lucide-react";
import { Button } from "../../components/ui/Button";
import { settingsRegistry } from "../../modules/registry";
import type { WorkbenchModule } from "../../shared/schema";
import { ExternalSettingsFrame } from "../../app/ExternalPage";
import { useSettings } from "./queries";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { EmptyState } from "../../components/ui/EmptyState";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { SettingsDialogContext, type SettingsDialogControl } from "../../shared/settingsDialog";

export function ModuleSettingsDialog({ module }: { module: WorkbenchModule }) {
  const Settings = settingsRegistry[module.id];
  const settings = useSettings();
  const theme =
    settings.data?.appearance.theme === "dark" ||
    (settings.data?.appearance.theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)
      ? "dark"
      : "light";
  const [open, setOpen] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const discard = useRef(false);
  const dirty = useRef(false);
  const control = useMemo<SettingsDialogControl>(
    () => ({
      setDirty: (next) => {
        dirty.current = next;
      },
      requestClose: () => {
        if (dirty.current) setConfirmDiscard(true);
        else setOpen(false);
      },
    }),
    [],
  );
  return (
    <Dialog.Root open={open} onOpenChange={(next) => (next ? setOpen(true) : control.requestClose())}>
      <Dialog.Trigger asChild>
        <Button variant="secondary" aria-label={`${module.name}设置`}>
          <Settings2 size={16} aria-hidden="true" />
          设置
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="command-overlay z-40" />
        <Dialog.Content
          className={`module-settings-dialog z-50${module.kind === "external" ? " is-external" : ""}`}
          aria-describedby={undefined}
        >
          <div className="module-settings-header">
            <div>
              <Dialog.Title>{module.name}设置</Dialog.Title>
            </div>
            <Dialog.Close asChild>
              <Button variant="ghost" size="icon" aria-label="关闭业务设置">
                <X size={18} />
              </Button>
            </Dialog.Close>
          </div>
          <div className="module-settings-body">
            {!module.enabled && module.kind === "external" && (
              <Notice title="业务已停用" className="mb-4">
                可修改配置和连接。
              </Notice>
            )}
            {module.kind === "external" && module.pending && (
              <Notice title={module.enabled ? "正在启用" : "正在停用"} className="mb-4">
                等待服务确认。
              </Notice>
            )}
            {module.kind === "external" && module.health === "offline" && (
              <Notice tone="warning" title="服务不可达" className="mb-4">
                修改连接后重试。
              </Notice>
            )}
            {module.kind === "external" && module.health === "incompatible" && (
              <Notice tone="danger" title="协议不兼容" className="mb-4">
                业务入口已关闭。
              </Notice>
            )}
            <SettingsDialogContext.Provider value={control}>
              <Suspense fallback={<Skeleton className="h-48" />}>
                {module.kind === "external" && module.settingsEntry ? (
                  <ExternalSettingsFrame moduleId={module.id} entry={module.settingsEntry} theme={theme} />
                ) : Settings ? (
                  <Settings enabled={module.enabled} />
                ) : (
                  <EmptyState title="暂无配置项" />
                )}
              </Suspense>
            </SettingsDialogContext.Provider>
          </div>
          <ConfirmDialog
            open={confirmDiscard}
            title="放弃未保存的修改？"
            description="关闭后，本次修改不会保存。"
            busy={false}
            confirmLabel="放弃修改"
            cancelLabel="继续编辑"
            onCancel={() => setConfirmDiscard(false)}
            onConfirm={() => {
              discard.current = true;
              setConfirmDiscard(false);
            }}
            onClosed={() => {
              if (!discard.current) return;
              discard.current = false;
              dirty.current = false;
              setOpen(false);
            }}
          />
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
