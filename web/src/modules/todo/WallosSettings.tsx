import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { saveWallosSettings, syncWallos, useRefreshTodo, useWallosSettings } from "./queries";
import type { WallosSettings as Settings } from "./schema";

export default function WallosSettings({ enabled }: { enabled: boolean }) {
  const settings = useWallosSettings(enabled);
  if (!enabled) return <p className="text-sm text-[var(--muted)]">启用待办模块后可以配置 Wallos 联动。停用模块期间自动同步也会暂停。</p>;
  if (settings.isPending) return <p role="status">正在读取 Wallos 配置…</p>;
  if (settings.isError) return <p role="alert" className="text-danger">{settings.error.message}</p>;
  return <WallosForm initial={settings.data} />;
}

function WallosForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState({ enabled: initial.enabled, baseUrl: initial.baseUrl, apiKey: "", daysBefore: initial.daysBefore, reminderHour: initial.reminderHour, timeZone: initial.timeZone });
  const [dirty, setDirty] = useState(false);
  const change = (patch: Partial<typeof form>) => { setForm((value) => ({ ...value, ...patch })); setDirty(true); };
  const refresh = useRefreshTodo();
  const save = useMutation({
    mutationFn: () => saveWallosSettings(form),
    onSuccess: () => { setForm((value) => ({ ...value, apiKey: "" })); setDirty(false); refresh(); toast.success("Wallos 配置已保存"); },
    onError: (error) => toast.error(error.message)
  });
  const sync = useMutation({
    mutationFn: syncWallos,
    onSuccess: (result) => { refresh(); toast.success(`同步完成，新建 ${result.created} 条提醒待办`); },
    onError: (error) => { refresh(); toast.error(error.message); }
  });
  function submit(event: FormEvent) { event.preventDefault(); save.mutate(); }
  return <form onSubmit={submit} className="space-y-4">
    <div><h3 className="font-medium">Wallos 订阅提醒</h3><p className="mt-1 text-sm text-[var(--muted)]">每小时同步，将当月应付款的有效订阅提前加入待办，再按指定时间提醒。月付、季付、半年付、年付均按 Wallos 付款日期处理。</p></div>
    <fieldset disabled={save.isPending || sync.isPending} className="space-y-4">
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.enabled} onChange={(e) => change({ enabled: e.target.checked })} />启用自动同步</label>
      <label className="block text-sm">Wallos 地址<input type="url" required={form.enabled} value={form.baseUrl} onChange={(e) => change({ baseUrl: e.target.value })} placeholder="https://wallos.example.com" className="ui-input mt-1 w-full" /></label>
      <label className="block text-sm">API Key<input type="password" autoComplete="new-password" value={form.apiKey} onChange={(e) => change({ apiKey: e.target.value })} placeholder={initial.hasApiKey ? "已保存，留空保留" : "填写 Wallos 的 API Key"} className="ui-input mt-1 w-full" /></label>
      <p className="text-xs text-[var(--muted)]">API Key 只保存在服务端，不回显。更换地址需重新填写密钥。</p>
      <div className="grid grid-cols-2 gap-3">
        <label className="block text-sm">提前天数<input type="number" min={0} max={90} required value={form.daysBefore} onChange={(e) => change({ daysBefore: e.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
        <label className="block text-sm">提醒小时（0–23）<input type="number" min={0} max={23} required value={form.reminderHour} onChange={(e) => change({ reminderHour: e.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
      </div>
      <label className="block text-sm">时区<input required value={form.timeZone} onChange={(e) => change({ timeZone: e.target.value })} placeholder="Asia/Shanghai" className="ui-input mt-1 w-full" /></label>
    </fieldset>
    <p className="text-xs text-[var(--muted)]">同一账期不会重复生成；完成后保留，删除后可在下次同步重新生成。提前天数和提醒小时按所选时区计算，届时发送站内提醒。下月账单若提前提醒落在本月，也会及时加入。修改配置只影响尚未生成的待办。</p>
    {initial.lastSync && <p className="text-xs text-[var(--muted)]">最近成功同步：{new Date(initial.lastSync).toLocaleString()}</p>}
    {initial.lastError && <p role="alert" className="text-sm text-danger">最近同步失败：{initial.lastError}</p>}
    <div className="flex flex-wrap gap-2">
      <Button disabled={save.isPending || sync.isPending}>{save.isPending ? "保存中…" : "保存配置"}</Button>
      <Button type="button" variant="secondary" disabled={dirty || !initial.enabled || save.isPending || sync.isPending} onClick={() => sync.mutate()}>{sync.isPending ? "同步中…" : "立即同步"}</Button>
    </div>
    {dirty && <p className="text-xs text-[var(--muted)]">保存配置后即可立即同步。</p>}
  </form>;
}
