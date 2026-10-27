import { aiTestSchema, backupSchema } from "./schema";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Save,
  Blocks,
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
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { cn } from "../../shared/cn";

const sections = [
  { id: "appearance", title: "外观", icon: Paintbrush },
  { id: "modules", title: "业务模块", icon: Blocks },
  { id: "ai", title: "AI", icon: Bot },
  { id: "notifications", title: "通知推送", icon: Radio },
  { id: "security", title: "账户安全", icon: Fingerprint },
  { id: "data", title: "数据与运行", icon: Database },
] as const;
type SectionId = (typeof sections)[number]["id"];
const sectionId = (id: string) => `settings-${id}`;
const isSection = (id: string | null): id is SectionId => sections.some((section) => section.id === id);
// Just below the sticky 64px top bar (and the chip strip on narrow screens).
const SPY_OFFSET = 140;

export default function SettingsPage() {
  const settings = useSettings();
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
  return <SettingsContent data={settings.data} />;
}

function scrollToSection(id: SectionId, smooth: boolean) {
  const reduced = document.documentElement.dataset.motion === "reduced" || window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  document.getElementById(sectionId(id))?.scrollIntoView({ block: "start", behavior: smooth && !reduced ? "smooth" : "auto" });
}

function SettingsContent({ data }: { data: Settings }) {
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab");
  const [current, setCurrent] = useState<SectionId>(isSection(requested) ? requested : "appearance");
  const nav = useRef<HTMLElement>(null);
  const firstScroll = useRef(true);

  // `?tab=` (old links, module dialogs, the AI panel) scrolls to its section.
  useEffect(() => {
    if (!isSection(requested)) return;
    scrollToSection(requested, !firstScroll.current);
    firstScroll.current = false;
  }, [requested]);

  // Highlight the section under the top bar while the page scrolls.
  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      let next: SectionId = "appearance";
      for (const section of sections) {
        const element = document.getElementById(sectionId(section.id));
        if (element && element.getBoundingClientRect().top <= SPY_OFFSET) next = section.id;
      }
      if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) next = "data";
      setCurrent(next);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, []);

  // Keep the active chip visible when the nav is a horizontal strip.
  useEffect(() => {
    const strip = nav.current;
    const chip = strip?.querySelector<HTMLElement>(`[data-section="${current}"]`);
    if (strip && chip && strip.scrollWidth > strip.clientWidth) strip.scrollTo({ left: chip.offsetLeft - 16 });
  }, [current]);

  function select(id: SectionId) {
    setCurrent(id);
    scrollToSection(id, true);
    setParams(
      (value) => {
        const next = new URLSearchParams(value);
        next.set("tab", id);
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  }

  return (
    <div className="page settings-page">
      <PageHeader title="设置" />
      <div className="settings-layout">
        <nav ref={nav} className="settings-nav" aria-label="设置分类">
          {sections.map(({ id, title, icon: Icon }) => (
            <a
              key={id}
              href={`?tab=${id}`}
              data-section={id}
              className={cn("settings-nav-link focus-ring", current === id && "is-active")}
              aria-current={current === id ? "true" : undefined}
              onClick={(event) => {
                event.preventDefault();
                select(id);
              }}
            >
              <Icon size={18} />
              <span>{title}</span>
            </a>
          ))}
        </nav>
        <div className="settings-sections">
          <SettingsSection id="appearance">
            <AppearanceForm value={data} />
          </SettingsSection>
          <SettingsSection id="modules">
            <ModulesPanel />
          </SettingsSection>
          <SettingsSection id="ai">
            <AIForm key={JSON.stringify(data.ai)} value={data.ai} />
          </SettingsSection>
          <SettingsSection id="notifications">
            <TelegramPanel />
          </SettingsSection>
          <SettingsSection id="security">
            <PasswordForm mode={data.deployment.authMode} />
          </SettingsSection>
          <SettingsSection id="data">
            <DataPanel value={data} />
            <LoggingPanel key={data.logging.level} value={data.logging} />
          </SettingsSection>
        </div>
      </div>
    </div>
  );
}

function SettingsSection({ id, children }: { id: SectionId; children: ReactNode }) {
  const title = sections.find((section) => section.id === id)?.title;
  return (
    <section id={sectionId(id)} className="settings-section" aria-labelledby={`${sectionId(id)}-title`}>
      <h2 id={`${sectionId(id)}-title`}>{title}</h2>
      {children}
    </section>
  );
}

/** Label on the left, control on the right. */
function SettingsRow({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="settings-row">
      <div className="settings-row-label">{label}</div>
      <div className="settings-row-control">{children}</div>
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
          <div className="settings-line">
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
      <SettingsRow label="登录方式">
        <Badge>本机免登录</Badge>
      </SettingsRow>
    );
  return (
    <form className="studio-form" onSubmit={submit}>
      <div className="control-heading">
        <div>
          <h3>修改登录密码</h3>
        </div>
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
    { id: "default", title: "TradingView（默认）", color: "#2962ff" },
    { id: "claude", title: "暖陶（Claude）", color: "#cc785c" },
  ] as const;
  const themes = [
    { id: "light", title: "浅色", icon: Sun },
    { id: "dark", title: "深色", icon: Moon },
    { id: "system", title: "跟随系统", icon: Monitor },
  ] as const;
  const apply = (next: Settings["appearance"]) => save.mutate(next, { onError: (err) => toast.error(err.message) });
  return (
    <div className="settings-rows">
      <SettingsRow label="风格">
        <div className="settings-segment" role="group" aria-label="风格">
          {palettes.map((item) => (
            <button
              key={item.id}
              type="button"
              className="focus-ring"
              aria-pressed={palette === item.id}
              onClick={() => apply({ theme, motion, palette: item.id })}
            >
              <span className="palette-swatch" style={{ background: item.color }} aria-hidden="true" />
              {item.title}
            </button>
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label="主题">
        <div className="settings-segment" role="group" aria-label="主题">
          {themes.map(({ id, title, icon: Icon }) => (
            <button key={id} type="button" className="focus-ring" aria-pressed={theme === id} onClick={() => apply({ theme: id, motion, palette })}>
              <Icon size={14} aria-hidden="true" />
              {title}
            </button>
          ))}
        </div>
      </SettingsRow>
      <SettingsRow label="减少动态效果">
        <Switch
          label="减少动态效果"
          checked={motion === "reduced"}
          disabled={save.isPending}
          onChange={(checked) => apply({ theme, motion: checked ? "reduced" : "full", palette })}
        />
      </SettingsRow>
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
    <div className="settings-rows">
      <SettingsRow label="数据备份">
        {deployment.backupSupported ? (
          <Button variant="secondary" size="sm" disabled={backup.isPending} onClick={() => backup.mutate()}>
            <Database size={15} />
            {backup.isPending ? "备份中…" : "创建备份"}
          </Button>
        ) : (
          <Badge>由数据库服务管理</Badge>
        )}
      </SettingsRow>
      {!deployment.backupSupported && (
        <p className="settings-note">
          使用数据库服务的备份或 <code>pg_dump</code>；本地数据路径中的运行文件需另行备份。
        </p>
      )}
      {file && (
        <Notice title="备份已创建" className="my-3">
          backups/{file}
        </Notice>
      )}
      <section aria-labelledby="settings-runtime-title">
        <h3 id="settings-runtime-title" className="settings-subhead">
          运行信息
        </h3>
        <dl className="studio-deployment-details">
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
