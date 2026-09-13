import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { disconnectFutu, saveFutuSettings, useFutuSettings, useRefreshInvestment } from "./queries";
import type { FutuSettings as Settings } from "./schema";

export default function FutuSettings({ enabled }: { enabled: boolean }) {
  const settings = useFutuSettings(enabled);
  if (!enabled) return null;
  if (settings.isPending) return <p role="status">正在读取富途 OpenD 配置…</p>;
  if (settings.isError) return <p role="alert" className="text-danger">{settings.error.message}</p>;
  return <FutuForm initial={settings.data} />;
}

function FutuForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState({
    host: initial.host, port: String(initial.port), enabled: initial.enabled, allowNonLocal: initial.allowNonLocal
  });
  const [dirty, setDirty] = useState(false);
  const change = (patch: Partial<typeof form>) => { setForm((value) => ({ ...value, ...patch })); setDirty(true); };
  const refresh = useRefreshInvestment();
  const save = useMutation({
    mutationFn: () => saveFutuSettings({
      host: form.host.trim(), port: Number(form.port), enabled: form.enabled, allowNonLocal: form.allowNonLocal
    }),
    onSuccess: () => { setDirty(false); refresh(); toast.success("富途 OpenD 配置已保存"); },
    onError: (error) => toast.error(error.message)
  });
  const disconnect = useMutation({
    mutationFn: disconnectFutu,
    onSuccess: () => { refresh(); toast.success("已关闭夜盘覆盖"); },
    onError: (error) => toast.error(error.message)
  });
  function submit(event: FormEvent) { event.preventDefault(); save.mutate(); }
  const busy = save.isPending || disconnect.isPending;
  return <form onSubmit={submit} className="space-y-4">
    <h3 className="font-medium">富途夜盘（OpenD）</h3>
    <p className="text-xs text-[var(--muted)]">只接入行情，不接入交易。历史 K 线优先嘉信；夜盘当前窗口走订阅，往日夜盘才用富途历史额度兜底。开关关闭后图表不再显示 Night / 24h。</p>
    <fieldset disabled={busy} className="space-y-4">
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.enabled} onChange={(e) => change({ enabled: e.target.checked })} />启用夜盘覆盖</label>
      <label className="block text-sm">OpenD 主机<input required value={form.host} onChange={(e) => change({ host: e.target.value })} autoComplete="off" className="ui-input mt-1 w-full" /></label>
      <label className="block text-sm">OpenD 端口<input required type="number" min={1} max={65535} value={form.port} onChange={(e) => change({ port: e.target.value })} className="ui-input mt-1 w-full" /></label>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.allowNonLocal} onChange={(e) => change({ allowNonLocal: e.target.checked })} />允许非本机地址（Docker 服务名如 futu-opend）</label>
    </fieldset>
    {initial.connected && <p className="text-xs text-[var(--muted)]">已连接 OpenD{initial.qotLogined ? "，行情已登录" : "，等待行情登录"}{initial.historyRemain != null ? `，历史额度剩余 ${initial.historyRemain}` : ""}。</p>}
    {initial.lastError && <p role="alert" className="text-sm text-danger">最近错误：{initial.lastError}</p>}
    <div className="flex flex-wrap gap-2">
      <Button disabled={busy}>{save.isPending ? "保存中…" : "保存配置"}</Button>
      <Button type="button" variant="secondary" disabled={busy || !initial.enabled} onClick={() => disconnect.mutate()}>关闭覆盖</Button>
    </div>
    {dirty && <p className="text-xs text-[var(--muted)]">保存后终端才会按新开关显示或隐藏 Night / 24h。</p>}
  </form>;
}
