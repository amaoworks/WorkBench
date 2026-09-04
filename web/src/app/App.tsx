import { useQuery } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";
import { api } from "../shared/api";
import { Skeleton } from "../components/ui/States";
import { LoginPage } from "./LoginPage";
import { Layout } from "./Layout";

type AuthStatus = { authenticated: boolean; mode: "local" | "password" };

export function App() {
  const auth = useQuery({ queryKey: ["auth"], queryFn: () => api<AuthStatus>("/api/auth/status"), staleTime: 60_000 });
  if (auth.isLoading) return <div className="mx-auto mt-24 max-w-md space-y-4 px-6"><Skeleton className="h-10" /><Skeleton className="h-64" /></div>;
  if (auth.isError) return <div className="grid min-h-screen place-items-center p-6 text-center"><div><h1 className="text-xl font-semibold">无法连接 Workbench</h1><p className="mt-2 text-[var(--muted)]">请确认后端已启动，然后刷新页面。</p></div></div>;
  if (!auth.data?.authenticated) return <LoginPage />;
  return (
    <Routes>
      <Route path="/*" element={<Layout />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

