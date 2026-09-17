import { loggingSettingsSchema } from "./schema";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { LoaderCircle, Save } from "lucide-react";
import { toast } from "sonner";
import { apiValidated } from "../../shared/api";
import type { LoggingSettings, Settings } from "./queries";

const levels = [
  { value: "debug", label: "DEBUG · 调试", description: "记录全部日志，包括健康检查、任务执行和事件投递细节。" },
  { value: "info", label: "INFO · 信息", description: "记录正常请求、运行状态、警告和错误，适合日常运行。" },
  { value: "warn", label: "WARN · 警告", description: "仅记录请求拒绝、任务重试等警告，以及错误。" },
  { value: "error", label: "ERROR · 错误", description: "仅记录服务异常、请求失败和后台任务最终失败。" },
] as const;

export function LoggingPanel({ value }: { value: LoggingSettings }) {
  const [level, setLevel] = useState(value.level);
  const client = useQueryClient();
  const save = useMutation({
    mutationFn: () => apiValidated("/api/settings/logging", loggingSettingsSchema, { method: "PUT", body: JSON.stringify({ level }) }),
    onSuccess: (logging) => {
      client.setQueryData<Settings>(["settings"], (old) => old ? { ...old, logging } : old);
      toast.success("日志等级已保存并立即生效");
    },
    onError: (error) => toast.error(error.message),
  });
  return <form className="studio-form" onSubmit={(event) => { event.preventDefault(); save.mutate(); }}>
    <div className="control-heading"><div><h3>运行日志</h3><p>选择最低记录等级，保存后立即生效，重启后保留。</p></div></div>
    <label className="studio-field"><span id="logging-level-label">日志等级</span>
      <select aria-labelledby="logging-level-label" aria-describedby="logging-level-help" value={level} disabled={save.isPending} onChange={(event) => setLevel(event.target.value as LoggingSettings["level"])}>
        {levels.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      <small id="logging-level-help">{levels.find((option) => option.value === level)?.description}</small>
    </label>
    <p className="form-note">容器部署使用 docker compose logs 查看日志，systemd 部署使用 journalctl 查看。</p>
    <div className="form-actions"><button className="studio-button" disabled={save.isPending || level === value.level}>{save.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <Save size={16} />}保存日志等级</button></div>
  </form>;
}
