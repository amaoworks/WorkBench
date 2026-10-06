import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { saveOvernightSettings, useOvernightSettings, useRefreshInvestment } from "./queries";

export default function OvernightSettings({ enabled }: { enabled: boolean }) {
  const settings = useOvernightSettings(enabled);
  if (!enabled) return null;
  if (settings.isPending) return <Skeleton className="h-32" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="夜盘设置读取失败">
        {settings.error.message}
      </Notice>
    );
  return (
    <OvernightForm
      key={`${settings.data.enabled}:${settings.data.providerEnabled}`}
      initial={settings.data.enabled}
      providerEnabled={settings.data.providerEnabled}
    />
  );
}

function OvernightForm({ initial, providerEnabled }: { initial: boolean; providerEnabled: boolean }) {
  const [enabled, setEnabled] = useState(initial);
  const refresh = useRefreshInvestment();
  const save = useMutation({
    mutationFn: () => saveOvernightSettings(enabled),
    onSuccess: async () => {
      await refresh();
      toast.success("夜盘设置已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <h3 className="font-medium">夜盘行情</h3>
      <div className="flex items-center gap-2 text-sm">
        <Switch label="启用夜盘行情" checked={enabled} disabled={!providerEnabled || save.isPending} onChange={setEnabled} />
        启用夜盘行情
      </div>
      {!providerEnabled && <Notice title="富途牛牛行情未启用">启用后可使用夜盘行情。</Notice>}
      <Button disabled={save.isPending || enabled === initial || !providerEnabled}>{save.isPending ? "保存中…" : "保存"}</Button>
    </form>
  );
}
