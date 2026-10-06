import { useRef, useState } from "react";

type Options<T> = {
  title: string;
  description: (target: T) => string;
  onConfirm: (target: T) => Promise<unknown>;
  fallbackFocus: () => HTMLElement | null;
};

export function useConfirmDelete<T>({ title, description, onConfirm, fallbackFocus }: Options<T>) {
  const [target, setTarget] = useState<T | null>(null);
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const trigger = useRef<HTMLElement | null>(null);
  const removed = useRef(false);

  function request(next: T, element: HTMLElement) {
    if (submitting.current) return;
    trigger.current = element;
    removed.current = false;
    setTarget(next);
  }

  function cancel() {
    if (!submitting.current) setTarget(null);
  }

  async function confirm() {
    if (target === null || submitting.current) return;
    submitting.current = true;
    setBusy(true);
    try {
      await onConfirm(target);
      removed.current = true;
      setTarget(null);
    } catch {
      // The mutation displays the error; keep the target available for retry.
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  function restoreFocus() {
    const element = !removed.current && trigger.current?.isConnected ? trigger.current : fallbackFocus();
    if (element?.isConnected && !element.matches(":disabled")) element.focus();
  }

  return {
    target,
    request,
    cancel,
    dialogProps: {
      open: target !== null,
      title,
      description: target === null ? "" : description(target),
      busy,
      onCancel: cancel,
      onConfirm: () => {
        void confirm();
      },
      onClosed: restoreFocus,
    },
  };
}
