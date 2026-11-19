import * as Dialog from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import { Button } from "../../components/ui/Button";

export function NewsDialog({
  open,
  title,
  onOpenChange,
  children,
}: {
  open: boolean;
  title: string;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="command-overlay z-40" />
        <Dialog.Content className="module-settings-dialog z-50" aria-describedby={undefined}>
          <div className="module-settings-header">
            <Dialog.Title>{title}</Dialog.Title>
            <Dialog.Close asChild>
              <Button variant="ghost" size="icon" aria-label={`关闭${title}`}>
                <X size={18} />
              </Button>
            </Dialog.Close>
          </div>
          <div className="module-settings-body">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
