import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { saveNewsSettings, useNewsSettings, useRefreshNews } from "./queries";
import type { NewsSettings as Settings } from "./schema";

export default function NewsSettings({ enabled }: { enabled: boolean }) {
  const settings = useNewsSettings(enabled);
  if (!enabled) return <Notice title="资讯业务已停用">启用后可配置采集、AI 和简报。</Notice>;
  if (settings.isPending) return <Skeleton className="h-64" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="资讯设置读取失败">
        {settings.error.message}
      </Notice>
    );
  return <SettingsForm initial={settings.data} />;
}

function SettingsForm({ initial }: { initial: Settings }) {
  const [form, setForm] = useState(initial);
  const change = (patch: Partial<Settings>) => setForm((current) => ({ ...current, ...patch }));
  const refresh = useRefreshNews();
  const save = useMutation({
    mutationFn: () => saveNewsSettings(form),
    onSuccess: async (saved) => {
      setForm(saved);
      await refresh();
      toast.success("资讯设置已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    save.mutate();
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <section className="space-y-3">
        <h3 className="font-medium">AI 处理</h3>
        <div className="flex items-center gap-2 text-sm">
          <Switch label="自动分析新资讯" checked={form.autoAI} onChange={(autoAI) => change({ autoAI })} />
          自动分析新资讯
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="目标语言">
            <input
              required
              value={form.targetLanguage}
              onChange={(event) => change({ targetLanguage: event.target.value })}
              placeholder="zh-CN"
            />
          </Field>
          <Field label="每日 AI 上限">
            <input
              type="number"
              min={0}
              max={1000}
              required
              value={form.dailyAILimit}
              onChange={(event) => change({ dailyAILimit: event.target.valueAsNumber })}
            />
          </Field>
        </div>
      </section>
      <section className="space-y-3">
        <h3 className="font-medium">存储</h3>
        <Field label="保留天数">
          <input
            type="number"
            min={1}
            max={3650}
            required
            value={form.retentionDays}
            onChange={(event) => change({ retentionDays: event.target.valueAsNumber })}
          />
        </Field>
      </section>
      <section className="space-y-3">
        <h3 className="font-medium">定时简报</h3>
        <div className="flex items-center gap-2 text-sm">
          <Switch label="启用定时简报" checked={form.digestEnabled} onChange={(digestEnabled) => change({ digestEnabled })} />
          启用定时简报
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
          <Field label="小时">
            <input
              type="number"
              min={0}
              max={23}
              required
              value={form.digestHour}
              onChange={(event) => change({ digestHour: event.target.valueAsNumber })}
            />
          </Field>
          <Field label="分钟">
            <input
              type="number"
              min={0}
              max={59}
              required
              value={form.digestMinute}
              onChange={(event) => change({ digestMinute: event.target.valueAsNumber })}
            />
          </Field>
          <Field label="时区" className="sm:col-span-2">
            <input
              required
              value={form.timeZone}
              onChange={(event) => change({ timeZone: event.target.value })}
              placeholder="Asia/Shanghai"
            />
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="最多条数">
            <input
              type="number"
              min={1}
              max={30}
              required
              value={form.digestMaxItems}
              onChange={(event) => change({ digestMaxItems: event.target.valueAsNumber })}
            />
          </Field>
          <Field label="主题">
            <input value={form.digestTopic} onChange={(event) => change({ digestTopic: event.target.value })} placeholder="全部主题" />
          </Field>
        </div>
        <div className="flex items-center gap-2 text-sm">
          <Switch label="生成后提交推送" checked={form.pushEnabled} onChange={(pushEnabled) => change({ pushEnabled })} />
          生成后提交推送
        </div>
        <Link className="text-sm text-[var(--brand)] hover:underline" to="/settings?tab=notifications">
          配置 Telegram
        </Link>
      </section>
      <Button disabled={save.isPending}>{save.isPending ? "保存中…" : "保存"}</Button>
    </form>
  );
}
