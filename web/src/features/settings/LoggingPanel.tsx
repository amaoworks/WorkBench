import { loggingSettingsSchema } from "./schema";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { toast } from "sonner";
import { apiValidated } from "../../shared/api";
import type { LoggingSettings, Settings } from "./queries";
import { Button } from "../../components/ui/Button";
import { CardHeader } from "../../components/ui/Card";
import { Field } from "../../components/ui/Field";
import { Select } from "../../components/ui/Select";

const levels = [
  { value: "debug", label: "DEBUG · 调试" },
  { value: "info", label: "INFO · 信息" },
  { value: "warn", label: "WARN · 警告" },
  { value: "error", label: "ERROR · 错误" },
] as const;

export function LoggingPanel({ value }: { value: LoggingSettings }) {
  const [level, setLevel] = useState(value.level);
  const client = useQueryClient();
  const save = useMutation({
    mutationFn: () => apiValidated("/api/settings/logging", loggingSettingsSchema, { method: "PUT", body: JSON.stringify({ level }) }),
    onSuccess: (logging) => {
      client.setQueryData<Settings>(["settings"], (old) => (old ? { ...old, logging } : old));
      toast.success("日志等级已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <form
      className="ui-card mb-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <CardHeader title="运行日志" />
      <div className="p-4">
        <Field label="日志等级">
          <Select
            value={level}
            disabled={save.isPending}
            options={levels}
            onValueChange={(next) => setLevel(next as LoggingSettings["level"])}
          />
        </Field>
        <div className="form-actions">
          <Button disabled={save.isPending || level === value.level}>
            <Save size={16} />
            {save.isPending ? "保存中…" : "保存"}
          </Button>
        </div>
      </div>
    </form>
  );
}
