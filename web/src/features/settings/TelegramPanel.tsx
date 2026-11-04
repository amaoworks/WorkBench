import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { apiValidated } from "../../shared/api";
import { useIsAdmin } from "../../shared/auth";
import { savedSchema } from "../../shared/schema";
import { Button } from "../../components/ui/Button";
import { Badge } from "../../components/ui/Badge";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useTelegramSettings } from "./queries";
import type { TelegramSettings } from "./schema";

export function TelegramPanel() {
  const isAdmin = useIsAdmin();
  const settings = useTelegramSettings(isAdmin);
  if (!isAdmin) return null;
  if (settings.isPending) return <Skeleton className="h-40" />;
  if (settings.isError) return <Notice tone="danger" title="Telegram 服务加载失败">{settings.error.message}</Notice>;
  return <TelegramBotForm key={`${settings.data.enabled}:${settings.data.hasBotToken}`} value={settings.data} />;
}

function TelegramBotForm({ value }: { value: TelegramSettings }) {
  const client = useQueryClient();
  const [enabled, setEnabled] = useState(value.enabled);
  const [botToken, setBotToken] = useState("");
  const [clearBotToken, setClearBotToken] = useState(false);
  const save = useMutation({
    mutationFn: () => apiValidated("/api/settings/telegram", savedSchema, {
      method: "PUT", body: JSON.stringify({ enabled, botToken, clearBotToken }),
    }),
    onSuccess: async () => {
      setBotToken("");
      setClearBotToken(false);
      toast.success("Telegram 服务已保存");
      await client.invalidateQueries({ queryKey: ["settings", "telegram"] });
    },
    onError: (error) => toast.error(error.message),
  });
  const dirty = enabled !== value.enabled || botToken !== "" || clearBotToken;
  return (
    <form className="studio-form" onSubmit={(event) => {
      event.preventDefault();
      save.mutate();
    }}>
      <div className="control-heading"><h3>Telegram 服务</h3><Badge>全部用户</Badge></div>
      <fieldset className="form-fields" disabled={save.isPending}>
        <div className="settings-line">
          <span>启用共享服务</span>
          <Switch label="启用 Telegram 服务" checked={enabled} onChange={setEnabled} />
        </div>
        <Field label="Bot Token" meta={<Badge>{value.hasBotToken && !clearBotToken ? "已保存" : "未设置"}</Badge>}>
          <input
            type="password" autoComplete="new-password" value={botToken} maxLength={512}
            onChange={(event) => { setBotToken(event.target.value); setClearBotToken(false); }}
            placeholder={value.hasBotToken && !clearBotToken ? "留空保持不变" : "从 @BotFather 获取"}
          />
        </Field>
        {value.hasBotToken && (
          <div className="settings-line">
            <span>清除 Token 并停用</span>
            <Switch label="清除 Token 并停用" checked={clearBotToken} onChange={(checked) => {
              setClearBotToken(checked);
              if (checked) { setBotToken(""); setEnabled(false); }
            }} />
          </div>
        )}
      </fieldset>
      <div className="form-actions"><Button disabled={save.isPending || !dirty}>{save.isPending ? "保存中…" : "保存"}</Button></div>
    </form>
  );
}
