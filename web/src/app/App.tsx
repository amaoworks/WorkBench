import { authStatusSchema } from "../shared/schema";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";
import { apiValidated, AUTH_CHANNEL, broadcastIdentity, resetIdentityRequests, setAPIIdentity } from "../shared/api";
import { AuthContext } from "../shared/auth";
import { Skeleton } from "../components/ui/States";
import { Notice } from "../components/ui/Notice";
import { Button } from "../components/ui/Button";
import { FirstPasswordPage } from "./FirstPasswordPage";
import { LoginPage } from "./LoginPage";
import { Layout } from "./Layout";

export function App() {
  const client = useQueryClient();
  const auth = useQuery({
    queryKey: ["auth"], queryFn: () => apiValidated("/api/auth/status", authStatusSchema),
    staleTime: 15_000, refetchInterval: 30_000, retry: false,
  });
  const userId = auth.data?.authenticated ? auth.data.user?.id ?? null : null;
  const role = auth.data?.user?.role;
  const previousUser = useRef<string | null | undefined>(undefined);
  const [accountChanged, setAccountChanged] = useState(false);
  useLayoutEffect(() => {
    if (!auth.isSuccess) return;
    if (userId) setAPIIdentity(userId);
    else resetIdentityRequests();
    if (previousUser.current !== undefined && previousUser.current !== userId) broadcastIdentity(userId);
    previousUser.current = userId;
  }, [auth.isSuccess, userId, role]);
  useEffect(() => {
    const refresh = () => { void client.invalidateQueries({ queryKey: ["auth"] }); };
    const replaceWorkspace = () => {
      resetIdentityRequests();
      setAccountChanged(true);
      window.location.replace("/");
    };
    window.addEventListener("workbench:auth-invalid", refresh);
    window.addEventListener("workbench:account-changed", replaceWorkspace);
    const channel = typeof BroadcastChannel === "undefined" ? undefined : new BroadcastChannel(AUTH_CHANNEL);
    if (channel) channel.onmessage = event => {
      const data: unknown = event.data;
      if (typeof data === "object" && data !== null && "type" in data && data.type === "identity" &&
          "userId" in data && (typeof data.userId === "string" || data.userId === null) && data.userId !== userId) {
        replaceWorkspace();
      }
    };
    return () => {
      window.removeEventListener("workbench:auth-invalid", refresh);
      window.removeEventListener("workbench:account-changed", replaceWorkspace);
      channel?.close();
    };
  }, [client, userId]);
  if (auth.isLoading || accountChanged)
    return (
      <div className="mx-auto mt-24 max-w-md space-y-4 px-6">
        <Skeleton className="h-10" />
        <Skeleton className="h-64" />
      </div>
    );
  if (auth.isError)
    return (
      <div className="grid min-h-screen place-items-center p-6">
        <Notice tone="danger" title="无法连接 Workbench" action={<Button onClick={() => auth.refetch()}>重试连接</Button>}>
          {auth.error.message}
        </Notice>
      </div>
    );
  if (!auth.data?.authenticated) return <LoginPage />;
  if (auth.data.mustChangePassword) return <FirstPasswordPage />;
  const identity = `${auth.data.user?.id}:${auth.data.user?.role}`;
  return (
    <AuthContext.Provider value={auth.data}>
      <UserWorkspace key={identity}>
        <Routes>
          <Route path="/*" element={<Layout />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </UserWorkspace>
    </AuthContext.Provider>
  );
}

function UserWorkspace({ children }: { children: ReactNode }) {
  // A new identity gets a separate cache; late answers can never populate the next user's workspace.
  const [client] = useState(() => new QueryClient({
    defaultOptions: {
      queries: { staleTime: 20_000, retry: 1, refetchOnWindowFocus: false },
      mutations: { retry: 0 },
    },
  }));
  const active = useRef(false);
  useLayoutEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
      resetIdentityRequests();
      void client.cancelQueries();
      // StrictMode replays effects without replacing the mounted workspace.
      queueMicrotask(() => { if (!active.current) client.clear(); });
      const root = document.documentElement;
      root.classList.toggle("dark", window.matchMedia("(prefers-color-scheme: dark)").matches);
      root.dataset.palette = "default";
      root.dataset.motion = "full";
    };
  }, [client]);
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
