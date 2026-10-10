import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { disconnectSchwab, refreshSchwabToken, useRefreshInvestment } from "./queries";
import type { SchwabSettings as Settings } from "./schema";
import { formatDateTime } from "../../shared/format";
import { MaskedInput } from "./MaskedInput";

export type SchwabDraft = { appKey: string; appSecret: string; callbackUrl: string };

export function SchwabSection({
  initial,
  value,
  onChange,
  dirty,
  blockNavigation,
  error,
}: {
  initial: Settings;
  value: SchwabDraft;
  onChange: (patch: Partial<SchwabDraft>) => void;
  dirty: boolean;
  /** Any unsaved edit in the dialog; OAuth login leaves the page and would lose it. */
  blockNavigation: boolean;
  error?: string;
}) {
  const refresh = useRefreshInvestment();
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);
  const disconnectButton = useRef<HTMLButtonElement>(null);
  const disconnect = useMutation({
    mutationFn: disconnectSchwab,
    onSuccess: async () => {
      await refresh();
      setConfirmDisconnect(false);
      toast.success("已断开 Schwab");
    },
    onError: (failure) => {
      setConfirmDisconnect(false);
      toast.error(failure.message);
    },
  });
  const token = useMutation({
    mutationFn: refreshSchwabToken,
    onSuccess: async () => {
      await refresh();
      toast.success("访问令牌已刷新");
    },
    onError: async (failure) => {
      await refresh();
      toast.error(failure.message);
    },
  });
  const busy = disconnect.isPending || token.isPending;
  return (
    <section className="space-y-4" aria-labelledby="schwab-settings-title">
      <h3 id="schwab-settings-title" className="font-medium">
        Charles Schwab
      </h3>
      {error && (
        <Notice tone="danger" title="Schwab 设置未保存">
          {error}
        </Notice>
      )}
      <div className="space-y-4">
        <Field label="App Key">
          <MaskedInput revealLabel="App Key" required={dirty} value={value.appKey} onChange={(event) => onChange({ appKey: event.target.value })} />
        </Field>
        <Field label="App Secret" meta={initial.hasAppSecret ? <Badge tone="success">已保存 · 留空保留</Badge> : undefined}>
          <input
            type="password"
            autoComplete="new-password"
            value={value.appSecret}
            onChange={(event) => onChange({ appSecret: event.target.value })}
            placeholder={initial.hasAppSecret ? "输入新密钥以替换" : "填写 Schwab App Secret"}
          />
        </Field>
        <Field label="OAuth 回调地址">
          <input
            type="url"
            required={dirty}
            value={value.callbackUrl}
            onChange={(event) => onChange({ callbackUrl: event.target.value })}
            placeholder="https://workbench.example.com/oauth/schwab"
          />
        </Field>
      </div>
      {initial.connected && (
        <Badge tone="success">
          已连接{initial.tokenExpiresAt ? ` · 令牌到期 ${formatDateTime(initial.tokenExpiresAt)}` : ""}
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
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant={initial.reauthorizationRequired ? "primary" : "secondary"}
          disabled={busy || blockNavigation || !initial.hasAppSecret || !initial.callbackUrl.startsWith("https://")}
          onClick={() => {
            window.location.href = "/api/modules/investment/schwab/oauth/login";
          }}
        >
          {initial.reauthorizationRequired ? "重新授权" : "登录 Schwab"}
        </Button>
        <Button type="button" variant="secondary" disabled={busy || dirty || !initial.connected} onClick={() => token.mutate()}>
          {token.isPending ? "刷新中…" : "刷新令牌"}
        </Button>
        <Button
          ref={disconnectButton}
          type="button"
          variant="secondary"
          disabled={busy || !initial.connected}
          onClick={() => setConfirmDisconnect(true)}
        >
          断开
        </Button>
        {blockNavigation && <span className="text-xs text-[var(--muted)]">保存修改后可登录 Schwab</span>}
      </div>
      <ConfirmDialog
        open={confirmDisconnect}
        title="断开 Schwab？"
        description="断开后行情和交易将暂停，需要重新登录 Schwab 授权。"
        busy={disconnect.isPending}
        confirmLabel="断开"
        busyLabel="断开中…"
        onCancel={() => setConfirmDisconnect(false)}
        onConfirm={() => disconnect.mutate()}
        onClosed={() => disconnectButton.current?.focus()}
      />
    </section>
  );
}
