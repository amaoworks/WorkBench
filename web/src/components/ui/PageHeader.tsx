import type { ReactNode } from "react";

/**
 * Page toolbar. Only the overview shows a large title; every other page is already named by the
 * top-bar entry or its menu item, so the title is screen-reader only. The visible row holds page
 * actions only — no status or explanatory lines, which would repeat what the page already shows.
 */
export function PageHeader({ title, action }: { title: string; action?: ReactNode }) {
  if (!action)
    return (
      <header className="page-toolbar-empty">
        <h1 className="sr-only">{title}</h1>
      </header>
    );
  return (
    <header className="page-toolbar">
      <h1 className="sr-only">{title}</h1>
      <div className="page-toolbar-action">{action}</div>
    </header>
  );
}
