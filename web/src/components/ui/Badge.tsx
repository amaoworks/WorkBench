import type { HTMLAttributes } from "react";
import { cn } from "../../shared/cn";

const tones = {
  neutral: "bg-[var(--soft)] text-[var(--muted)]",
  success: "bg-[var(--success-soft)] text-[var(--success)]",
  warning: "bg-[var(--warning-soft)] text-[var(--warning)]",
  danger: "bg-[var(--danger-soft)] text-[var(--danger)]",
  info: "bg-[var(--brand-soft)] text-[var(--brand)]",
};

export function Badge({ tone = "neutral", className, ...props }: HTMLAttributes<HTMLSpanElement> & { tone?: keyof typeof tones }) {
  return (
    <span className={cn("inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs font-medium", tones[tone], className)} {...props} />
  );
}
