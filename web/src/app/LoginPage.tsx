import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LockKeyhole } from "lucide-react";
import { api } from "../shared/api";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";

export function LoginPage() {
  const [password, setPassword] = useState("");
  const queryClient = useQueryClient();
  const login = useMutation({
    mutationFn: () => api("/api/auth/login", { method: "POST", body: JSON.stringify({ password }) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["auth"] })
  });
  function submit(event: FormEvent) { event.preventDefault(); login.mutate(); }
  return (
    <main className="grid min-h-screen place-items-center bg-[radial-gradient(circle_at_top,#eef2ff,transparent_45%)] p-6 dark:bg-[radial-gradient(circle_at_top,#1e1b4b,transparent_45%)]">
      <Card className="w-full max-w-sm p-7">
        <div className="mb-6 grid size-11 place-items-center rounded-xl bg-indigo-500 text-white"><LockKeyhole size={20} /></div>
        <h1 className="text-xl font-semibold">欢迎回到 Workbench</h1>
        <p className="mt-1 text-sm text-[var(--muted)]">输入本地工作空间密码继续。</p>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <label className="block"><span className="mb-1.5 block text-xs font-medium">密码</span><input autoFocus type="password" value={password} onChange={(event) => setPassword(event.target.value)} className="focus-ring w-full rounded-lg border border-[var(--border)] bg-transparent px-3 py-2.5" /></label>
          {login.isError && <p className="text-sm text-red-500">{login.error.message}</p>}
          <Button className="w-full" disabled={!password || login.isPending}>{login.isPending ? "登录中…" : "登录"}</Button>
        </form>
      </Card>
    </main>
  );
}

