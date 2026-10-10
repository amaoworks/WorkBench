import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, apiValidated } from "../../shared/api";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { Card, CardHeader } from "../../components/ui/Card";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useTelegramSettings } from "./queries";
import { telegramTestSchema, type TelegramSettings } from "./schema";
import { formatDateTime } from "../../shared/format";

export function TelegramPanel() {
  const settings = useTelegramSettings();
  if (settings.isPending) return <Skeleton className="h-48" />;
  if (settings.isError)
    return (
      <Notice tone="danger" title="Telegram 配置加载失败">
        {settings.error.message}
      </Notice>
    );
  const value = settings.data;
  return (
    <div className="space-y-4">
      <TelegramForm key={`${value.enabled}:${value.chatId}:${value.hasBotToken}`} value={value} />
      <Card>
        <CardHeader title="最近投递状态" />
        <div className="p-4">
          <p className="mt-2 text-sm">
            最近尝试：{value.status.lastAttemptAt ? formatDateTime(value.status.lastAttemptAt) : "尚未发送"}
          </p>
          <p className="mt-2 text-sm">
            最近成功：{value.status.lastSuccessAt ? formatDateTime(value.status.lastSuccessAt) : "暂无成功记录"}
          </p>
          {value.status.lastError && (
            <Notice tone="danger" title="投递失败" className="mt-3">
              {value.status.lastError}
            </Notice>
          )}
          {value.failedDeliveries > 0 && (
            <Badge tone="danger" className="mt-3">
              {value.failedDeliveries} 条投递失败
            </Badge>
          )}
        </div>
      </Card>
    </div>
  );
}

function TelegramForm({ value }: { value: TelegramSettings }) {
  const client = useQueryClient();
  const [enabled, setEnabled] = useState(value.enabled);
  const [chatId, setChatId] = useState(value.chatId);
  const [botToken, setBotToken] = useState("");
  const [clearBotToken, setClearBotToken] = useState(false);
  const [testResult, setTestResult] = useState("");
  const payload = () => ({ enabled, chatId, botToken, clearBotToken });
  const refresh = () => client.invalidateQueries({ queryKey: ["settings", "telegram"] });
  const save = useMutation({
    mutationFn: () => api("/api/settings/telegram", { method: "PUT", body: JSON.stringify(payload()) }),
    onSuccess: async () => {
      setBotToken("");
      setClearBotToken(false);
      toast.success("Telegram 配置已保存");
      await refresh();
    },
    onError: (error) => toast.error(error.message),
  });
  const test = useMutation({
    mutationFn: () => apiValidated("/api/settings/telegram/test", telegramTestSchema, { method: "POST", body: JSON.stringify(payload()) }),
    onSuccess: () => setTestResult("测试消息已发送"),
    onError: (error) => setTestResult(error.message),
    onSettled: refresh,
  });
  const busy = save.isPending || test.isPending;
  return (
    <form
      className="studio-form"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div className="control-heading">
        <h3>Telegram 推送</h3>
      </div>
      <fieldset className="form-fields" disabled={busy} onChange={() => setTestResult("")}>
        <div className="flex items-center justify-between gap-3 text-sm">
          <span>启用推送</span>
          <Switch
            label="启用推送"
            checked={enabled}
            onChange={(checked) => {
              setEnabled(checked);
              setTestResult("");
            }}
          />
        </div>
        <Field label="Bot Token" meta={<Badge>{value.hasBotToken && !clearBotToken ? "已保存" : "未设置"}</Badge>}>
          <input
            type="password"
            autoComplete="new-password"
            value={botToken}
            maxLength={512}
            onChange={(event) => {
              setBotToken(event.target.value);
              setClearBotToken(false);
            }}
            placeholder={value.hasBotToken && !clearBotToken ? "留空保持不变" : "从 @BotFather 获取"}
          />
        </Field>
        <Field label="Chat ID">
          <input
            value={chatId}
            onChange={(event) => setChatId(event.target.value)}
            maxLength={64}
            placeholder="个人/群组数字 ID 或 @频道用户名"
          />
        </Field>
        {value.hasBotToken && (
          <div className="flex items-center justify-between gap-3 text-sm">
            <span>清除 Token 并停用</span>
            <Switch
              label="清除 Token 并停用"
              checked={clearBotToken}
              onChange={(checked) => {
                setClearBotToken(checked);
                setTestResult("");
                if (checked) {
                  setBotToken("");
                  setEnabled(false);
                }
              }}
            />
          </div>
        )}
      </fieldset>
      <div className="form-actions">
        <Button type="button" variant="secondary" disabled={busy} onClick={() => test.mutate()}>
          {test.isPending ? "发送中…" : "发送测试消息"}
        </Button>
        <Button disabled={busy}>{save.isPending ? "保存中…" : "保存"}</Button>
      </div>
      {testResult && (
        <Notice title={test.isError ? "发送失败" : "发送成功"} tone={test.isError ? "danger" : "info"} className="mt-4">
          {testResult}
        </Notice>
      )}
    </form>
  );
}
