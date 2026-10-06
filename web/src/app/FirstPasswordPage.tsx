import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Aperture, Check, KeyRound, LoaderCircle } from "lucide-react";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { Field } from "../components/ui/Field";
import { Notice } from "../components/ui/Notice";
import { apiValidated } from "../shared/api";
import { reauthenticateSchema } from "../shared/schema";

export function FirstPasswordPage() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const categories = [/\p{Lu}/u, /\p{Ll}/u, /\p{Nd}/u, /[\p{P}\p{S}]/u].filter((pattern) => pattern.test(newPassword)).length;
  const longEnough = [...newPassword].length >= 8;
  const changed = newPassword !== currentPassword;
  const confirmed = newPassword === confirm;
  const change = useMutation({
    mutationFn: () =>
      apiValidated("/api/settings/password", reauthenticateSchema, {
        method: "PUT",
        body: JSON.stringify({ currentPassword, newPassword }),
      }),
    onSuccess: () => window.location.replace("/"),
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!longEnough || categories < 3 || !changed || !confirmed) return;
    change.mutate();
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
        <h1>账户安全</h1>
        <p>首次登录需要设置新密码。</p>
        <form onSubmit={submit} className="mt-6 space-y-4">
          <fieldset className="form-fields" disabled={change.isPending}>
            <Field label="当前密码">
              <input
                autoFocus
                type="password"
                autoComplete="current-password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
                required
              />
            </Field>
            <Field label="新密码" error={newPassword && !changed ? "新密码不能与当前密码相同" : undefined}>
              <input
                type="password"
                autoComplete="new-password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
                required
              />
            </Field>
            <Field label="确认新密码" error={confirm && !confirmed ? "两次输入的密码不一致" : undefined}>
              <input
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(event) => setConfirm(event.target.value)}
                required
              />
            </Field>
            <div className="password-rules">
              <span className={longEnough ? "satisfied" : ""}>
                <Check size={13} />
                至少 8 个字符
              </span>
              <span className={categories >= 3 ? "satisfied" : ""}>
                <Check size={13} />
                大小写字母、数字、符号四选三
              </span>
              <span className={changed && newPassword !== "" ? "satisfied" : ""}>
                <Check size={13} />
                不同于当前密码
              </span>
            </div>
          </fieldset>
          {change.isError && (
            <Notice tone="danger" title="密码更新失败">
              {change.error.message}
            </Notice>
          )}
          <Button
            className="w-full"
            disabled={change.isPending || !currentPassword || !longEnough || categories < 3 || !changed || !confirmed}
          >
            {change.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <KeyRound size={16} />}
            {change.isPending ? "更新中…" : "更新密码"}
          </Button>
        </form>
      </Card>
    </main>
  );
}
