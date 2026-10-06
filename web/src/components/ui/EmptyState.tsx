import type { ReactNode } from "react";
import { Inbox } from "lucide-react";

export function EmptyState({
  icon = <Inbox size={22} />,
  title,
  description,
  action,
}: {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <div className="icon-tile" aria-hidden="true">
        {icon}
      </div>
      <h3>{title}</h3>
      {description && <p>{description}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
