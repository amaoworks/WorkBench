import { useState } from "react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { formatDateTime } from "../../shared/format";
import { useSaveTelegramRecipient, useTelegramRecipient, useTelegramRecipientMapping, useTestTelegramRecipient } from "./queries";
import type { TelegramRecipient } from "./schema";

export function TelegramRecipientPanel() {
  const query = useTelegramRecipient();
  if (query.isPending) return <Skeleton className="h-40" />;
  if (query.isError) return <Notice tone="danger" title="接收设置加载失败">{query.error.message}</Notice>;
  const value = query.data;
  return <RecipientForm key={`${value.enabled}:${value.chatId}:${value.configured}`} value={value} />;
}

export function TelegramRecipientMapping({ userId }: { userId: string }) {
  const query = useTelegramRecipientMapping(userId);
  if (query.isPending) return <Skeleton className="h-32" />;
  if (query.isError) return <Notice tone="danger" title="接收目标加载失败">{query.error.message}</Notice>;
  const value = query.data;
  return <RecipientForm key={`${value.enabled}:${value.chatId}:${value.configured}`} value={value} userId={userId} />;
}

function RecipientForm({ value, userId }: { value: TelegramRecipient; userId?: string }) {
  const [enabled, setEnabled] = useState(value.enabled);
  const [chatId, setChatId] = useState(value.chatId);
  const save = useSaveTelegramRecipient(userId);
  const test = useTestTelegramRecipient();
  const busy = save.isPending || test.isPending;
  const dirty = enabled !== value.enabled || chatId !== value.chatId || (!!userId && !!chatId && !value.configured);
  return (
    <form className="studio-form space-y-4" onSubmit={(event) => {
      event.preventDefault();
      save.mutate({ enabled, ...(userId ? { chatId } : {}) }, {
        onSuccess: () => toast.success("接收设置已保存"), onError: (error) => toast.error(error.message),
      });
    }}>
      <div className="control-heading"><h3>{userId ? "Telegram 接收目标" : "个人推送"}</h3></div>
      {!userId && !value.configured && (
        <Notice title="Telegram 推送未就绪">{value.chatId ? "联系管理员检查服务和接收目标。" : "联系管理员分配接收目标。"}</Notice>
      )}
      <fieldset className="form-fields" disabled={busy}>
        <Field label="Chat ID" meta={!userId ? <Badge>管理员分配</Badge> : undefined}>
          <input
            value={chatId} readOnly={!userId} maxLength={64} onChange={(event) => setChatId(event.target.value)}
            placeholder={userId ? "个人/群组数字 ID 或 @频道用户名" : "尚未分配"}
          />
        </Field>
        <div className="settings-line">
          <span>接收提醒</span>
          <Switch
            label="接收 Telegram 提醒" checked={enabled} onChange={setEnabled}
            disabled={!userId && !value.configured && !enabled}
          />
        </div>
      </fieldset>
      <div className="form-actions">
        {!userId && (
          <Button type="button" variant="secondary" disabled={busy || dirty || !value.configured} onClick={() => test.mutate(undefined, {
            onSuccess: () => toast.success("测试消息已发送"), onError: (error) => toast.error(error.message),
          })}>{test.isPending ? "发送中…" : "发送测试消息"}</Button>
        )}
        <Button disabled={busy || !dirty}>{save.isPending ? "保存中…" : "保存"}</Button>
      </div>
      {!userId && (value.status.lastAttemptAt || value.status.lastSuccessAt) && (
        <dl className="studio-deployment-details">
          <div><dt>最近尝试</dt><dd>{value.status.lastAttemptAt ? formatDateTime(value.status.lastAttemptAt) : "尚未发送"}</dd></div>
          <div><dt>最近成功</dt><dd>{value.status.lastSuccessAt ? formatDateTime(value.status.lastSuccessAt) : "暂无成功记录"}</dd></div>
        </dl>
      )}
      {!userId && value.status.lastError && <Notice tone="danger" title="投递失败">{value.status.lastError}</Notice>}
    </form>
  );
}
