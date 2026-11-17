import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "../../shared/cn";

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("ui-card", className)} {...props} />;
}

export function CardHeader({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="ui-card-header">
      <div>
        <h2>{title}</h2>
      </div>
      {action}
    </div>
  );
}

export function StatCard({
  label,
  value,
  accent = "brand",
}: {
  label: string;
  value: string | number;
  accent?: "brand" | "warning" | "success";
}) {
  return (
    <Card className="stat-card">
      <p className="stat-label">
        <span className={cn("stat-dot", accent)} />
        {label}
      </p>
      <p className="stat-value">{value}</p>
    </Card>
  );
}
