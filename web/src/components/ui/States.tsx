import { cn } from "../../shared/cn";

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-[var(--soft)]", className)} />;
}
