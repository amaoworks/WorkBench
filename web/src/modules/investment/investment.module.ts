import { lazy } from "react";
import { ChartNoAxesCombined } from "lucide-react";
import type { ModuleUI } from "../registry";
export default {
  id: "investment",
  settings: lazy(() => import("./InvestmentSettings")),
  pages: { "investment.overview": lazy(() => import("./InvestmentPage")) },
  widgets: {
    "investment.snapshot": lazy(() => import("./InvestmentSnapshotWidget")),
    "investment.quotes": lazy(() => import("./InvestmentQuotesWidget")),
    "investment.alerts": lazy(() => import("./InvestmentAlertsWidget"))
  },
  icons: { "investment.chart": ChartNoAxesCombined }
} satisfies ModuleUI;
