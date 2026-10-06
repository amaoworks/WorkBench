import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { saveFutuSettings, useFutuSettings, useRefreshInvestment } from "./queries";
import type { FutuSettings as Settings } from "./schema";

export default function FutuSettings({ enabled }: { enabled: boolean }) {
  const settings = useFutuSettings(enabled);
  if (!enabled) return null;
  if (settings.isPending) return <Skeleton className="h-56" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="富途牛牛设置读取失败">
        {settings.error.message}
      </Notice>
    );
  const { enabled: active, account, hasPassword, managed } = settings.data;
  return <FutuForm key={JSON.stringify([active, account, hasPassword, managed])} initial={settings.data} />;
}

function FutuForm({ initial }: { initial: Settings }) {
  const [enabled, setEnabled] = useState(initial.enabled);
  const [account, setAccount] = useState(initial.account);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const dirty = enabled !== initial.enabled || account !== initial.account || password !== "" || clearPassword;
  const refresh = useRefreshInvestment();
  const save = useMutation({
    mutationFn: () => saveFutuSettings({ enabled, account, password, clearPassword }),
    onSuccess: async () => {
      setPassword("");
      setClearPassword(false);
      await refresh();
      toast.success(enabled ? "富途牛牛启动请求已提交" : "富途牛牛设置已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
      className="space-y-4"
    >
      <h3 className="font-medium">富途牛牛行情</h3>
      <fieldset disabled={save.isPending} className="form-fields">
        <div className="flex items-center gap-2 text-sm">
          <Switch
            label="启用富途牛牛行情"
            checked={enabled}
            disabled={!initial.managed && !enabled}
            onChange={(next) => {
              setEnabled(next);
              if (next) setClearPassword(false);
            }}
          />
          启用富途牛牛行情
        </div>
        <Field label="富途牛牛账号">
          <input
            autoComplete="username"
            value={account}
            onChange={(event) => setAccount(event.target.value)}
            placeholder="牛牛号、手机号或邮箱"
            maxLength={256}
            required={enabled}
          />
        </Field>
        <Field
          label="登录密码"
          meta={
            <Badge tone={initial.hasPassword && !clearPassword ? "success" : "neutral"}>
              {initial.hasPassword && !clearPassword ? "已保存 · 留空保留" : "尚未设置"}
            </Badge>
          }
        >
          <input
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(event) => {
              setPassword(event.target.value);
              setClearPassword(false);
            }}
            maxLength={1024}
            required={enabled && (!initial.hasPassword || account !== initial.account)}
            placeholder={initial.hasPassword ? "输入新密码以替换" : "输入富途牛牛登录密码"}
          />
        </Field>
        {initial.hasPassword && (
          <div className="flex items-center gap-2 text-sm">
            <Switch
              label="清除已保存密码并停用富途牛牛行情"
              checked={clearPassword}
              onChange={(next) => {
                setClearPassword(next);
                if (next) {
                  setPassword("");
                  setEnabled(false);
                }
              }}
            />
            清除已保存密码并停用
          </div>
        )}
      </fieldset>
      {!initial.managed && (
        <Notice title="富途牛牛登录服务未接入">{initial.hasPassword ? "账号密码已保存。" : "账号密码可先保存。"}</Notice>
      )}
      {initial.enabled && (
        <Notice title="富途牛牛行情状态">
          {initial.connected
            ? initial.qotLogined
              ? "行情已连接。"
              : "服务已启动，等待行情登录。"
            : {
                installing: "正在安装服务…",
                starting: "正在启动服务…",
                running: "正在连接行情…",
                stopping: "正在停止服务…",
                error: "服务启动失败。",
                stopped: "服务已停止。",
                external: "正在等待服务连接…",
              }[initial.serviceState]}
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
      <div className="form-actions">
        {dirty && <Badge tone="warning">有未保存的修改</Badge>}
        <Button disabled={save.isPending || (!dirty && initial.serviceState !== "error")}>
          {save.isPending ? "保存中…" : !dirty && initial.serviceState === "error" ? "重试启动" : "保存"}
        </Button>
      </div>
    </form>
  );
}
