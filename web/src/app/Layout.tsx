import { unreadCountSchema } from "../shared/schema";
import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useState } from "react";
import {
  Bell,
  Bot,
  CheckSquare2,
  LayoutDashboard,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Sun,
  Settings2,
  Aperture,
} from "lucide-react";
import { NavLink, Route, Routes } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { apiValidated } from "../shared/api";
import { cn } from "../shared/cn";
import { ThemeContext } from "../shared/theme";
import { Button } from "../components/ui/Button";
import { Skeleton } from "../components/ui/States";
import { useModules } from "./queries";
import { AIPanel } from "../features/ai/AIPanel";
import { useNotificationStream } from "../features/notifications/useNotificationStream";
import { useSettings, useAppearance } from "../features/settings/queries";
import { toast } from "sonner";

const DashboardPage = lazy(() => import("../features/dashboard/DashboardPage"));
const NotificationsPage = lazy(() => import("../features/notifications/NotificationsPage"));
const SettingsPage = lazy(() => import("../features/settings/SettingsPage"));

import { pageRegistry, iconRegistry } from "../modules/registry";
import { ExternalPage } from "./ExternalPage";

export function Layout() {
  const modules = useModules();
  const [collapsed, setCollapsed] = useState(false);
  const [aiOpen, setAIOpen] = useState(false);
  const settings = useSettings();
  const appearance = useAppearance();
  const theme = settings.data?.appearance.theme ?? "system";
  const motion = settings.data?.appearance.motion ?? "full";
  const [systemDark, setSystemDark] = useState(() => window.matchMedia("(prefers-color-scheme: dark)").matches);
  const dark = theme === "dark" || (theme === "system" && systemDark);
  useNotificationStream();
  const unread = useQuery({
    queryKey: ["notifications", "count"],
    queryFn: () => apiValidated("/api/notifications/unread-count", unreadCountSchema),
  });
  useLayoutEffect(() => {
    const root = document.documentElement;
    if (root.classList.contains("dark") === dark) return;
    // Switch every color in the same frame instead of letting per-element transitions trail behind.
    root.classList.add("theme-switching");
    root.classList.toggle("dark", dark);
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => root.classList.remove("theme-switching")));
    return () => {
      cancelAnimationFrame(frame);
      root.classList.remove("theme-switching");
    };
  }, [dark]);
  useEffect(() => {
    document.documentElement.dataset.motion = motion;
  }, [motion]);
  useEffect(() => {
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(query.matches);
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  const navigation = useMemo(
    () =>
      modules.data?.items
        .filter((item) => item.enabled && (item.kind === "external" || (item.observedEnabled === true && !item.pending && !item.lastError)))
        .flatMap((item) => item.navigation.map((nav) => ({ ...nav, icon: item.icon })))
        .sort((a, b) => a.order - b.order) ?? [],
    [modules.data],
  );
  return (
    <ThemeContext.Provider value={dark ? "dark" : "light"}>
      <div className="workbench-shell min-h-screen">
        <aside
          data-collapsed={collapsed}
          className={cn(
            "workspace-sidebar fixed inset-y-0 left-0 z-20 flex flex-col border-r border-[var(--border)] bg-[var(--surface)] transition-[width]",
            collapsed ? "w-16" : "w-56",
          )}
        >
          <div className="workspace-logo flex h-20 items-center gap-3 px-4">
            <div className="brand-mark">
              <Aperture size={23} strokeWidth={1.5} />
            </div>
            {!collapsed && <span className="sidebar-label font-semibold tracking-tight">Workbench</span>}
          </div>
          <nav className="flex-1 space-y-1 p-2" aria-label="主导航">
            <SideLink to="/" icon={LayoutDashboard} label="总览" collapsed={collapsed} />
            {navigation.map((item) => {
              const Icon = iconRegistry[item.icon] ?? CheckSquare2;
              return <SideLink key={item.pageKey} to={item.route} icon={Icon} label={item.label} collapsed={collapsed} />;
            })}
            <SideLink to="/notifications" icon={Bell} label="通知" collapsed={collapsed} badge={unread.data?.count} />
            <SideLink to="/settings" icon={Settings2} label="设置" collapsed={collapsed} />
          </nav>
          <button
            className="focus-ring m-2 flex items-center gap-3 rounded-lg p-2 text-[var(--muted)] hover:bg-[var(--soft)]"
            onClick={() => setCollapsed((value) => !value)}
            aria-label={collapsed ? "展开导航" : "折叠导航"}
          >
            {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
            {!collapsed && <span>收起导航</span>}
          </button>
        </aside>
        <div className={cn("workspace-body transition-[margin]", collapsed ? "ml-16" : "ml-56")}>
          <header
            className={cn(
              "sticky top-0 z-10 flex h-16 items-center justify-end",
              "border-b border-[var(--border)] bg-[color:var(--surface)]/90 px-6 backdrop-blur",
            )}
          >
            <div className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="icon"
                disabled={!settings.data}
                onClick={() =>
                  appearance.mutate({ theme: dark ? "light" : "dark", motion }, { onError: (err) => toast.error(err.message) })
                }
                aria-label="切换主题"
              >
                {dark ? <Sun size={18} /> : <Moon size={18} />}
              </Button>
              <Button variant="ghost" size="icon" onClick={() => setAIOpen(true)} aria-label="打开 AI">
                <Bot size={19} />
              </Button>
            </div>
          </header>
          <main className="workspace-main mx-auto w-full">
            <Suspense
              fallback={
                <div className="space-y-4">
                  <Skeleton className="h-9 w-48" />
                  <Skeleton className="h-72" />
                </div>
              }
            >
              <Routes>
                <Route index element={<DashboardPage />} />
                <Route path="notifications" element={<NotificationsPage />} />
                <Route path="settings" element={<SettingsPage />} />
                {navigation.map((item) => {
                  const Page = pageRegistry[item.pageKey];
                  return Page ? <Route key={item.pageKey} path={item.route.replace(/^\//, "")} element={<Page />} /> : null;
                })}
                <Route path="apps/:moduleId/:pageName" element={<ExternalPage />} />
                <Route path="*" element={<UnknownPage />} />
              </Routes>
            </Suspense>
          </main>
        </div>
        <AIPanel open={aiOpen} onClose={() => setAIOpen(false)} />
      </div>
    </ThemeContext.Provider>
  );
}

function SideLink({
  to,
  icon: Icon,
  label,
  collapsed,
  badge,
}: {
  to: string;
  icon: React.ComponentType<{ size?: number }>;
  label: string;
  collapsed: boolean;
  badge?: number;
}) {
  return (
    <NavLink
      to={to}
      end={to === "/"}
      aria-label={badge ? `${label}，${badge} 条未读` : label}
      className={({ isActive }) =>
        cn("workspace-nav-link focus-ring flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition", isActive && "is-active")
      }
    >
      <Icon size={18} />
      {!collapsed && <span className="sidebar-label flex-1">{label}</span>}
      {!collapsed && badge ? <span className="sidebar-label nav-badge">{badge}</span> : null}
      {/* Shown only when the sidebar is icon-only (collapsed or narrow screens). */}
      <span className="workspace-nav-tooltip" aria-hidden="true">
        {badge ? `${label} · ${badge}` : label}
      </span>
      {badge ? <span className="workspace-nav-dot" aria-hidden="true" /> : null}
    </NavLink>
  );
}

function UnknownPage() {
  return (
    <div className="py-20 text-center">
      <h1 className="text-xl font-semibold">页面不可用</h1>
      <p className="mt-2 text-[var(--muted)]">业务页面未注册或当前版本不兼容。</p>
    </div>
  );
}
