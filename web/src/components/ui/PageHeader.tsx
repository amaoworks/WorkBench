import type { ReactNode } from "react";

/**
 * Page toolbar. Only the overview shows a large title; every other page is already named by the
 * top-bar entry or its menu item, so the title is screen-reader only and the row keeps just the
 * informative parts: a short status/data summary and the page actions.
 */
export function PageHeader({ title, description, action }: { title: string; description?: ReactNode; action?: ReactNode }) {
  // Nothing informative to show: keep only the screen-reader title and the top spacing.
  if (!description && !action)
    return (
      <header className="page-toolbar-empty">
        <h1 className="sr-only">{title}</h1>
      </header>
    );
  return (
    <header className="page-toolbar">
      <h1 className="sr-only">{title}</h1>
      <div className="page-toolbar-info">{description}</div>
      {action && <div className="page-toolbar-action">{action}</div>}
    </header>
  );
}
