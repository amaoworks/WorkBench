import SchwabSettings from "./SchwabSettings";
import FutuSettings from "./FutuSettings";
import OvernightSettings from "./OvernightSettings";

export default function InvestmentSettings({ enabled }: { enabled: boolean }) {
  return <div className="space-y-8">
    <SchwabSettings enabled={enabled} />
    <FutuSettings enabled={enabled} />
    <OvernightSettings enabled={enabled} />
  </div>;
}
