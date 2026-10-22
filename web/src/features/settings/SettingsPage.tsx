import { aiTestSchema, backupSchema } from "./schema";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Save,
  AudioLines,
  Check,
  Database,
  Fingerprint,
  KeyRound,
  LoaderCircle,
  Monitor,
  Moon,
  Bot,
  Paintbrush,
  Radio,
  ShieldCheck,
  Sun,
} from "lucide-react";
import { toast } from "sonner";
import { api, apiValidated } from "../../shared/api";
import { useAppearance, useSettings, type AISettings, type Settings } from "./queries";
import { ModulesPanel } from "./ModulesPanel";
import { LoggingPanel } from "./LoggingPanel";
import { TelegramPanel } from "./TelegramPanel";
import { PageHeader } from "../../components/ui/PageHeader";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { CardHeader } from "../../components/ui/Card";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";

const sections = [
  { id: "modules", title: "业务", icon: Database },
  { id: "ai", title: "AI 配置", icon: Bot },
  { id: "notifications", title: "通知推送", icon: Radio },
  { id: "security", title: "账户安全", icon: Fingerprint },
  { id: "appearance", title: "外观", icon: Paintbrush },
  { id: "data", title: "数据与运行", icon: Database },
];

export default function SettingsPage() {
  const settings = useSettings();
  const [params, setParams] = useSearchParams();
  const selected = params.get("tab");
  const active = sections.some((section) => section.id === selected) ? selected : "modules";
  function selectTab(id: string) {
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.set("tab", id);
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  }
  if (settings.isPending)
    return (
      <div className="page space-y-4">
        <Skeleton className="h-12" />
        <Skeleton className="h-64" />
      </div>
    );
  if (settings.isError)
    return (
      <Notice tone="danger" title="设置加载失败" action={<Button onClick={() => settings.refetch()}>重试</Button>}>
        {settings.error.message}
      </Notice>
    );
  const data = settings.data;
  return (
    <div className="page settings-page">
      <PageHeader title="设置" />
      <div className="settings-tabs" role="tablist" aria-label="设置分类">
        {sections.map(({ id, title, icon: Icon }, index) => (
          <button
            key={id}
            id={`tab-${id}`}
            type="button"
            role="tab"
            aria-selected={active === id}
            aria-controls={`panel-${id}`}
            tabIndex={active === id ? 0 : -1}
            onClick={() => selectTab(id)}
            onKeyDown={(event) => {
              let next: number;
              if (event.key === "ArrowRight") next = (index + 1) % sections.length;
              else if (event.key === "ArrowLeft") next = (index + sections.length - 1) % sections.length;
              else if (event.key === "Home") next = 0;
              else if (event.key === "End") next = sections.length - 1;
              else return;
              event.preventDefault();
              const nextSection = sections[next];
              if (nextSection) {
                selectTab(nextSection.id);
                document.getElementById(`tab-${nextSection.id}`)?.focus();
              }
            }}
          >
            <Icon size={17} />
            <span>{title}</span>
          </button>
        ))}
      </div>
      <div className="settings-panel" role="tabpanel" id="panel-modules" aria-labelledby="tab-modules" hidden={active !== "modules"}>
        <ModulesPanel />
      </div>
      <div className="settings-panel" role="tabpanel" id="panel-ai" aria-labelledby="tab-ai" hidden={active !== "ai"}>
        <AIForm key={JSON.stringify(data.ai)} value={data.ai} />
      </div>
      <div
        className="settings-panel"
        role="tabpanel"
        id="panel-notifications"
        aria-labelledby="tab-notifications"
        hidden={active !== "notifications"}
      >
        <TelegramPanel />
      </div>
      <div className="settings-panel" role="tabpanel" id="panel-security" aria-labelledby="tab-security" hidden={active !== "security"}>
        <PasswordForm mode={data.deployment.authMode} />
      </div>
      <div
        className="settings-panel"
        role="tabpanel"
        id="panel-appearance"
        aria-labelledby="tab-appearance"
        hidden={active !== "appearance"}
      >
        <AppearanceForm value={data} />
      </div>
      <div className="settings-panel" role="tabpanel" id="panel-data" aria-labelledby="tab-data" hidden={active !== "data"}>
        <LoggingPanel key={data.logging.level} value={data.logging} />
        <DataPanel value={data} />
      </div>
    </div>
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
      toast.success("AI 配置已保存");
      await client.invalidateQueries({ queryKey: ["settings"] });
      await client.invalidateQueries({ queryKey: ["ai"] });
    },
    onError: (err) => toast.error(err.message),
  });
  const test = useMutation({
    mutationFn: () => apiValidated("/api/settings/ai/test", aiTestSchema, { method: "POST", body: JSON.stringify(payload()) }),
    onSuccess: (result) => setTestResult(`连接成功 · ${result.latencyMs} ms`),
    onError: (err) => setTestResult(err.message),
  });
  const busy = save.isPending || test.isPending;
  const dirty = enabled !== value.enabled || baseUrl !== value.baseUrl || model !== value.model || apiKey !== "" || clearApiKey;
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
      className="studio-form"
    >
      <div className="control-heading">
        <div>
          <h3>AI 服务</h3>
        </div>
        <Switch label="启用 AI 服务" checked={enabled} disabled={busy} onChange={setEnabled} />
      </div>
      <fieldset disabled={busy} className="form-fields" onChange={() => setTestResult("")}>
        <Field label="接口地址">
          <input
            type="url"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://api.openai.com/v1"
            required
            maxLength={2048}
          />
        </Field>
        <Field label="模型名称">
          <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="填写可用的模型 ID" required maxLength={200} />
        </Field>
        <Field label="访问密钥" meta={<Badge>{value.hasApiKey && !clearApiKey ? "已保存" : "未设置"}</Badge>}>
          <input
            type="password"
            autoComplete="new-password"
            value={apiKey}
            onChange={(e) => {
              setApiKey(e.target.value);
              setClearApiKey(false);
            }}
            placeholder={value.hasApiKey && !clearApiKey ? "留空保持不变" : "输入 API Key"}
            maxLength={8192}
          />
        </Field>
        {value.hasApiKey && (
          <div className="flex items-center justify-between gap-3 text-sm">
            <span>清除密钥并停用</span>
            <Switch
              label="清除密钥并停用"
              checked={clearApiKey}
              onChange={(checked) => {
                setClearApiKey(checked);
                if (checked) {
                  setApiKey("");
                  setEnabled(false);
                }
              }}
            />
          </div>
        )}
      </fieldset>
      <div className="form-actions">
        {dirty && (
          <Badge tone="warning" className="mr-auto">
            未保存
          </Badge>
        )}
        <Button
          type="button"
          variant="secondary"
          disabled={busy}
          onClick={() => {
            setTestResult("");
            test.mutate();
          }}
        >
          <Radio size={16} />
          {test.isPending ? "测试中…" : "测试连接"}
        </Button>
        <Button disabled={busy || !dirty}>
          <Save size={16} />
          {save.isPending ? "保存中…" : "保存"}
        </Button>
      </div>
      {testResult && (
        <Notice tone={test.isError ? "danger" : "info"} title={test.isError ? "连接失败" : "连接成功"} className="mt-4">
          {testResult}
        </Notice>
      )}
    </form>
  );
}

function PasswordForm({ mode }: { mode: string }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const categories = [/\p{Lu}/u, /\p{Ll}/u, /\p{Nd}/u, /[\p{P}\p{S}]/u].filter((pattern) => pattern.test(newPassword)).length;
  const longEnough = [...newPassword].length >= 8;
  const change = useMutation({
    mutationFn: () => api("/api/settings/password", { method: "PUT", body: JSON.stringify({ currentPassword, newPassword }) }),
    onSuccess: () => {
      window.location.assign("/");
    },
    onError: (err) => toast.error(err.message),
  });
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (newPassword !== confirm) {
      toast.error("两次输入的新密码不一致");
      return;
    }
    change.mutate();
  };
  if (mode === "local")
    return (
      <div className="local-mode">
        <ShieldCheck size={32} strokeWidth={1.3} />
        <h3>本机免登录模式</h3>
      </div>
    );
  return (
    <form className="studio-form" onSubmit={submit}>
      <div className="control-heading">
        <div>
          <h3>修改登录密码</h3>
        </div>
        <ShieldCheck size={24} className="muted-icon" />
      </div>
      <fieldset className="form-fields" disabled={change.isPending}>
        <Field label="当前密码">
          <input
            type="password"
            autoComplete="current-password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            required
          />
        </Field>
        <div className="field-pair">
          <Field label="新密码">
            <input
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              required
            />
          </Field>
          <Field label="确认新密码" error={confirm && confirm !== newPassword ? "两次输入的密码不一致" : undefined}>
            <input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </Field>
        </div>
        <div className="password-rules">
          <span className={longEnough ? "satisfied" : ""}>
            <Check size={13} />
            至少 8 个字符
          </span>
          <span className={categories >= 3 ? "satisfied" : ""}>
            <Check size={13} />
            大小写字母、数字、符号四选三
          </span>
        </div>
      </fieldset>
      <div className="form-actions">
        <button
          className="studio-button"
          disabled={change.isPending || !currentPassword || !longEnough || categories < 3 || newPassword !== confirm}
        >
          {change.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <KeyRound size={16} />}更新密码
        </button>
      </div>
    </form>
  );
}

function AppearanceForm({ value }: { value: Settings }) {
  const save = useAppearance();
  const { theme, motion, palette = "default" } = value.appearance;
  const palettes = [
    { id: "default", title: "草木（默认）", description: "大地橄榄绿与自然米灰", color: "#526b37" },
    { id: "claude", title: "暖陶（Claude）", description: "暖象牙纸底与焦糖赤陶色", color: "#cc785c" },
  ] as const;
  const themes = [
    { id: "light", title: "浅色", icon: Sun },
    { id: "dark", title: "深色", icon: Moon },
    { id: "system", title: "跟随系统", icon: Monitor },
  ] as const;
  return (
    <div className="studio-form">
      <div className="control-heading">
        <h3>风格</h3>
      </div>
      <div className="palette-options" role="group" aria-label="风格">
        {palettes.map((item) => (
          <button
            key={item.id}
            type="button"
            className={`palette-option focus-ring ${palette === item.id ? "selected" : ""}`}
            aria-pressed={palette === item.id}
            onClick={() => save.mutate({ theme, motion, palette: item.id }, { onError: (err) => toast.error(err.message) })}
          >
            <div className="palette-info">
              <span className="palette-swatch" style={{ background: item.color }} aria-hidden="true" />
              <div>
                <span className="text-sm font-medium">{item.title}</span>
                <p className="mt-0.5 text-xs text-[var(--muted)]">{item.description}</p>
              </div>
            </div>
            {palette === item.id && <Check size={16} className="shrink-0 text-brand" />}
          </button>
        ))}
      </div>
      <div className="control-heading">
        <h3>模式</h3>
      </div>
      <div className="theme-options" role="group" aria-label="主题">
        {themes.map(({ id, title, icon: Icon }) => (
          <button
            key={id}
            type="button"
            className={`theme-option focus-ring ${theme === id ? "selected" : ""}`}
            aria-pressed={theme === id}
            onClick={() => save.mutate({ theme: id, motion, palette }, { onError: (err) => toast.error(err.message) })}
          >
            <div className={`theme-preview preview-${id}`} aria-hidden="true">
              <div className="preview-rail" />
              <div className="preview-content">
                <i />
                <i />
                <div>
                  <b />
                  <b />
                </div>
              </div>
              <span>
                <Icon size={18} />
              </span>
            </div>
            <div className="theme-caption">
              <span>{title}</span>
              {theme === id && <Check size={16} />}
            </div>
          </button>
        ))}
      </div>
      <div className="motion-control">
        <div className="motion-label">
          <AudioLines size={20} />
          <h3>减少动态效果</h3>
        </div>
        <Switch
          label="减少动态效果"
          checked={motion === "reduced"}
          disabled={save.isPending}
          onChange={(checked) =>
            save.mutate({ theme, motion: checked ? "reduced" : "full", palette }, { onError: (err) => toast.error(err.message) })
          }
        />
      </div>
    </div>
  );
}

function DataPanel({ value }: { value: Settings }) {
  const [file, setFile] = useState("");
  const backup = useMutation({
    mutationFn: () => apiValidated("/api/system/backup", backupSchema, { method: "POST", body: "{}" }),
    onSuccess: (result) => {
      setFile(result.file);
      toast.success("备份已创建");
    },
    onError: (err) => toast.error(err.message),
  });
  const deployment = value.deployment;
  return (
    <div className="space-y-4">
      <div className="ui-card">
        <CardHeader
          title="数据备份"
          action={
            deployment.backupSupported ? (
              <Button variant="secondary" disabled={backup.isPending} onClick={() => backup.mutate()}>
                <Database size={16} />
                {backup.isPending ? "备份中…" : "创建备份"}
              </Button>
            ) : (
              <Badge>由数据库服务管理</Badge>
            )
          }
        />
        {!deployment.backupSupported && (
          <p className="p-4 text-sm text-[var(--muted)]">
            使用数据库服务的备份或 <code>pg_dump</code>；本地数据路径中的运行文件需另行备份。
          </p>
        )}
        {file && (
          <Notice title="备份已创建" className="m-4">
            backups/{file}
          </Notice>
        )}
      </div>
      <section className="ui-card" aria-label="运行信息">
        <CardHeader title="运行信息" action={<Badge>只读</Badge>} />
        <dl className="studio-deployment-details p-4">
          <div>
            <dt>监听地址</dt>
            <dd>{deployment.listenAddress}</dd>
          </div>
          <div>
            <dt>数据库</dt>
            <dd>{deployment.databaseDriver === "postgres" ? "PostgreSQL" : "SQLite"}</dd>
          </div>
          <div>
            <dt>{deployment.databaseDriver === "postgres" ? "本地数据路径" : "数据库路径"}</dt>
            <dd>{deployment.dataPath}</dd>
          </div>
          <div>
            <dt>认证模式</dt>
            <dd>{deployment.authMode === "password" ? "密码登录" : "本机免登录"}</dd>
          </div>
          <div>
            <dt>外部访问地址</dt>
            <dd>{deployment.publicUrl || "本机 HTTP 服务"}</dd>
          </div>
          <div>
            <dt>允许访问的 Host</dt>
            <dd>{deployment.allowedHosts?.length ? deployment.allowedHosts.join(", ") : "默认允许本机监听地址"}</dd>
          </div>
        </dl>
      </section>
    </div>
  );
}
