import { useRef, useState, type FormEvent } from "react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useReportDirty, useSettingsDialog } from "../../shared/settingsDialog";
import { SchwabSection, type SchwabDraft } from "./SchwabSettings";
import { FutuSection, type FutuDraft } from "./FutuSettings";
import { OvernightSection } from "./OvernightSettings";
import {
  saveFutuSettings,
  saveOvernightSettings,
  saveSchwabSettings,
  useFutuSettings,
  useOvernightSettings,
  useRefreshInvestment,
  useSchwabSettings,
} from "./queries";

const defaultCallbackUrl = () => (window.location.protocol === "https:" ? `${window.location.origin}/oauth/schwab` : "");

type Section = "schwab" | "futu" | "overnight";
const SAVED_MESSAGES: Record<Section, string> = { schwab: "Schwab 设置已保存", futu: "富途牛牛设置已保存", overnight: "夜盘设置已保存" };
const SECTION_NAMES: Record<Section, string> = { schwab: "Charles Schwab", futu: "富途牛牛行情", overnight: "夜盘行情" };

export default function InvestmentSettings({ enabled }: { enabled: boolean }) {
  const schwab = useSchwabSettings(enabled);
  const futu = useFutuSettings(enabled);
  const overnight = useOvernightSettings(enabled);
  if (!enabled) return <Notice title="投资业务已停用">启用后可配置 Charles Schwab。</Notice>;
  if (schwab.isPending || futu.isPending || overnight.isPending) return <Skeleton className="h-64" />;
  return <InvestmentSettingsForm schwab={schwab} futu={futu} overnight={overnight} />;
}

function InvestmentSettingsForm({
  schwab,
  futu,
  overnight,
}: {
  schwab: ReturnType<typeof useSchwabSettings>;
  futu: ReturnType<typeof useFutuSettings>;
  overnight: ReturnType<typeof useOvernightSettings>;
}) {
  const dialog = useSettingsDialog();
  const refresh = useRefreshInvestment();
  // A null draft means "unchanged": the section follows the latest server data.
  const [schwabDraft, setSchwabDraft] = useState<SchwabDraft | null>(null);
  const [futuDraft, setFutuDraft] = useState<FutuDraft | null>(null);
  const [overnightDraft, setOvernightDraft] = useState<boolean | null>(null);
  const [errors, setErrors] = useState<Partial<Record<Section, string>>>({});
  const [saving, setSaving] = useState(false);

  const schwabBase: SchwabDraft | null = schwab.data
    ? { appKey: schwab.data.appKey, appSecret: "", callbackUrl: schwab.data.callbackUrl || defaultCallbackUrl() }
    : null;
  const futuBase: FutuDraft | null = futu.data ? { enabled: futu.data.enabled, account: futu.data.account, password: "" } : null;
  const schwabValue = schwabDraft ?? schwabBase;
  const futuValue = futuDraft ?? futuBase;
  const overnightValue = overnightDraft ?? overnight.data?.enabled ?? false;
  const schwabDirty =
    !!schwabDraft &&
    !!schwabBase &&
    (schwabDraft.appKey !== schwabBase.appKey || schwabDraft.appSecret !== "" || schwabDraft.callbackUrl !== schwabBase.callbackUrl);
  const futuDirty =
    !!futuDraft &&
    !!futuBase &&
    (futuDraft.enabled !== futuBase.enabled || futuDraft.account !== futuBase.account || futuDraft.password !== "");
  const overnightDirty = overnightDraft !== null && !!overnight.data && overnightDraft !== overnight.data.enabled;
  const dirty = schwabDirty || futuDirty || overnightDirty;
  useReportDirty(dirty);

  // Turning Futu off stops its service and the overnight quotes that depend on it, so it is confirmed first.
  const disablesFutu = futuDirty && !!futu.data?.enabled && futuValue?.enabled === false;
  const [confirmDisable, setConfirmDisable] = useState(false);
  const saveButton = useRef<HTMLButtonElement>(null);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!dirty || saving) return;
    if (disablesFutu) setConfirmDisable(true);
    else void save();
  }

  async function save() {
    setSaving(true);
    const failed: Partial<Record<Section, string>> = {};
    const saved: Section[] = [];
    const run = async (section: Section, action: () => Promise<unknown>) => {
      try {
        await action();
        saved.push(section);
      } catch (error) {
        failed[section] = error instanceof Error ? error.message : String(error);
      }
    };
    // Sections save in order: overnight depends on the Futu provider being enabled.
    if (schwabDirty && schwabValue) await run("schwab", () => saveSchwabSettings(schwabValue));
    if (futuDirty && futuValue) await run("futu", () => saveFutuSettings({ ...futuValue, clearPassword: false }));
    if (overnightDirty) await run("overnight", () => saveOvernightSettings(overnightValue));
    await refresh().catch(() => undefined);
    if (saved.includes("schwab")) setSchwabDraft(null);
    if (saved.includes("futu")) setFutuDraft(null);
    if (saved.includes("overnight")) setOvernightDraft(null);
    setErrors(failed);
    setSaving(false);
    const failedSections = Object.keys(failed) as Section[];
    const failedNames = failedSections.map((section) => SECTION_NAMES[section]);
    if (failedNames.length > 0) {
      toast.error(`${failedNames.join("、")} 保存失败`);
      const firstFailed = failedSections[0];
      const targetId = firstFailed === "schwab" ? "schwab-settings-title" : firstFailed === "futu" ? "futu-settings-title" : "overnight-settings-title";
      document.getElementById(targetId)?.scrollIntoView({ behavior: "smooth", block: "start" });
    } else if (saved.includes("futu") && futuValue?.enabled) toast.success("已保存，富途牛牛正在启动");
    else if (saved.length === 1) toast.success(SAVED_MESSAGES[saved[0] as Section]);
    else toast.success("投资设置已保存");
  }

  return (
    <form onSubmit={submit} className="investment-settings">
      <fieldset disabled={saving} className="space-y-8">
        {schwab.isError ? (
          <Notice tone="danger" title="Schwab 设置读取失败">
            {schwab.error.message}
          </Notice>
        ) : (
          schwab.data &&
          schwabValue && (
            <SchwabSection
              initial={schwab.data}
              value={schwabValue}
              onChange={(patch) => setSchwabDraft({ ...schwabValue, ...patch })}
              dirty={schwabDirty}
              blockNavigation={dirty}
              error={errors.schwab}
            />
          )
        )}
        {futu.isError ? (
          <Notice tone="danger" title="富途牛牛设置读取失败">
            {futu.error.message}
          </Notice>
        ) : (
          futu.data &&
          futuValue && (
            <FutuSection
              initial={futu.data}
              value={futuValue}
              onChange={(patch) => setFutuDraft({ ...futuValue, ...patch })}
              dirty={futuDirty}
              error={errors.futu}
              onCleared={() => setFutuDraft(null)}
            />
          )
        )}
        {overnight.isError ? (
          <Notice tone="danger" title="夜盘设置读取失败">
            {overnight.error.message}
          </Notice>
        ) : (
          overnight.data && (
            <OvernightSection
              value={overnightValue}
              providerEnabled={overnight.data.providerEnabled || !!futuValue?.enabled}
              onChange={setOvernightDraft}
              error={errors.overnight}
            />
          )
        )}
      </fieldset>
      <div className="settings-dialog-footer">
        {dirty && <Badge tone="warning">有未保存的修改</Badge>}
        {dialog && (
          <Button type="button" variant="secondary" disabled={saving} onClick={dialog.requestClose}>
            取消
          </Button>
        )}
        <Button ref={saveButton} type="submit" disabled={!dirty || saving}>
          {saving ? "保存中…" : "保存"}
        </Button>
      </div>
      <ConfirmDialog
        open={confirmDisable}
        title="停用富途牛牛行情？"
        description={overnight.data?.enabled ? "富途牛牛服务将停止，夜盘行情也将停用。" : "富途牛牛服务将停止。"}
        busy={false}
        confirmLabel="停用并保存"
        onCancel={() => setConfirmDisable(false)}
        onConfirm={() => {
          setConfirmDisable(false);
          void save();
        }}
        onClosed={() => saveButton.current?.focus()}
      />
    </form>
  );
}
