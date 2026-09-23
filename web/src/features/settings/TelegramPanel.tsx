import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api, apiValidated } from "../../shared/api";
import { Button } from "../../components/ui/Button";
import { useTelegramSettings } from "./queries";
import { telegramTestSchema, type TelegramSettings } from "./schema";

export function TelegramPanel() {
  const settings = useTelegramSettings();
  if (settings.isPending) return <p role="status">正在读取 Telegram 配置…</p>;
  if (settings.isError) return <p role="alert">{settings.error.message}</p>;
  const value = settings.data;
  return <div className="space-y-4">
    <TelegramForm key={`${value.enabled}:${value.chatId}:${value.hasBotToken}`} value={value} />
    <div className="studio-form"><h3>最近投递状态</h3>
      <p className="mt-2 text-sm">最近尝试：{value.status.lastAttemptAt ? new Date(value.status.lastAttemptAt).toLocaleString() : "尚未发送"}</p>
      <p className="mt-2 text-sm">最近成功：{value.status.lastSuccessAt ? new Date(value.status.lastSuccessAt).toLocaleString() : "尚无成功记录"}</p>
      {value.status.lastError && <p role="alert" className="mt-2 text-danger">{value.status.lastError}</p>}
      {value.failedDeliveries > 0 && <p className="mt-2 text-danger">{value.failedDeliveries} 条投递已失败，请检查配置。</p>}
    </div>
  </div>;
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
  const save = useMutation({ mutationFn: () => api("/api/settings/telegram", { method: "PUT", body: JSON.stringify(payload()) }),
    onSuccess: async () => { setBotToken(""); setClearBotToken(false); toast.success("Telegram 配置已保存"); await refresh(); }, onError: (error) => toast.error(error.message) });
  const test = useMutation({ mutationFn: () => apiValidated("/api/settings/telegram/test", telegramTestSchema, { method: "POST", body: JSON.stringify(payload()) }),
    onSuccess: () => setTestResult("测试消息已发送"), onError: (error) => setTestResult(error.message), onSettled: refresh });
  const busy = save.isPending || test.isPending;
  return <form className="studio-form" onSubmit={(event) => { event.preventDefault(); save.mutate(); }}>
    <div className="control-heading"><div><h3>Telegram 推送</h3></div></div>
    <fieldset className="form-fields" disabled={busy} onChange={() => setTestResult("")}>
      <label className="check-option"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />启用推送</label>
      <label className="studio-field"><span>Bot Token <span className="field-meta">{value.hasBotToken && !clearBotToken ? "已保存" : "未设置"}</span></span>
        <input type="password" autoComplete="new-password" value={botToken} maxLength={512} onChange={(event) => { setBotToken(event.target.value); setClearBotToken(false); }} placeholder={value.hasBotToken && !clearBotToken ? "留空保持不变" : "从 @BotFather 获取"} />
      </label>
      <label className="studio-field"><span>Chat ID</span><input aria-label="Chat ID" value={chatId} onChange={(event) => setChatId(event.target.value)} maxLength={64} placeholder="个人/群组数字 ID 或 @频道用户名" />
      </label>
      {value.hasBotToken && <label className="check-option"><input type="checkbox" checked={clearBotToken} onChange={(event) => { setClearBotToken(event.target.checked); if (event.target.checked) { setBotToken(""); setEnabled(false); } }} />清除 Token 并停用</label>}
    </fieldset>
    <div className="form-actions"><Button type="button" variant="secondary" disabled={busy} onClick={() => test.mutate()}>{test.isPending ? "发送中…" : "发送测试消息"}</Button><Button disabled={busy}>{save.isPending ? "保存中…" : "保存配置"}</Button></div>
    {testResult && <p role="status" className={`inline-result ${test.isError ? "is-error" : ""}`}>{testResult}</p>}
  </form>;
}
