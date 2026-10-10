import * as Dialog from "@radix-ui/react-dialog";
import { useRef } from "react";
import { Button } from "./Button";

export function ConfirmDialog({
  open,
  title,
  description,
  busy,
  onCancel,
  onConfirm,
  onClosed,
  confirmLabel = "确认删除",
  busyLabel = "删除中…",
  cancelLabel = "取消",
  tone = "danger",
}: {
  open: boolean;
  title: string;
  description: string;
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  onClosed: () => void;
  confirmLabel?: string;
  busyLabel?: string;
  cancelLabel?: string;
  tone?: "danger" | "primary";
}) {
  const cancel = useRef<HTMLButtonElement>(null);
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) onCancel();
      }}
    >
      <Dialog.Portal>
        {/* Confirmations can open above another dialog, so they always stack over it. */}
        <Dialog.Overlay className="command-overlay confirm-overlay" />
        <Dialog.Content
          className="module-settings-dialog confirm-dialog"
          role="alertdialog"
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            cancel.current?.focus();
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            onClosed();
          }}
          onEscapeKeyDown={(event) => {
            if (busy) event.preventDefault();
          }}
          onInteractOutside={(event) => event.preventDefault()}
        >
          <div className="p-5">
            <Dialog.Title className="font-semibold">{title}</Dialog.Title>
            <Dialog.Description className="mt-3 whitespace-pre-wrap break-words text-sm text-[var(--muted)]">
              {description}
            </Dialog.Description>
            <div className="mt-5 flex justify-end gap-2">
              <Button ref={cancel} variant="secondary" disabled={busy} onClick={onCancel}>
                {cancelLabel}
              </Button>
              <Button variant={tone} disabled={busy} onClick={onConfirm}>
                {busy ? busyLabel : confirmLabel}
              </Button>
            </div>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
