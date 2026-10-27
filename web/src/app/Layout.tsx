import { unreadCountSchema, type WorkbenchModule } from "../shared/schema";
import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { Bell, CheckSquare2, LayoutDashboard } from "lucide-react";
import { Route, Routes, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { apiValidated } from "../shared/api";
import { ThemeContext } from "../shared/theme";
import { Button, ButtonLink } from "../components/ui/Button";
import { Notice } from "../components/ui/Notice";
import { Skeleton } from "../components/ui/States";
import { useModules } from "./queries";
import { AIPanel } from "../features/ai/AIPanel";
import { useNotificationStream } from "../features/notifications/useNotificationStream";
import { useSettings, useAppearance } from "../features/settings/queries";
import { toast } from "sonner";
import { TopNav } from "./TopNav";
import { matchesLocation, notificationsMenu, overviewMenu, settingsIcon, settingsMenu, type NavEntry } from "./navigation";

const DashboardPage = lazy(() => import("../features/dashboard/DashboardPage"));
const NotificationsPage = lazy(() => import("../features/notifications/NotificationsPage"));
const SettingsPage = lazy(() => import("../features/settings/SettingsPage"));

import { pageRegistry, iconRegistry, menuRegistry, subpageRegistry } from "../modules/registry";
import { ExternalPage } from "./ExternalPage";

const available = (item: WorkbenchModule) =>
  item.enabled && (item.kind === "external" || (item.observedEnabled === true && !item.pending && !item.lastError));

export function Layout() {
  const modules = useModules();
  const [aiOpen, setAIOpen] = useState(false);
  const settings = useSettings();
  const appearance = useAppearance();
  const location = useLocation();
  const theme = settings.data?.appearance.theme ?? "system";
  const motion = settings.data?.appearance.motion ?? "full";
  const palette = settings.data?.appearance.palette ?? "default";
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
    document.documentElement.dataset.palette = palette;
  }, [palette]);
  useEffect(() => {
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => setSystemDark(query.matches);
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  // Ctrl+J (⌘J on macOS) toggles the AI panel from anywhere, including inside text fields.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.altKey || event.shiftKey || event.key.toLowerCase() !== "j") return;
      event.preventDefault();
      setAIOpen((value) => !value);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  const navigation = useMemo(
    () =>
      modules.data?.items
        .filter(available)
        .flatMap((item) => item.navigation.map((nav) => ({ ...nav, icon: item.icon, moduleId: item.id })))
        .sort((a, b) => a.order - b.order) ?? [],
    [modules.data],
  );
  const entries = useMemo<NavEntry[]>(
    () => [
      { key: "overview", label: "总览", to: "/", icon: LayoutDashboard, menu: overviewMenu },
      ...navigation.map((item) => ({
        key: item.pageKey,
        label: item.label,
        to: item.route,
        icon: iconRegistry[item.icon] ?? CheckSquare2,
        menu: menuRegistry[item.moduleId] ?? [],
      })),
      { key: "notifications", label: "通知", to: "/notifications", icon: Bell, badge: unread.data?.count, menu: notificationsMenu },
      { key: "settings", label: "设置", to: "/settings", icon: settingsIcon, menu: settingsMenu },
    ],
    [navigation, unread.data?.count],
  );
  const context = pageContext(entries, location.pathname, location.search);
  return (
    <ThemeContext.Provider value={dark ? "dark" : "light"}>
      <div className="workbench-shell min-h-screen">
        <TopNav
          entries={entries}
          onOpenAI={() => setAIOpen(true)}
          theme={theme}
          themeDisabled={!settings.data}
          onTheme={(next) => appearance.mutate({ theme: next, motion, palette }, { onError: (err) => toast.error(err.message) })}
        />
        <div className="workspace-body">
          <main className="workspace-main mx-auto w-full">
            <Suspense fallback={<PageSkeleton />}>
              <Routes>
                <Route index element={<DashboardPage />} />
                <Route path="notifications" element={<NotificationsPage />} />
                <Route path="settings" element={<SettingsPage />} />
                {navigation.map((item) => {
                  const Page = pageRegistry[item.pageKey];
                  return Page ? <Route key={item.pageKey} path={item.route.replace(/^\//, "")} element={<Page />} /> : null;
                })}
                {navigation.flatMap((item) =>
                  Object.entries(subpageRegistry[item.moduleId] ?? {}).map(([path, Page]) => (
                    <Route key={`${item.pageKey}/${path}`} path={`${item.route.replace(/^\/|\/$/g, "")}/${path}`} element={<Page />} />
                  )),
                )}
                <Route path="apps/:moduleId/:pageName" element={<ExternalPage />} />
                <Route path="*" element={<RouteFallback modules={modules} />} />
              </Routes>
            </Suspense>
          </main>
        </div>
        <AIPanel
          open={aiOpen}
          onClose={() => setAIOpen(false)}
          context={context ? { route: location.pathname + location.search, label: context } : undefined}
        />
      </div>
    </ThemeContext.Provider>
  );
}

function pageContext(entries: NavEntry[], pathname: string, search: string) {
  const entry = [...entries].reverse().find((item) => (item.to === "/" ? pathname === "/" : pathname === item.to || pathname.startsWith(`${item.to}/`)));
  if (!entry) return undefined;
  const sub = entry.menu.find((item) => matchesLocation(item.to, pathname, search));
  return sub && sub.label !== entry.label ? `${entry.label} · ${sub.label}` : entry.label;
}

function PageSkeleton() {
  return (
    <div className="space-y-4" aria-busy="true" aria-label="页面加载中">
      <Skeleton className="h-9 w-48" />
      <Skeleton className="h-72" />
    </div>
  );
}

/**
 * Routes are built from /api/modules, so a direct visit to a module page lands here until the module list
 * arrives or while the module is still starting. Explain which state it is instead of a dead end.
 */
function RouteFallback({ modules }: { modules: ReturnType<typeof useModules> }) {
  const { pathname } = useLocation();
  if (modules.isPending) return <PageSkeleton />;
  if (modules.isError)
    return (
      <Notice tone="danger" title="业务列表加载失败" action={<Button variant="secondary" onClick={() => modules.refetch()}>重试</Button>}>
        {modules.error.message}
      </Notice>
    );
  const owner = modules.data.items.find((item) =>
    item.navigation.some((nav) => pathname === nav.route || pathname.startsWith(`${nav.route.replace(/\/$/, "")}/`)),
  );
  const settingsLink = (
    <ButtonLink variant="secondary" to={owner ? `/settings?tab=modules&module=${encodeURIComponent(owner.id)}` : "/settings?tab=modules"}>
      业务设置
    </ButtonLink>
  );
  if (owner && !owner.enabled)
    return (
      <Notice title={`${owner.name}未启用`} action={settingsLink}>
        在业务设置中启用后即可使用此页面。
      </Notice>
    );
  if (owner?.lastError)
    return (
      <Notice tone="danger" title={`${owner.name}运行异常`} action={settingsLink}>
        {owner.lastError}
      </Notice>
    );
  if (owner && (owner.pending || (owner.kind === "builtin" && owner.observedEnabled !== true)))
    return (
      <div className="space-y-4" aria-busy="true">
        <Notice title={`${owner.name}正在启动`}>启动完成后页面会自动打开。</Notice>
        <Skeleton className="h-72" />
      </div>
    );
  if (owner)
    return (
      <Notice tone="warning" title="页面暂不可用" action={settingsLink}>
        当前版本的界面不包含此页面，请刷新或更新后再试。
      </Notice>
    );
  return (
    <div className="py-20 text-center">
      <h1 className="text-xl font-semibold">页面不存在</h1>
      <p className="mt-2 text-[var(--muted)]">地址可能已变更。</p>
      <ButtonLink className="mt-6" variant="secondary" to="/">
        返回总览
      </ButtonLink>
    </div>
  );
}
