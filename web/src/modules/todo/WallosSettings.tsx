import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { saveWallosSettings, syncWallos, useRefreshTodo, useWallosSettings } from "./queries";
import type { WallosSettings as Settings } from "./schema";
import { formatDateTime } from "../../shared/format";
import { useReportDirty } from "../../shared/settingsDialog";

export default function WallosSettings({ enabled }: { enabled: boolean }) {
  const settings = useWallosSettings(enabled);
  if (!enabled) return <Notice title="待办业务已停用">启用后可配置 Wallos 联动。</Notice>;
  if (settings.isPending) return <Skeleton className="h-64" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="Wallos 设置读取失败">
        {settings.error.message}
      </Notice>
    );
  return <WallosForm initial={settings.data} />;
}

function WallosForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState({
    enabled: initial.enabled,
    baseUrl: initial.baseUrl,
    apiKey: "",
    daysBefore: initial.daysBefore,
    reminderHour: initial.reminderHour,
    timeZone: initial.timeZone,
  });
  const [dirty, setDirty] = useState(false);
  useReportDirty(dirty);
  const change = (patch: Partial<typeof form>) => {
    setForm((value) => ({ ...value, ...patch }));
    setDirty(true);
  };
  const refresh = useRefreshTodo();
  const save = useMutation({
    mutationFn: () => saveWallosSettings(form),
    onSuccess: async () => {
      setForm((value) => ({ ...value, apiKey: "" }));
      setDirty(false);
      await refresh();
      toast.success("Wallos 设置已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  const sync = useMutation({
    mutationFn: syncWallos,
    onSuccess: async (result) => {
      await refresh();
      toast.success(`同步完成，新建 ${result.created} 条提醒待办`);
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
  return (
    <form onSubmit={submit} className="space-y-4">
      <h3 className="font-medium">Wallos 订阅提醒</h3>
      <fieldset disabled={save.isPending || sync.isPending} className="space-y-4">
        <div className="flex items-center gap-2 text-sm">
          <Switch label="启用自动同步" checked={form.enabled} onChange={(enabled) => change({ enabled })} />
          启用自动同步
        </div>
        <Field label="Wallos 地址">
          <input
            type="url"
            required={form.enabled}
            value={form.baseUrl}
            onChange={(event) => change({ baseUrl: event.target.value })}
            placeholder="https://wallos.example.com"
          />
        </Field>
        <Field label="API Key" meta={initial.hasApiKey ? <Badge tone="neutral">已保存 · 留空保留</Badge> : undefined}>
          <input
            type="password"
            autoComplete="new-password"
            value={form.apiKey}
            onChange={(event) => change({ apiKey: event.target.value })}
            placeholder={initial.hasApiKey ? "输入新密钥以替换" : "填写 Wallos 的 API Key"}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="提前天数">
            <input
              type="number"
              min={0}
              max={90}
              required
              value={form.daysBefore}
              onChange={(event) => change({ daysBefore: event.target.valueAsNumber })}
            />
          </Field>
          <Field label="提醒小时（0–23）">
            <input
              type="number"
              min={0}
              max={23}
              required
              value={form.reminderHour}
              onChange={(event) => change({ reminderHour: event.target.valueAsNumber })}
            />
          </Field>
        </div>
        <Field label="时区">
          <input
            required
            value={form.timeZone}
            onChange={(event) => change({ timeZone: event.target.value })}
            placeholder="Asia/Shanghai"
          />
        </Field>
      </fieldset>
      {initial.lastSync && <Badge tone="neutral">最近同步：{formatDateTime(initial.lastSync)}</Badge>}
      {initial.lastError && (
        <Notice tone="danger" title="最近同步失败">
          {initial.lastError}
        </Notice>
      )}
      <div className="flex flex-wrap gap-2">
        <Button disabled={save.isPending || sync.isPending}>{save.isPending ? "保存中…" : "保存"}</Button>
        <Button
          type="button"
          variant="secondary"
          disabled={dirty || !initial.enabled || save.isPending || sync.isPending}
          onClick={() => sync.mutate()}
        >
          {sync.isPending ? "同步中…" : "立即同步"}
        </Button>
      </div>
      {dirty && <Badge tone="warning">有未保存的修改</Badge>}
    </form>
  );
}
