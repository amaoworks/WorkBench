import { loggingSettingsSchema } from "./schema";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save } from "lucide-react";
import { toast } from "sonner";
import { apiValidated } from "../../shared/api";
import type { LoggingSettings, Settings } from "./queries";
import { Button } from "../../components/ui/Button";
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
      className="settings-row"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <span className="settings-row-label" id="logging-level-label">
        日志等级
      </span>
      <div className="settings-row-control">
        <Select
          aria-labelledby="logging-level-label"
          value={level}
          disabled={save.isPending}
          options={levels}
          onValueChange={(next) => setLevel(next as LoggingSettings["level"])}
        />
        <Button size="sm" variant="secondary" disabled={save.isPending || level === value.level}>
          <Save size={15} />
          {save.isPending ? "保存中…" : "保存"}
        </Button>
      </div>
    </form>
  );
}
