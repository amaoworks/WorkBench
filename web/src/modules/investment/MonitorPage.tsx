import { PageHeader } from "../../components/ui/PageHeader";
import { InvestmentMonitor } from "./InvestmentMonitor";

export default function MonitorPage() {
  return (
    <div className="page monitor-page">
      <PageHeader title="价格监控" />
      <InvestmentMonitor />
    </div>
  );
}
