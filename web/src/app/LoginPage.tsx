import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Aperture, ArrowRight, LoaderCircle } from "lucide-react";
import { apiValidated } from "../shared/api";
import { loginResponseSchema } from "../shared/schema";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Field } from "../components/ui/Field";

export function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const queryClient = useQueryClient();
  const login = useMutation({
    mutationFn: () => apiValidated("/api/auth/login", loginResponseSchema, { method: "POST", body: JSON.stringify({ username, password }) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["auth"] }),
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    login.mutate();
  }
  return (
    <main className="login-page">
      <Card className="login-card">
        <div className="login-brand">
          <div className="brand-mark">
            <Aperture size={22} />
          </div>
          <span>Workbench</span>
        </div>
        <h1>欢迎回来</h1>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <Field label="用户名">
            <input
              autoFocus
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              required
              maxLength={64}
            />
          </Field>
          <Field label="密码" error={login.isError ? login.error.message : undefined}>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              required
            />
          </Field>
          <Button className="w-full" disabled={!username.trim() || !password || login.isPending}>
            {login.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <ArrowRight size={16} />}
            {login.isPending ? "登录中…" : "登录"}
          </Button>
        </form>
      </Card>
    </main>
  );
}
