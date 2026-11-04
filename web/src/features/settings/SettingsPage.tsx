import { useState, type FormEvent } from "react";
import { Navigate, useSearchParams } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import { Blocks, Check, Fingerprint, KeyRound, LoaderCircle, Monitor, Moon, Paintbrush, Radio, Sun } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { useAppearance, useSettings, type Settings } from "./queries";
import { ModulesPanel } from "./ModulesPanel";
import { TelegramRecipientPanel } from "./TelegramRecipientPanel";
import { DeviceSessionsPanel } from "./DeviceSessionsPanel";
import { SettingsLayout, SettingsRow, SettingsSection } from "./SettingsLayout";
import { useAuth, useIsAdmin } from "../../shared/auth";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";

const sections = [
  { id: "appearance", title: "外观", icon: Paintbrush },
  { id: "modules", title: "业务", icon: Blocks },
  { id: "notifications", title: "通知推送", icon: Radio },
  { id: "security", title: "账户安全", icon: Fingerprint },
] as const;

export default function SettingsPage() {
  const isAdmin = useIsAdmin();
  const [params] = useSearchParams();
  // Only unambiguous management bookmarks move; business links still open personal settings.
  if (isAdmin && ["ai", "users", "data"].includes(params.get("tab") ?? "")) {
    const next = new URLSearchParams(params);
    next.delete("module");
    return <Navigate to={`/admin/settings?${next}`} replace />;
  }
  return <PersonalSettings />;
}

function PersonalSettings() {
  const auth = useAuth();
  const settings = useSettings();
  if (settings.isPending) return <div className="page space-y-4"><Skeleton className="h-12" /><Skeleton className="h-64" /></div>;
  if (settings.isError) return (
    <Notice tone="danger" title="设置加载失败" action={<Button onClick={() => settings.refetch()}>重试</Button>}>
      {settings.error.message}
    </Notice>
  );
  return (
    <SettingsLayout title="个人设置" sections={sections}>
      <SettingsSection id="appearance" title="外观"><AppearanceForm value={settings.data} /></SettingsSection>
      <SettingsSection id="modules" title="业务"><ModulesPanel /></SettingsSection>
      <SettingsSection id="notifications" title="通知推送"><TelegramRecipientPanel /></SettingsSection>
      <SettingsSection id="security" title="账户安全">
        <PasswordForm mode={auth.mode} />
        {auth.mode === "password" && <DeviceSessionsPanel />}
      </SettingsSection>
    </SettingsLayout>
  );
}

function PasswordForm({ mode }: { mode: string }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const categories = [/\p{Lu}/u, /\p{Ll}/u, /\p{Nd}/u, /[\p{P}\p{S}]/u].filter((pattern) => pattern.test(newPassword)).length;
  const longEnough = [...newPassword].length >= 8;
  const change = useMutation({
    mutationFn: () => api("/api/settings/password", { method: "PUT", body: JSON.stringify({ currentPassword, newPassword }) }),
    onSuccess: () => { window.location.assign("/"); },
    onError: (err) => toast.error(err.message),
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (newPassword !== confirm) {
      toast.error("两次输入的新密码不一致");
      return;
    }
    change.mutate();
  };
  if (mode === "local") return <SettingsRow label="登录方式"><Badge>本机免登录</Badge></SettingsRow>;
  return (
    <form className="studio-form" onSubmit={submit}>
      <div className="control-heading"><div><h3>修改登录密码</h3></div></div>
      <fieldset className="form-fields" disabled={change.isPending}>
        <Field label="当前密码">
          <input
            type="password" autoComplete="current-password" value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)} required
          />
        </Field>
        <div className="field-pair">
          <Field label="新密码">
            <input type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} required />
          </Field>
          <Field label="确认新密码" error={confirm && confirm !== newPassword ? "两次输入的密码不一致" : undefined}>
            <input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </Field>
        </div>
        <div className="password-rules">
          <span className={longEnough ? "satisfied" : ""}><Check size={13} />至少 8 个字符</span>
          <span className={categories >= 3 ? "satisfied" : ""}><Check size={13} />大小写字母、数字、符号四选三</span>
        </div>
      </fieldset>
      <div className="form-actions">
        <button className="studio-button" disabled={change.isPending || !currentPassword || !longEnough || categories < 3 || newPassword !== confirm}>
          {change.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <KeyRound size={16} />}更新密码
        </button>
      </div>
    </form>
  );
}

function AppearanceForm({ value }: { value: Settings }) {
  const save = useAppearance();
  const { theme, motion, palette = "default" } = value.appearance;
  const palettes = [
    { id: "default", title: "宝蓝", color: "#2962ff" },
    { id: "claude", title: "陶橙", color: "#cc785c" },
  ] as const;
  const themes = [
    { id: "light", title: "浅色", icon: Sun },
    { id: "dark", title: "深色", icon: Moon },
    { id: "system", title: "跟随系统", icon: Monitor },
  ] as const;
  const apply = (next: Settings["appearance"]) => save.mutate(next, { onError: (err) => toast.error(err.message) });
  return (
    <div className="settings-rows">
      <SettingsRow label="风格">
        <div className="settings-segment" role="group" aria-label="风格">
          {palettes.map((item) => (
            <button
              key={item.id} type="button" className="focus-ring" aria-pressed={palette === item.id}
              onClick={() => apply({ theme, motion, palette: item.id })}
            >
              <span className="palette-swatch" style={{ background: item.color }} aria-hidden="true" />{item.title}
            </button>
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label="主题">
        <div className="settings-segment" role="group" aria-label="主题">
          {themes.map(({ id, title, icon: Icon }) => (
            <button key={id} type="button" className="focus-ring" aria-pressed={theme === id} onClick={() => apply({ theme: id, motion, palette })}>
              <Icon size={14} aria-hidden="true" />{title}
            </button>
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label="减少动态效果">
        <Switch
          label="减少动态效果" checked={motion === "reduced"} disabled={save.isPending}
          onChange={(checked) => apply({ theme, motion: checked ? "reduced" : "full", palette })}
        />
      </SettingsRow>
    </div>
  );
}
