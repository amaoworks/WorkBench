import { Notice } from "../../components/ui/Notice";
import { Switch } from "../../components/ui/Switch";

export function OvernightSection({
  value,
  providerEnabled,
  onChange,
  error,
}: {
  value: boolean;
  providerEnabled: boolean;
  onChange: (enabled: boolean) => void;
  error?: string;
}) {
  return (
    <section className="space-y-4" aria-labelledby="overnight-settings-title">
      <h3 id="overnight-settings-title" className="font-medium">
        夜盘行情
      </h3>
      {error && (
        <Notice tone="danger" title="夜盘设置未保存">
          {error}
        </Notice>
      )}
      <div className="flex items-center gap-2 text-sm">
        <Switch label="启用夜盘行情" checked={value} disabled={!providerEnabled} onChange={onChange} />
        启用夜盘行情
      </div>
      {!providerEnabled && <Notice title="富途牛牛行情未启用">启用后可使用夜盘行情。</Notice>}
    </section>
  );
}
