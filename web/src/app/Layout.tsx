import { lazy, Suspense, useEffect, useMemo, useState } from "react";
import { Bell, Bot, CheckSquare2, Command, LayoutDashboard, Moon, PanelLeftClose, PanelLeftOpen, Sun } from "lucide-react";
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

const DashboardPage = lazy(() => import("../features/dashboard/DashboardPage"));
const NotificationsPage = lazy(() => import("../features/notifications/NotificationsPage"));
const TodoPage = lazy(() => import("../modules/todo/TodoPage"));

const pageRegistry: Record<string, React.LazyExoticComponent<React.ComponentType>> = {
  "todo.list": TodoPage
};

const iconRegistry = { "check-square": CheckSquare2 } as const;

export function Layout() {
  const modules = useModules();
  const [collapsed, setCollapsed] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  const [aiOpen, setAIOpen] = useState(false);
  const [dark, setDark] = useState(() => localStorage.getItem("workbench-theme") === "dark");
  useNotificationStream();
  const unread = useQuery({ queryKey: ["notifications", "count"], queryFn: () => api<{ count: number }>("/api/notifications/unread-count") });
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("workbench-theme", dark ? "dark" : "light");
  }, [dark]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); setCommandOpen(true); }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const navigation = useMemo(() => modules.data?.items.filter((item) => item.enabled).flatMap((item) => item.navigation.map((nav) => ({ ...nav, icon: item.icon }))).sort((a, b) => a.order - b.order) ?? [], [modules.data]);
  return (
    <div className="min-h-screen bg-[#f7f8fc] text-slate-800 dark:bg-slate-950 dark:text-slate-100">
      <aside className={cn("fixed inset-y-0 left-0 z-20 flex flex-col border-r border-[var(--border)] bg-[var(--surface)] transition-[width]", collapsed ? "w-16" : "w-56")}>
        <div className="flex h-16 items-center gap-3 border-b border-[var(--border)] px-4"><div className="grid size-8 shrink-0 place-items-center rounded-lg bg-indigo-500 font-bold text-white">W</div>{!collapsed && <span className="font-semibold tracking-tight">Workbench</span>}</div>
        <nav className="flex-1 space-y-1 p-2" aria-label="主导航">
          <SideLink to="/" icon={LayoutDashboard} label="总览" collapsed={collapsed} />
          {navigation.map((item) => { const Icon = iconRegistry[item.icon as keyof typeof iconRegistry] ?? CheckSquare2; return <SideLink key={item.pageKey} to={item.route} icon={Icon} label={item.label} collapsed={collapsed} />; })}
          <SideLink to="/notifications" icon={Bell} label="通知" collapsed={collapsed} badge={unread.data?.count} />
        </nav>
        <button className="focus-ring m-2 flex items-center gap-3 rounded-lg p-2 text-[var(--muted)] hover:bg-slate-100 dark:hover:bg-slate-800" onClick={() => setCollapsed((value) => !value)} aria-label={collapsed ? "展开导航" : "折叠导航"}>{collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}{!collapsed && <span>收起导航</span>}</button>
      </aside>
      <div className={cn("transition-[margin]", collapsed ? "ml-16" : "ml-56")}>
        <header className="sticky top-0 z-10 flex h-16 items-center justify-between border-b border-[var(--border)] bg-[color:var(--surface)]/90 px-6 backdrop-blur">
          <button onClick={() => setCommandOpen(true)} className="focus-ring flex w-72 items-center gap-2 rounded-lg border border-[var(--border)] px-3 py-2 text-left text-sm text-[var(--muted)]"><Command size={16} /><span className="flex-1">搜索或执行命令</span><kbd className="rounded border border-[var(--border)] px-1.5 text-[10px]">⌘K</kbd></button>
          <div className="flex items-center gap-1"><Button variant="ghost" size="icon" onClick={() => setDark((value) => !value)} aria-label="切换主题">{dark ? <Sun size={18} /> : <Moon size={18} />}</Button><Button variant="ghost" size="icon" onClick={() => setAIOpen(true)} aria-label="打开 AI"><Bot size={19} /></Button></div>
        </header>
        <main className="mx-auto max-w-7xl p-6 lg:p-8">
          <Suspense fallback={<div className="space-y-4"><Skeleton className="h-9 w-48" /><Skeleton className="h-72" /></div>}>
            <Routes>
              <Route index element={<DashboardPage />} />
              <Route path="notifications" element={<NotificationsPage />} />
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
  return <NavLink to={to} end={to === "/"} title={collapsed ? label : undefined} className={({ isActive }) => cn("focus-ring flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition", isActive ? "bg-indigo-50 font-medium text-indigo-600 dark:bg-indigo-500/10 dark:text-indigo-300" : "text-[var(--muted)] hover:bg-slate-100 dark:hover:bg-slate-800")}><Icon size={18} />{!collapsed && <span className="flex-1">{label}</span>}{!collapsed && badge ? <span className="rounded-full bg-red-500 px-1.5 text-[10px] text-white">{badge}</span> : null}</NavLink>;
}

function UnknownPage() { return <div className="py-20 text-center"><h1 className="text-xl font-semibold">页面不可用</h1><p className="mt-2 text-[var(--muted)]">模块页面未注册或当前版本不兼容。</p></div>; }

