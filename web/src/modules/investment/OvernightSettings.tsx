import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { saveOvernightSettings, useOvernightSettings, useRefreshInvestment } from "./queries";

export default function OvernightSettings({ enabled }: { enabled: boolean }) {
  const settings = useOvernightSettings(enabled);
  if (!enabled) return null;
  if (settings.isPending) return <p role="status">正在读取夜盘设置…</p>;
  if (settings.isError) return <p role="alert">{settings.error.message}</p>;
  return <OvernightForm key={`${settings.data.enabled}:${settings.data.providerEnabled}`} initial={settings.data.enabled} providerEnabled={settings.data.providerEnabled} />;
}

function OvernightForm({ initial, providerEnabled }: { initial: boolean; providerEnabled: boolean }) {
  const [enabled, setEnabled] = useState(initial);
  const refresh = useRefreshInvestment();
  const save = useMutation({ mutationFn: () => saveOvernightSettings(enabled), onSuccess: async () => { await refresh(); toast.success("夜盘设置已保存"); }, onError: (error) => toast.error(error.message) });
  return <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); save.mutate(); }}>
    <h3 className="font-medium">夜盘行情</h3>
    <p className="text-sm text-[var(--muted)]">美股夜盘功能，当前行情来源为富途牛牛。</p>
    <label className="check-option"><input type="checkbox" checked={enabled} disabled={!providerEnabled || save.isPending} onChange={(event) => setEnabled(event.target.checked)} />启用夜盘行情</label>
    {!providerEnabled && <p role="status" className="text-sm text-[var(--muted)]">请先在上方启用富途牛牛行情服务。</p>}
    <Button disabled={save.isPending || enabled === initial || !providerEnabled}>{save.isPending ? "保存中…" : "保存夜盘设置"}</Button>
  </form>;
}
