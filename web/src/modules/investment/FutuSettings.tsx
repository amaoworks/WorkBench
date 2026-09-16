import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { saveFutuSettings, useFutuSettings, useRefreshInvestment } from "./queries";
import type { FutuSettings as Settings } from "./schema";

export default function FutuSettings({ enabled }: { enabled: boolean }) {
  const settings = useFutuSettings(enabled);
  if (!enabled) return null;
  if (settings.isPending) return <p role="status">正在读取富途牛牛设置…</p>;
  if (settings.isError) return <p role="alert" className="text-danger">{settings.error.message}</p>;
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
    onSuccess: async () => { setPassword(""); setClearPassword(false); await refresh(); toast.success(enabled ? "富途牛牛启动请求已提交" : "富途牛牛配置已保存"); },
    onError: (error) => toast.error(error.message)
  });
  return <form onSubmit={(event) => { event.preventDefault(); save.mutate(); }} className="space-y-4">
    <h3 className="font-medium">富途牛牛</h3>
    <fieldset disabled={save.isPending} className="form-fields">
      <label className="check-option"><input type="checkbox" checked={enabled} disabled={!initial.managed && !enabled} onChange={(event) => { setEnabled(event.target.checked); if (event.target.checked) setClearPassword(false); }} />启用富途牛牛</label>
        <label className="studio-field"><span>富途牛牛账号</span><input autoComplete="username" value={account} onChange={(event) => setAccount(event.target.value)} placeholder="牛牛号、手机号或邮箱" maxLength={256} required={enabled} /></label>
        <label className="studio-field"><span>登录密码 <span className="field-meta">{initial.hasPassword && !clearPassword ? "已保存 · 留空保留" : "尚未设置"}</span></span><input type="password" autoComplete="new-password" value={password} onChange={(event) => { setPassword(event.target.value); setClearPassword(false); }} maxLength={1024} required={enabled && (!initial.hasPassword || account !== initial.account)} placeholder={initial.hasPassword ? "输入新密码以替换" : "输入富途牛牛登录密码"} /><small>保存后用于 OpenD 登录，不会回显。首次登录可能需要在 OpenD 完成短信或设备验证。</small></label>
        {initial.hasPassword && <label className="check-option"><input type="checkbox" checked={clearPassword} onChange={(event) => { setClearPassword(event.target.checked); if (event.target.checked) { setPassword(""); setEnabled(false); } }} />清除已保存密码并停用富途牛牛</label>}
    </fieldset>
    {!initial.managed && <p role="status" className="text-sm text-[var(--muted)]">{initial.hasPassword ? "账号密码已保存。" : "账号密码可先保存。"}当前部署尚未接入富途牛牛登录服务（OpenD），因此无法启用。</p>}
    {initial.enabled && <p role="status" className="text-sm text-[var(--muted)]">{initial.connected ? (initial.qotLogined ? "富途牛牛已连接，行情已登录" : "富途牛牛服务已启动，等待行情登录；首次登录可能需要短信或设备验证") : ({ installing: "首次启用，正在下载并安装富途牛牛服务…", starting: "正在启动富途牛牛服务…", running: "富途牛牛服务已启动，正在连接行情…", stopping: "正在停止富途牛牛服务…", error: "富途牛牛服务启动失败", stopped: "富途牛牛服务已停止", external: "正在等待富途牛牛服务连接…" }[initial.serviceState])}</p>}
    {initial.serviceError && <p role="alert" className="text-sm text-danger">{initial.serviceError}</p>}
    {!initial.serviceError && initial.lastError && <p role="alert" className="text-sm text-danger">行情连接尚未就绪，将自动重试。</p>}
    <p className="form-note">停用富途牛牛会同时关闭夜盘行情。重新启用富途牛牛后，需再次手动开启夜盘。</p>
    <div className="form-actions">{dirty && <span className="unsaved-label">有未保存的修改</span>}<Button disabled={save.isPending || (!dirty && initial.serviceState !== "error")}>{save.isPending ? "保存中…" : (!dirty && initial.serviceState === "error" ? "重试启动" : "保存富途牛牛配置")}</Button></div>
  </form>;
}
