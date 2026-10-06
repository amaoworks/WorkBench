import type { ReactNode } from "react";
import { cn } from "../../shared/cn";

type NoticeProps = {
  tone?: "info" | "warning" | "danger";
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  className?: string;
};

const tones = {
  info: "border-[var(--border)] bg-[var(--soft)]",
  warning: "border-[var(--warning)] bg-[var(--warning-soft)]",
  danger: "border-[var(--danger)] bg-[var(--danger-soft)]",
};

export function Notice({ tone = "info", title, children, action, className }: NoticeProps) {
  return (
    <div role={tone === "info" ? "status" : "alert"} className={cn("min-w-0 rounded-lg border p-4", tones[tone], className)}>
      <h3 className="break-words font-semibold">{title}</h3>
      {children && <div className="mt-2 whitespace-pre-wrap break-words text-sm text-[var(--muted)]">{children}</div>}
      {action && <div className="mt-3 flex flex-wrap gap-2">{action}</div>}
    </div>
  );
}
