import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Blocks, Bot, Database, Radio, Save, Users } from "lucide-react";
import { toast } from "sonner";
import { api, apiValidated } from "../../shared/api";
import { useIsAdmin } from "../../shared/auth";
import { Badge } from "../../components/ui/Badge";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useSettings, type AISettings, type Settings } from "./queries";
import { aiTestSchema, backupSchema } from "./schema";
import { LoggingPanel } from "./LoggingPanel";
import { ModulesPanel } from "./ModulesPanel";
import { SettingsLayout, SettingsRow, SettingsSection } from "./SettingsLayout";
import { TelegramPanel } from "./TelegramPanel";
import { UsersPanel } from "./UsersPanel";

const sections = [
  { id: "users", title: "用户管理", icon: Users },
  { id: "modules", title: "业务管理", icon: Blocks },
  { id: "ai", title: "AI 服务", icon: Bot },
  { id: "notifications", title: "通知服务", icon: Radio },
  { id: "data", title: "数据与运行", icon: Database },
] as const;

export default function AdminSettingsPage() {
  const isAdmin = useIsAdmin();
  if (!isAdmin) return (
    <Notice title="无权访问后台设置" action={<ButtonLink to="/settings" variant="secondary">个人设置</ButtonLink>}>
      仅管理员可管理平台配置。
    </Notice>
  );
  // Keep management queries behind the route guard, including direct URL visits.
  return <AdminSettings />;
}

function AdminSettings() {
  const settings = useSettings();
  if (settings.isPending) return <div className="page space-y-4"><Skeleton className="h-12" /><Skeleton className="h-64" /></div>;
  if (settings.isError) return (
    <Notice tone="danger" title="后台设置加载失败" action={<Button onClick={() => settings.refetch()}>重试</Button>}>
      {settings.error.message}
    </Notice>
  );
  const data = settings.data;
  return (
    <SettingsLayout title="后台设置" sections={sections}>
      <SettingsSection id="users" title="用户管理"><UsersPanel /></SettingsSection>
      <SettingsSection id="modules" title="业务管理"><ModulesPanel scope="admin" /></SettingsSection>
      <SettingsSection id="ai" title="AI 服务">
        {data.ai && <AIForm key={JSON.stringify(data.ai)} value={data.ai} />}
      </SettingsSection>
      <SettingsSection id="notifications" title="通知服务"><TelegramPanel /></SettingsSection>
      <SettingsSection id="data" title="数据与运行">
        {data.deployment && <DataPanel deployment={data.deployment} />}
        {data.logging && <LoggingPanel key={data.logging.level} value={data.logging} />}
      </SettingsSection>
    </SettingsLayout>
  );
}

function AIForm({ value }: { value: AISettings }) {
  const client = useQueryClient();
  const [enabled, setEnabled] = useState(value.enabled);
  const [baseUrl, setBaseUrl] = useState(value.baseUrl);
  const [model, setModel] = useState(value.model);
  const [apiKey, setApiKey] = useState("");
  const [clearApiKey, setClearApiKey] = useState(false);
  const [testResult, setTestResult] = useState("");
  const payload = () => ({ enabled, baseUrl, model, apiKey, clearApiKey });
  const save = useMutation({
    mutationFn: () => api("/api/settings/ai", { method: "PUT", body: JSON.stringify(payload()) }),
    onSuccess: async () => {
      setApiKey("");
      setClearApiKey(false);
      toast.success("AI 配置已保存");
      await client.invalidateQueries({ queryKey: ["settings"] });
      await client.invalidateQueries({ queryKey: ["ai"] });
    },
    onError: (err) => toast.error(err.message),
  });
  const test = useMutation({
    mutationFn: () => apiValidated("/api/settings/ai/test", aiTestSchema, { method: "POST", body: JSON.stringify(payload()) }),
    onSuccess: (result) => setTestResult(`${result.latencyMs} ms`),
    onError: (err) => setTestResult(err.message),
  });
  const busy = save.isPending || test.isPending;
  const dirty = enabled !== value.enabled || baseUrl !== value.baseUrl || model !== value.model || apiKey !== "" || clearApiKey;
  return (
    <form onSubmit={(event) => { event.preventDefault(); save.mutate(); }} className="studio-form">
      <div className="control-heading">
        <h3>AI 服务</h3><Switch label="启用 AI 服务" checked={enabled} disabled={busy} onChange={setEnabled} />
      </div>
      <fieldset disabled={busy} className="form-fields" onChange={() => setTestResult("")}>
        <Field label="接口地址">
          <input
            type="url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://api.openai.com/v1" required maxLength={2048}
          />
        </Field>
        <Field label="模型名称">
          <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="填写可用的模型 ID" required maxLength={200} />
        </Field>
        <Field label="访问密钥" meta={<Badge>{value.hasApiKey && !clearApiKey ? "已保存" : "未设置"}</Badge>}>
          <input
            type="password" autoComplete="new-password" value={apiKey}
            onChange={(e) => { setApiKey(e.target.value); setClearApiKey(false); }}
            placeholder={value.hasApiKey && !clearApiKey ? "留空保持不变" : "输入 API Key"} maxLength={8192}
          />
        </Field>
        {value.hasApiKey && (
          <div className="settings-line">
            <span>清除密钥并停用</span>
            <Switch label="清除密钥并停用" checked={clearApiKey} onChange={(checked) => {
              setClearApiKey(checked);
              if (checked) { setApiKey(""); setEnabled(false); }
            }} />
          </div>
        )}
      </fieldset>
      <div className="form-actions">
        {dirty && <Badge tone="warning" className="mr-auto">未保存</Badge>}
        <Button type="button" variant="secondary" disabled={busy} onClick={() => { setTestResult(""); test.mutate(); }}>
          <Radio size={16} />{test.isPending ? "测试中…" : "测试连接"}
        </Button>
        <Button disabled={busy || !dirty}><Save size={16} />{save.isPending ? "保存中…" : "保存"}</Button>
      </div>
      {testResult && (
        <Notice tone={test.isError ? "danger" : "info"} title={test.isError ? "连接失败" : "连接成功"} className="mt-4">{testResult}</Notice>
      )}
    </form>
  );
}

function DataPanel({ deployment }: { deployment: NonNullable<Settings["deployment"]> }) {
  const [file, setFile] = useState("");
  const backup = useMutation({
    mutationFn: () => apiValidated("/api/system/backup", backupSchema, { method: "POST", body: "{}" }),
    onSuccess: (result) => { setFile(result.file); toast.success("备份已创建"); },
    onError: (err) => toast.error(err.message),
  });
  return (
    <div className="settings-rows">
      <SettingsRow label={<>数据备份 <Badge>全部用户</Badge></>}>
        {deployment.backupSupported ? (
          <Button variant="secondary" size="sm" disabled={backup.isPending} onClick={() => backup.mutate()}>
            <Database size={15} />{backup.isPending ? "备份中…" : "创建备份"}
          </Button>
        ) : <Badge>由数据库服务管理</Badge>}
      </SettingsRow>
      {!deployment.backupSupported && <p className="settings-note">用 <code>pg_dump</code> 或数据库服务备份，运行文件另行备份</p>}
      {file && <Notice title="备份已创建" className="my-3">backups/{file}</Notice>}
      <section aria-labelledby="settings-runtime-title">
        <h3 id="settings-runtime-title" className="settings-subhead">运行信息</h3>
        <dl className="studio-deployment-details">
          <div><dt>监听地址</dt><dd>{deployment.listenAddress}</dd></div>
          <div><dt>数据库</dt><dd>{deployment.databaseDriver === "postgres" ? "PostgreSQL" : "SQLite"}</dd></div>
          {deployment.databaseDriver !== "postgres" && <div><dt>数据库路径</dt><dd>{deployment.dataPath}</dd></div>}
          <div><dt>认证模式</dt><dd>{deployment.authMode === "password" ? "密码登录" : "本机免登录"}</dd></div>
          <div><dt>外部访问地址</dt><dd>{deployment.publicUrl || "本机 HTTP 服务"}</dd></div>
          <div><dt>允许访问的 Host</dt><dd>{deployment.allowedHosts?.length ? deployment.allowedHosts.join(", ") : "默认允许本机监听地址"}</dd></div>
        </dl>
      </section>
    </div>
  );
}
