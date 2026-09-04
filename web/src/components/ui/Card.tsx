import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "../../shared/cn";

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-[0_1px_2px_rgb(15_23_42/0.03)]", className)} {...props} />;
}

export function CardHeader({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-[var(--border)] px-5 py-4">
      <div><h2 className="font-semibold">{title}</h2>{description && <p className="mt-1 text-xs text-[var(--muted)]">{description}</p>}</div>
      {action}
    </div>
  );
}

export function StatCard({ label, value, accent = "indigo" }: { label: string; value: string | number; accent?: "indigo" | "amber" | "emerald" }) {
  const colors = { indigo: "bg-indigo-500", amber: "bg-amber-500", emerald: "bg-emerald-500" };
  return (
    <Card className="relative overflow-hidden p-5">
      <span className={cn("absolute inset-y-0 left-0 w-1", colors[accent])} />
      <p className="text-xs font-medium text-[var(--muted)]">{label}</p>
      <p className="mt-2 text-2xl font-semibold tracking-tight">{value}</p>
    </Card>
  );
}

