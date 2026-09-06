import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { Bell, Bot, CheckSquare2, Command, LayoutDashboard, Moon, PanelLeftClose, PanelLeftOpen, Sun, Settings2, Aperture } from "lucide-react";
import { NavLink, Route, Routes } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../shared/api";
import { cn } from "../shared/cn";
import { Button } from "../components/ui/Button";
import { Skeleton } from "../components/ui/States";
import { useModules } from "./queries";
import { CommandPalette } from "./CommandPalette";
import { AIPanel } from "../features/ai/AIPanel";
import { useNotificationStream } from "../features/notifications/useNotificationStream";
import { useSettings, useAppearance } from "../features/settings/queries";
import { toast } from "sonner";

const DashboardPage = lazy(() => import("../features/dashboard/DashboardPage"));
const NotificationsPage = lazy(() => import("../features/notifications/NotificationsPage"));
const SettingsPage = lazy(() => import("../features/settings/SettingsPage"));

import { pageRegistry, iconRegistry } from "../modules/registry";

export function Layout() {
  const modules = useModules();
  const [collapsed, setCollapsed] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [aiOpen, setAIOpen] = useState(false);
  const settings = useSettings();
  const appearance = useAppearance();
  const theme = settings.data?.appearance.theme ?? "system";
  const motion = settings.data?.appearance.motion ?? "full";
  const [systemDark, setSystemDark] = useState(() => window.matchMedia("(prefers-color-scheme: dark)").matches);
  const dark = theme === "dark" || (theme === "system" && systemDark);
  useNotificationStream();
  const unread = useQuery({ queryKey: ["notifications", "count"], queryFn: () => api<{ count: number }>("/api/notifications/unread-count") });
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
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
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); setCommandOpen(true); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const navigation = useMemo(() => modules.data?.items.filter((item) => item.enabled).flatMap((item) => item.navigation.map((nav) => ({ ...nav, icon: item.icon }))).sort((a, b) => a.order - b.order) ?? [], [modules.data]);
  return (
    <div className="workbench-shell min-h-screen">
      <aside className={cn("workspace-sidebar fixed inset-y-0 left-0 z-20 flex flex-col border-r border-[var(--border)] bg-[var(--surface)] transition-[width]", collapsed ? "w-16" : "w-56")}>
        <div className="workspace-logo flex h-20 items-center gap-3 px-4"><div className="brand-mark"><Aperture size={23} strokeWidth={1.5} /></div>{!collapsed && <span className="sidebar-label font-semibold tracking-tight">Workbench<span className="brand-period">.</span></span>}</div>
        <nav className="flex-1 space-y-1 p-2" aria-label="主导航">
          <SideLink to="/" icon={LayoutDashboard} label="总览" collapsed={collapsed} />
          {navigation.map((item) => { const Icon = iconRegistry[item.icon] ?? CheckSquare2; return <SideLink key={item.pageKey} to={item.route} icon={Icon} label={item.label} collapsed={collapsed} />; })}
          <SideLink to="/notifications" icon={Bell} label="通知" collapsed={collapsed} badge={unread.data?.count} />
          <SideLink to="/settings" icon={Settings2} label="设置" collapsed={collapsed} />
        </nav>
        <button className="focus-ring m-2 flex items-center gap-3 rounded-lg p-2 text-[var(--muted)] hover:bg-[var(--soft)]" onClick={() => setCollapsed((value) => !value)} aria-label={collapsed ? "展开导航" : "折叠导航"}>{collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}{!collapsed && <span>收起导航</span>}</button>
      </aside>
      <div className={cn("workspace-body transition-[margin]", collapsed ? "ml-16" : "ml-56")}>
        <header className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-[var(--border)] bg-[color:var(--surface)]/90 px-6 backdrop-blur">
          <button onClick={() => setCommandOpen(true)} className="workspace-search focus-ring flex w-72 items-center gap-2 rounded-lg border border-[var(--border)] px-3 py-2 text-left text-sm text-[var(--muted)]"><Command size={16} /><span className="flex-1">搜索页面</span><kbd className="rounded border border-[var(--border)] px-1.5 text-xs">Ctrl / ⌘ K</kbd></button>
          <div className="flex items-center gap-1"><Button variant="ghost" size="icon" disabled={appearance.isPending || !settings.data} onClick={() => appearance.mutate({ theme: dark ? "light" : "dark", motion }, { onError: (err) => toast.error(err.message) })} aria-label="切换主题">{dark ? <Sun size={18} /> : <Moon size={18} />}</Button><Button variant="ghost" size="icon" onClick={() => setAIOpen(true)} aria-label="打开 AI"><Bot size={19} /></Button></div>
        </header>
        <main className="mx-auto max-w-7xl p-6 lg:p-8">
          <Suspense fallback={<div className="space-y-4"><Skeleton className="h-9 w-48" /><Skeleton className="h-72" /></div>}>
            <Routes>
              <Route index element={<DashboardPage />} />
              <Route path="notifications" element={<NotificationsPage />} />
              <Route path="settings" element={<SettingsPage />} />
              {navigation.map((item) => { const Page = pageRegistry[item.pageKey]; return Page ? <Route key={item.pageKey} path={item.route.replace(/^\//, "")} element={<Page />} /> : null; })}
              <Route path="*" element={<UnknownPage />} />
            </Routes>
          </Suspense>
        </main>
      </div>
      <CommandPalette open={commandOpen} onOpenChange={setCommandOpen} navigation={navigation} />
      <AIPanel open={aiOpen} onClose={() => setAIOpen(false)} />
    </div>
  );
}

function SideLink({ to, icon: Icon, label, collapsed, badge }: { to: string; icon: React.ComponentType<{ size?: number }>; label: string; collapsed: boolean; badge?: number }) {
  return <NavLink to={to} end={to === "/"} title={label} aria-label={label} className={({ isActive }) => cn("workspace-nav-link focus-ring flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition", isActive && "is-active")}><Icon size={18} />{!collapsed && <span className="sidebar-label flex-1">{label}</span>}{!collapsed && badge ? <span className="sidebar-label nav-badge">{badge}</span> : null}</NavLink>;
}

function UnknownPage() { return <div className="py-20 text-center"><h1 className="text-xl font-semibold">页面不可用</h1><p className="mt-2 text-[var(--muted)]">模块页面未注册或当前版本不兼容。</p></div>; }
