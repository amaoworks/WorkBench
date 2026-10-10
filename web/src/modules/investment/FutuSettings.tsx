import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Switch } from "../../components/ui/Switch";
import { saveFutuSettings, useRefreshInvestment } from "./queries";
import type { FutuSettings as Settings } from "./schema";
import { MaskedInput } from "./MaskedInput";

export type FutuDraft = { enabled: boolean; account: string; password: string };

const SERVICE_STATES: Record<Settings["serviceState"], string> = {
  installing: "正在安装服务…",
  starting: "正在启动服务…",
  running: "正在连接行情…",
  stopping: "正在停止服务…",
  error: "服务启动失败。",
  stopped: "服务已停止。",
  external: "正在等待服务连接…",
};

export function FutuSection({
  initial,
  value,
  onChange,
  dirty,
  error,
  onCleared,
}: {
  initial: Settings;
  value: FutuDraft;
  onChange: (patch: Partial<FutuDraft>) => void;
  dirty: boolean;
  error?: string;
  onCleared: () => void;
}) {
  const refresh = useRefreshInvestment();
  const [confirmClear, setConfirmClear] = useState(false);
  const clearButton = useRef<HTMLButtonElement>(null);
  const clear = useMutation({
    mutationFn: () => saveFutuSettings({ enabled: false, account: initial.account, password: "", clearPassword: true }),
    onSuccess: async () => {
      await refresh();
      onCleared();
      setConfirmClear(false);
      toast.success("已清除富途牛牛密码");
    },
    onError: (failure) => {
      setConfirmClear(false);
      toast.error(failure.message);
    },
  });
  const retry = useMutation({
    mutationFn: () => saveFutuSettings({ enabled: initial.enabled, account: initial.account, password: "", clearPassword: false }),
    onSuccess: async () => {
      await refresh();
      toast.success("富途牛牛启动请求已提交");
    },
    onError: (failure) => toast.error(failure.message),
  });
  const busy = clear.isPending || retry.isPending;
  return (
    <section className="space-y-4" aria-labelledby="futu-settings-title">
      <h3 id="futu-settings-title" className="font-medium">
        富途牛牛行情
      </h3>
      {error && (
        <Notice tone="danger" title="富途牛牛设置未保存">
          {error}
        </Notice>
      )}
      <div className="form-fields">
        <div className="flex items-center gap-2 text-sm">
          <Switch
            label="启用富途牛牛行情"
            checked={value.enabled}
            disabled={busy || (!initial.managed && !value.enabled)}
            onChange={(next) => onChange({ enabled: next })}
          />
          启用富途牛牛行情
        </div>
        <Field label="富途牛牛账号">
          <MaskedInput
            revealLabel="富途牛牛账号"
            value={value.account}
            onChange={(event) => onChange({ account: event.target.value })}
            placeholder="牛牛号、手机号或邮箱"
            maxLength={256}
            required={dirty && value.enabled}
          />
        </Field>
        <Field
          label="登录密码"
          meta={
            <Badge tone={initial.hasPassword ? "success" : "neutral"}>{initial.hasPassword ? "已保存 · 留空保留" : "尚未设置"}</Badge>
          }
        >
          <input
            type="password"
            autoComplete="new-password"
            value={value.password}
            onChange={(event) => onChange({ password: event.target.value })}
            maxLength={1024}
            required={dirty && value.enabled && (!initial.hasPassword || value.account !== initial.account)}
            placeholder={initial.hasPassword ? "输入新密码以替换" : "输入富途牛牛登录密码"}
          />
        </Field>
      </div>
      {!initial.managed && (
        <Notice title="富途牛牛登录服务未接入">{initial.hasPassword ? "账号密码已保存。" : "账号密码可先保存。"}</Notice>
      )}
      {initial.enabled && (
        <Notice title="富途牛牛行情状态">
          {initial.connected ? (initial.qotLogined ? "行情已连接。" : "服务已启动，等待行情登录。") : SERVICE_STATES[initial.serviceState]}
        </Notice>
      )}
      {initial.serviceError && (
        <Notice tone="danger" title="富途牛牛服务异常">
          {initial.serviceError}
        </Notice>
      )}
      {!initial.serviceError && initial.lastError && (
        <Notice tone="warning" title="行情连接尚未就绪">
          系统将自动重试。
        </Notice>
      )}
      {(initial.hasPassword || (!dirty && initial.serviceState === "error")) && (
        <div className="flex flex-wrap gap-2">
          {!dirty && initial.serviceState === "error" && (
            <Button type="button" variant="secondary" disabled={busy} onClick={() => retry.mutate()}>
              {retry.isPending ? "启动中…" : "重试启动"}
            </Button>
          )}
          {initial.hasPassword && (
            <Button ref={clearButton} type="button" variant="secondary" disabled={busy} onClick={() => setConfirmClear(true)}>
              清除已保存密码并停用
            </Button>
          )}
        </div>
      )}
      <ConfirmDialog
        open={confirmClear}
        title="清除富途牛牛密码？"
        description="将清除已保存的登录密码，并停用富途牛牛行情。"
        busy={clear.isPending}
        confirmLabel="清除并停用"
        busyLabel="清除中…"
        onCancel={() => setConfirmClear(false)}
        onConfirm={() => clear.mutate()}
        onClosed={() => clearButton.current?.focus()}
      />
    </section>
  );
}
