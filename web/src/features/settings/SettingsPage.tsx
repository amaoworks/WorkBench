import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Save, AudioLines, Check, CircleHelp, Database, Fingerprint, KeyRound, LoaderCircle, Monitor, Moon, Bot, Paintbrush, Radio, Server, ShieldCheck, Sun } from "lucide-react";
import { toast } from "sonner";
import { api } from "../../shared/api";
import { useAppearance, useSettings, type AISettings, type Settings } from "./queries";
import { ModulesPanel } from "./ModulesPanel";
import { PageHeader } from "../../components/ui/PageHeader";

const sections = [
  { id: "modules", title: "业务模块", icon: Database },
  { id: "ai", title: "AI 配置", icon: Bot },
  { id: "security", title: "账户安全", icon: Fingerprint },
  { id: "appearance", title: "外观", icon: Paintbrush },
  { id: "data", title: "数据与运行", icon: Database }
];

export default function SettingsPage() {
  const settings = useSettings();
  const [params, setParams] = useSearchParams();
  const selected = params.get("tab");
  const active = sections.some((section) => section.id === selected) ? selected : "ai";
  function selectTab(id: string) {
    setParams((current) => { const next = new URLSearchParams(current); next.set("tab", id); return next; }, { replace: true, preventScrollReset: true });
  }
  if (settings.isPending) return <div className="settings-loading" role="status"><LoaderCircle className="animate-spin" size={24} />正在准备你的工作空间</div>;
  if (settings.isError) return <div className="settings-loading" role="alert"><p>设置加载失败：{settings.error.message}</p><button className="studio-button" onClick={() => settings.refetch()}>重试</button></div>;
  const data = settings.data;
  return <div className="page settings-page">
    <PageHeader title="设置" description="管理工作空间的连接、安全与使用偏好。" />
    <div className="settings-tabs" role="tablist" aria-label="设置分类">{sections.map(({ id, title, icon: Icon }, index) => <button key={id} id={`tab-${id}`} type="button" role="tab" aria-selected={active === id} aria-controls={`panel-${id}`} tabIndex={active === id ? 0 : -1} onClick={() => selectTab(id)} onKeyDown={(event) => {
      let next: number;
      if (event.key === "ArrowRight") next = (index + 1) % sections.length;
      else if (event.key === "ArrowLeft") next = (index + sections.length - 1) % sections.length;
      else if (event.key === "Home") next = 0;
      else if (event.key === "End") next = sections.length - 1;
      else return;
      event.preventDefault();
      const nextSection = sections[next];
      if (nextSection) { selectTab(nextSection.id); document.getElementById(`tab-${nextSection.id}`)?.focus(); }
    }}><Icon size={17} /><span>{title}</span></button>)}</div>
    <div className="settings-panel" role="tabpanel" id="panel-modules" aria-labelledby="tab-modules" hidden={active !== "modules"}><ModulesPanel /></div>
    <div className="settings-panel" role="tabpanel" id="panel-ai" aria-labelledby="tab-ai" hidden={active !== "ai"}><AIForm key={JSON.stringify(data.ai)} value={data.ai} /></div>
    <div className="settings-panel" role="tabpanel" id="panel-security" aria-labelledby="tab-security" hidden={active !== "security"}><PasswordForm mode={data.deployment.authMode} /></div>
    <div className="settings-panel" role="tabpanel" id="panel-appearance" aria-labelledby="tab-appearance" hidden={active !== "appearance"}><AppearanceForm value={data} /></div>
    <div className="settings-panel" role="tabpanel" id="panel-data" aria-labelledby="tab-data" hidden={active !== "data"}><DataPanel value={data} /></div>
  </div>;
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
  const save = useMutation({ mutationFn: () => api("/api/settings/ai", { method: "PUT", body: JSON.stringify(payload()) }), onSuccess: async () => { setApiKey(""); toast.success("智能配置已保存，新对话请求立即生效"); await client.invalidateQueries({ queryKey: ["settings"] }); await client.invalidateQueries({ queryKey: ["ai"] }); }, onError: (err) => toast.error(err.message) });
  const test = useMutation({ mutationFn: () => api<{ latencyMs: number }>("/api/settings/ai/test", { method: "POST", body: JSON.stringify(payload()) }), onSuccess: (result) => setTestResult(`连接成功 · ${result.latencyMs} ms`), onError: (err) => setTestResult(err.message) });
  const busy = save.isPending || test.isPending;
  const dirty = enabled !== value.enabled || baseUrl !== value.baseUrl || model !== value.model || apiKey !== "" || clearApiKey;
  return <form onSubmit={(event) => { event.preventDefault(); save.mutate(); }} className="studio-form">
    <div className="control-heading"><div><h3>AI 服务</h3><p>兼容 Responses API 的模型服务</p></div><Switch label="启用 AI 服务" checked={enabled} disabled={busy} onChange={setEnabled} /></div>
    <fieldset disabled={busy} className="form-fields" onChange={() => setTestResult("")}>
      <label className="studio-field"><span>接口地址 <span className="field-meta">BASE URL</span></span><input type="url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://api.openai.com/v1" required maxLength={2048} /><small>填写服务商提供的 API 根地址，通常以 /v1 结尾。</small></label>
      <label className="studio-field"><span>模型名称 <span className="field-meta">MODEL</span></span><input value={model} onChange={(e) => setModel(e.target.value)} placeholder="填写可用的模型 ID" required maxLength={200} /></label>
      <label className="studio-field"><span>访问密钥 <span className="key-status"><KeyRound size={12} />{value.hasApiKey && !clearApiKey ? "已保存 · 留空保留" : "尚未设置"}</span></span><input type="password" autoComplete="new-password" value={apiKey} onChange={(e) => { setApiKey(e.target.value); setClearApiKey(false); }} placeholder={value.hasApiKey && !clearApiKey ? "输入新密钥以替换" : "输入 API Key"} maxLength={8192} /><small>密钥保存后不会回显。更换服务地址时需填写对应密钥。</small></label>
      {value.hasApiKey && <label className="check-option"><input type="checkbox" checked={clearApiKey} onChange={(e) => { setClearApiKey(e.target.checked); if (e.target.checked) { setApiKey(""); setEnabled(false); } }} />清除已保存的密钥并停用 AI</label>}
    </fieldset>
    <div className="form-actions">{dirty && <span className="unsaved-label">有未保存的修改</span>}<button type="button" className="studio-button secondary" disabled={busy} onClick={() => { setTestResult(""); test.mutate(); }}>{test.isPending ? <LoaderCircle className="animate-spin" size={16} /> : <Radio size={16} />}测试连接</button><button className="studio-button" disabled={busy || !dirty}>{save.isPending ? <LoaderCircle className="animate-spin" size={16} /> : <Save size={16} />}保存配置</button></div>
    <p className="form-note">连接测试会向当前表单中的服务发送一条简短请求，可能产生少量费用；不会保存配置。</p>
    {testResult && <p className={`inline-result ${test.isError ? "is-error" : ""}`} role="status">{test.isError ? <CircleHelp size={16} /> : <Check size={16} />}{testResult}</p>}
  </form>;
}

function PasswordForm({ mode }: { mode: string }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const categories = [/\p{Lu}/u, /\p{Ll}/u, /\p{Nd}/u, /[\p{P}\p{S}]/u].filter((pattern) => pattern.test(newPassword)).length;
  const longEnough = [...newPassword].length >= 8;
  const change = useMutation({ mutationFn: () => api("/api/settings/password", { method: "PUT", body: JSON.stringify({ currentPassword, newPassword }) }), onSuccess: () => { window.location.assign("/"); }, onError: (err) => toast.error(err.message) });
  const submit = (event: FormEvent) => { event.preventDefault(); if (newPassword !== confirm) { toast.error("两次输入的新密码不一致"); return; } change.mutate(); };
  if (mode === "local") return <div className="local-mode"><ShieldCheck size={32} strokeWidth={1.3} /><h3>当前为本机免登录</h3><p>如需密码，启动时加上 <code>-auth password</code>。</p></div>;
  return <form className="studio-form" onSubmit={submit}><div className="control-heading"><div><h3>修改登录密码</h3><p>修改成功后，所有设备需要重新登录。</p></div><ShieldCheck size={24} className="muted-icon" /></div><fieldset className="form-fields" disabled={change.isPending}>
    <label className="studio-field"><span>当前密码</span><input type="password" autoComplete="current-password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} required /></label>
    <div className="field-pair"><label className="studio-field"><span>新密码</span><input type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} required /></label><label className="studio-field"><span>确认新密码</span><input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required aria-invalid={confirm !== "" && confirm !== newPassword} /></label></div>
    <div className="password-rules"><span className={longEnough ? "satisfied" : ""}><Check size={13} />至少 8 个字符</span><span className={categories >= 3 ? "satisfied" : ""}><Check size={13} />大小写字母、数字、符号四选三</span></div>
    {confirm && confirm !== newPassword && <p className="field-error">两次输入的密码不一致</p>}
    </fieldset><div className="form-actions"><button className="studio-button" disabled={change.isPending || !currentPassword || !longEnough || categories < 3 || newPassword !== confirm}>{change.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <KeyRound size={16} />}更新密码并重新登录</button></div></form>;
}

function AppearanceForm({ value }: { value: Settings }) {
  const save = useAppearance();
  const { theme, motion } = value.appearance;
  const themes = [{ id: "light", title: "日光", subtitle: "清晰，轻盈", icon: Sun }, { id: "dark", title: "夜幕", subtitle: "沉静，专注", icon: Moon }, { id: "system", title: "跟随系统", subtitle: "随你的设备切换", icon: Monitor }] as const;
  return <div className="studio-form"><div className="control-heading"><div><h3>空间色调</h3><p>选择后自动保存，同步到这个工作空间。</p></div></div><div className="theme-options" role="group" aria-label="空间色调">{themes.map(({ id, title, subtitle, icon: Icon }) => <button key={id} className={`theme-option ${theme === id ? "selected" : ""}`} aria-pressed={theme === id} disabled={save.isPending} onClick={() => save.mutate({ theme: id, motion }, { onError: (err) => toast.error(err.message) })}><div className={`theme-preview preview-${id}`} aria-hidden="true"><div className="preview-rail" /><div className="preview-content"><i /><i /><div><b /><b /></div></div><span><Icon size={18} /></span></div><div className="theme-caption"><span>{title}<small>{subtitle}</small></span>{theme === id && <Check size={16} />}</div></button>)}</div><div className="motion-control"><div className="motion-label"><AudioLines size={20} /><div><h3>减少动态效果</h3><p>让画面更安静。系统的减少动态效果偏好也会被尊重。</p></div></div><Switch label="减少动态效果" checked={motion === "reduced"} disabled={save.isPending} onChange={(checked) => save.mutate({ theme, motion: checked ? "reduced" : "full" }, { onError: (err) => toast.error(err.message) })} /></div></div>;
}

function DataPanel({ value }: { value: Settings }) {
  const [file, setFile] = useState("");
  const backup = useMutation({ mutationFn: () => api<{ file: string }>("/api/system/backup", { method: "POST", body: "{}" }), onSuccess: (result) => { setFile(result.file); toast.success("备份已创建并通过完整性检查"); }, onError: (err) => toast.error(err.message) });
  const deployment = value.deployment;
  return <div className="studio-form"><div className="backup-panel"><div className="icon-tile"><Database size={22} /></div><div><h3>数据备份</h3><p>创建完整数据库备份。</p></div><button className="studio-button secondary" disabled={backup.isPending} onClick={() => backup.mutate()}>{backup.isPending ? <LoaderCircle size={16} className="animate-spin" /> : <Database size={16} />}{backup.isPending ? "正在备份" : "创建备份"}</button></div>{file && <p className="inline-result" role="status"><Check size={16} /><span>已保存至数据库同目录的 backups/{file}</span></p>}<p className="form-note backup-note">备份含数据和密钥，保存在服务器。</p>
    <section className="deployment-panel" aria-labelledby="deployment-title"><div className="control-heading"><h3 id="deployment-title"><Server size={17} />运行信息</h3><span className="deployment-hint">启动配置 · 只读</span></div><dl><div><dt>监听地址</dt><dd>{deployment.listenAddress}</dd></div><div><dt>数据库路径</dt><dd>{deployment.dataPath}</dd></div><div><dt>认证模式</dt><dd>{deployment.authMode === "password" ? "密码登录" : "本机免登录"}</dd></div><div><dt>外部访问地址</dt><dd>{deployment.publicUrl || "本机 HTTP 服务"}</dd></div><div><dt>允许访问的 Host</dt><dd>{deployment.allowedHosts?.length ? deployment.allowedHosts.join(", ") : "默认允许本机监听地址"}</dd></div></dl></section></div>;
}

function Switch({ checked, onChange, label, disabled }: { checked: boolean; onChange: (value: boolean) => void; label: string; disabled?: boolean }) {
  return <button type="button" className="studio-switch" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}><span /></button>;
}
