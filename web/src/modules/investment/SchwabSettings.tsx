import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { disconnectSchwab, refreshSchwabToken, saveSchwabSettings, useRefreshInvestment, useSchwabSettings } from "./queries";
import type { SchwabSettings as Settings } from "./schema";

export default function SchwabSettings({ enabled }: { enabled: boolean }) {
  const settings = useSchwabSettings(enabled);
  if (!enabled) return <p className="text-sm text-[var(--muted)]">启用投资后可以配置 Charles Schwab。停用期间终端连接也会关闭。</p>;
  if (settings.isPending) return <p role="status">正在读取 Schwab 配置…</p>;
  if (settings.isError) return <p role="alert" className="text-danger">{settings.error.message}</p>;
  return <SchwabForm initial={settings.data} />;
}

function SchwabForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState({ appKey: initial.appKey, appSecret: "", callbackUrl: initial.callbackUrl || (window.location.protocol === "https:" ? `${window.location.origin}/oauth/schwab` : "") });
  const [dirty, setDirty] = useState(false);
  const change = (patch: Partial<typeof form>) => { setForm((value) => ({ ...value, ...patch })); setDirty(true); };
  const refresh = useRefreshInvestment();
  const save = useMutation({
    mutationFn: () => saveSchwabSettings(form),
    onSuccess: () => { setForm((value) => ({ ...value, appSecret: "" })); setDirty(false); refresh(); toast.success("Schwab 配置已保存"); },
    onError: (error) => toast.error(error.message)
  });
  const disconnect = useMutation({
    mutationFn: disconnectSchwab,
    onSuccess: () => { refresh(); toast.success("已断开 Schwab"); },
    onError: (error) => toast.error(error.message)
  });
  const token = useMutation({
    mutationFn: refreshSchwabToken,
    onSuccess: () => { refresh(); toast.success("访问令牌已刷新"); },
    onError: (error) => toast.error(error.message)
  });
  function submit(event: FormEvent) { event.preventDefault(); save.mutate(); }
  const busy = save.isPending || disconnect.isPending || token.isPending;
  return <form onSubmit={submit} className="space-y-4">
    <h3 className="font-medium">Charles Schwab</h3>
    <fieldset disabled={busy} className="space-y-4">
      <label className="block text-sm">App Key<input required value={form.appKey} onChange={(e) => change({ appKey: e.target.value })} autoComplete="off" className="ui-input mt-1 w-full" /></label>
      <label className="block text-sm">App Secret<input type="password" autoComplete="new-password" value={form.appSecret} onChange={(e) => change({ appSecret: e.target.value })} placeholder={initial.hasAppSecret ? "已保存，留空保留" : "填写 Schwab App Secret"} className="ui-input mt-1 w-full" /></label>
      <label className="block text-sm">OAuth 回调地址<input type="url" required value={form.callbackUrl} onChange={(e) => change({ callbackUrl: e.target.value })} placeholder="https://workbench.example.com/oauth/schwab" className="ui-input mt-1 w-full" /></label>
    </fieldset>
    {initial.connected && <p className="text-xs text-[var(--muted)]">已连接{initial.tokenExpiresAt ? `，访问令牌到期 ${new Date(initial.tokenExpiresAt).toLocaleString()}` : ""}。</p>}
    {!initial.connected && initial.hasAppSecret && <p className="text-xs text-[var(--muted)]">配置已保存，尚未完成 OAuth 授权。</p>}
    {initial.callbackUrl && !initial.callbackUrl.startsWith("https://") && <p role="alert" className="text-sm text-danger">已保存的回调地址不是 HTTPS，请修正后再登录 Schwab。</p>}
    {initial.lastError && <p role="alert" className="text-sm text-danger">最近错误：{initial.lastError}</p>}
    <div className="flex flex-wrap gap-2">
      <Button disabled={busy}>{save.isPending ? "保存中…" : "保存配置"}</Button>
      <Button type="button" variant="secondary" disabled={busy || dirty || !initial.hasAppSecret || !initial.callbackUrl.startsWith("https://")} onClick={() => { window.location.href = "/api/modules/investment/schwab/oauth/login"; }}>登录 Schwab</Button>
      <Button type="button" variant="secondary" disabled={busy || dirty || !initial.connected} onClick={() => token.mutate()}>{token.isPending ? "刷新中…" : "刷新令牌"}</Button>
      <Button type="button" variant="secondary" disabled={busy || !initial.connected} onClick={() => disconnect.mutate()}>断开</Button>
    </div>
    {dirty && <p className="text-xs text-[var(--muted)]">保存配置后再登录。</p>}
  </form>;
}
