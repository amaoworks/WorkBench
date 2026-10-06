import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { disconnectSchwab, refreshSchwabToken, saveSchwabSettings, useRefreshInvestment, useSchwabSettings } from "./queries";
import type { SchwabSettings as Settings } from "./schema";

export default function SchwabSettings({ enabled }: { enabled: boolean }) {
  const settings = useSchwabSettings(enabled);
  if (!enabled) return <Notice title="投资业务已停用">启用后可配置 Charles Schwab。</Notice>;
  if (settings.isPending) return <Skeleton className="h-64" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="Schwab 设置读取失败">
        {settings.error.message}
      </Notice>
    );
  return <SchwabForm initial={settings.data} />;
}

function SchwabForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState({
    appKey: initial.appKey,
    appSecret: "",
    callbackUrl: initial.callbackUrl || (window.location.protocol === "https:" ? `${window.location.origin}/oauth/schwab` : ""),
  });
  const [dirty, setDirty] = useState(false);
  const change = (patch: Partial<typeof form>) => {
    setForm((value) => ({ ...value, ...patch }));
    setDirty(true);
  };
  const refresh = useRefreshInvestment();
  const save = useMutation({
    mutationFn: () => saveSchwabSettings(form),
    onSuccess: async () => {
      setForm((value) => ({ ...value, appSecret: "" }));
      setDirty(false);
      await refresh();
      toast.success("Schwab 设置已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  const disconnect = useMutation({
    mutationFn: disconnectSchwab,
    onSuccess: async () => {
      await refresh();
      toast.success("已断开 Schwab");
    },
    onError: (error) => toast.error(error.message),
  });
  const token = useMutation({
    mutationFn: refreshSchwabToken,
    onSuccess: async () => {
      await refresh();
      toast.success("访问令牌已刷新");
    },
    onError: async (error) => {
      await refresh();
      toast.error(error.message);
    },
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    save.mutate();
  }
  const busy = save.isPending || disconnect.isPending || token.isPending;
  return (
    <form onSubmit={submit} className="space-y-4">
      <h3 className="font-medium">Charles Schwab</h3>
      <fieldset disabled={busy} className="space-y-4">
        <Field label="App Key">
          <input required value={form.appKey} onChange={(event) => change({ appKey: event.target.value })} autoComplete="off" />
        </Field>
        <Field label="App Secret" meta={initial.hasAppSecret ? <Badge tone="success">已保存 · 留空保留</Badge> : undefined}>
          <input
            type="password"
            autoComplete="new-password"
            value={form.appSecret}
            onChange={(event) => change({ appSecret: event.target.value })}
            placeholder={initial.hasAppSecret ? "输入新密钥以替换" : "填写 Schwab App Secret"}
          />
        </Field>
        <Field label="OAuth 回调地址">
          <input
            type="url"
            required
            value={form.callbackUrl}
            onChange={(event) => change({ callbackUrl: event.target.value })}
            placeholder="https://workbench.example.com/oauth/schwab"
          />
        </Field>
      </fieldset>
      {initial.connected && (
        <Badge tone="success">
          已连接{initial.tokenExpiresAt ? ` · 令牌到期 ${new Date(initial.tokenExpiresAt).toLocaleString()}` : ""}
        </Badge>
      )}
      {initial.reauthorizationRequired && (
        <Notice tone="warning" title="Schwab 授权已失效">
          行情和交易已暂停。
        </Notice>
      )}
      {!initial.connected && !initial.reauthorizationRequired && initial.hasAppSecret && (
        <Notice title="未连接 Schwab">请完成 OAuth 授权。</Notice>
      )}
      {initial.callbackUrl && !initial.callbackUrl.startsWith("https://") && (
        <Notice tone="danger" title="回调地址无效">
          回调地址须为 HTTPS。
        </Notice>
      )}
      {initial.lastError && !initial.reauthorizationRequired && (
        <Notice tone="danger" title="Schwab 连接异常">
          {initial.lastError}
        </Notice>
      )}
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy}>{save.isPending ? "保存中…" : "保存"}</Button>
        <Button
          type="button"
          variant={initial.reauthorizationRequired ? "primary" : "secondary"}
          disabled={busy || dirty || !initial.hasAppSecret || !initial.callbackUrl.startsWith("https://")}
          onClick={() => {
            window.location.href = "/api/modules/investment/schwab/oauth/login";
          }}
        >
          {initial.reauthorizationRequired ? "重新授权" : "登录 Schwab"}
        </Button>
        <Button type="button" variant="secondary" disabled={busy || dirty || !initial.connected} onClick={() => token.mutate()}>
          {token.isPending ? "刷新中…" : "刷新令牌"}
        </Button>
        <Button type="button" variant="secondary" disabled={busy || !initial.connected} onClick={() => disconnect.mutate()}>
          断开
        </Button>
      </div>
      {dirty && <Badge tone="warning">有未保存的修改</Badge>}
    </form>
  );
}
