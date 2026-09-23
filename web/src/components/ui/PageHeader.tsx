import type { ReactNode } from "react";

export function PageHeader({ title, description, action }: { title: string; description?: ReactNode; action?: ReactNode }) {
  return (
    <header className="page-header">
      <div className="page-header-main">
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {action && <div className="page-header-action">{action}</div>}
    </header>
  );
}
