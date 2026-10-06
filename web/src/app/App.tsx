import { authStatusSchema } from "../shared/schema";
import { useQuery } from "@tanstack/react-query";
import { Navigate, Route, Routes } from "react-router-dom";
import { apiValidated } from "../shared/api";
import { Skeleton } from "../components/ui/States";
import { Notice } from "../components/ui/Notice";
import { Button } from "../components/ui/Button";
import { FirstPasswordPage } from "./FirstPasswordPage";
import { LoginPage } from "./LoginPage";
import { Layout } from "./Layout";

export function App() {
  const auth = useQuery({ queryKey: ["auth"], queryFn: () => apiValidated("/api/auth/status", authStatusSchema), staleTime: 60_000 });
  if (auth.isLoading)
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
  return (
    <Routes>
      <Route path="/*" element={<Layout />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
