import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Aperture, ArrowRight, LoaderCircle } from "lucide-react";
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
    <main className="login-page">
      <Card className="login-card">
        <div className="login-brand"><div className="brand-mark"><Aperture size={22} /></div><span>Workbench</span></div>
        <h1>欢迎回来</h1>
        <p>登录你的个人工作空间。</p>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <label className="studio-field"><span>密码</span><input autoFocus type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} required /></label>
          {login.isError && <p className="text-sm text-danger" role="alert">{login.error.message}</p>}
          <Button className="w-full" disabled={!password || login.isPending}>{login.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <ArrowRight size={16} />}{login.isPending ? "登录中…" : "登录"}</Button>
        </form>
      </Card>
    </main>
  );
}
