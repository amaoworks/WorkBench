import { useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { WidgetHeaderSlotContext } from "../../shared/dashboard";

/** Renders badges or actions into the surrounding overview card header. */
export function WidgetHeader({ children }: { children: ReactNode }) {
  const slot = useContext(WidgetHeaderSlotContext);
  return slot ? createPortal(children, slot) : null;
}

/** One-line placeholder for a card that needs setup instead of an empty box. */
export function WidgetSetup({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="widget-setup">
      <span>{children}</span>
      {action}
    </div>
  );
}
