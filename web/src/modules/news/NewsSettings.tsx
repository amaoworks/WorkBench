import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { saveNewsSettings, useNewsSettings, useRefreshNews } from "./queries";
import type { NewsSettings as Settings } from "./schema";

export default function NewsSettings({ enabled }: { enabled: boolean }) {
  const settings = useNewsSettings(enabled);
  if (!enabled) return <p className="text-sm text-[var(--muted)]">启用资讯模块后可以配置采集、AI 和简报。</p>;
  if (settings.isPending) return <p role="status">正在读取资讯配置…</p>;
  if (settings.isError) return <p role="alert" className="text-danger">{settings.error.message}</p>;
  return <SettingsForm initial={settings.data} />;
}

function SettingsForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState(initial);
  const change = (patch: Partial<Settings>) => setForm((current) => ({ ...current, ...patch }));
  const refresh = useRefreshNews();
  const save = useMutation({
    mutationFn: () => saveNewsSettings(form),
    onSuccess: (saved) => { setForm(saved); void refresh(); toast.success("资讯配置已保存"); },
    onError: (error) => toast.error(error.message)
  });
  function submit(event: FormEvent) { event.preventDefault(); save.mutate(); }

  return <form onSubmit={submit} className="space-y-5">
    <section className="space-y-3">
      <h3 className="font-medium">AI 处理</h3>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.autoAI} onChange={(event) => change({ autoAI: event.target.checked })} />自动分析新资讯</label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="block text-sm">目标语言<input required value={form.targetLanguage} onChange={(event) => change({ targetLanguage: event.target.value })} placeholder="zh-CN" className="ui-input mt-1 w-full" /></label>
        <label className="block text-sm">每日 AI 上限<input type="number" min={0} max={1000} required value={form.dailyAILimit} onChange={(event) => change({ dailyAILimit: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
      </div>
    </section>
    <section className="space-y-3">
      <h3 className="font-medium">存储</h3>
      <label className="block text-sm">保留天数<input type="number" min={1} max={3650} required value={form.retentionDays} onChange={(event) => change({ retentionDays: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
    </section>
    <section className="space-y-3">
      <h3 className="font-medium">定时简报</h3>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.digestEnabled} onChange={(event) => change({ digestEnabled: event.target.checked })} />启用定时简报</label>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <label className="block text-sm">小时<input type="number" min={0} max={23} required value={form.digestHour} onChange={(event) => change({ digestHour: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
        <label className="block text-sm">分钟<input type="number" min={0} max={59} required value={form.digestMinute} onChange={(event) => change({ digestMinute: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
        <label className="col-span-2 block text-sm">时区<input required value={form.timeZone} onChange={(event) => change({ timeZone: event.target.value })} placeholder="Asia/Shanghai" className="ui-input mt-1 w-full" /></label>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="block text-sm">最多条数<input type="number" min={1} max={30} required value={form.digestMaxItems} onChange={(event) => change({ digestMaxItems: event.target.valueAsNumber })} className="ui-input mt-1 w-full" /></label>
        <label className="block text-sm">主题<input value={form.digestTopic} onChange={(event) => change({ digestTopic: event.target.value })} placeholder="全部主题" className="ui-input mt-1 w-full" /></label>
      </div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.pushEnabled} onChange={(event) => change({ pushEnabled: event.target.checked })} />生成后提交推送</label>
      <Link className="text-sm text-[var(--brand)] hover:underline" to="/settings?tab=notifications">配置 Telegram</Link>
    </section>
    <Button disabled={save.isPending}>{save.isPending ? "保存中…" : "保存配置"}</Button>
  </form>;
}
