import { lazy } from "react";
import { ChartNoAxesCombined } from "lucide-react";
import type { ModuleUI } from "../registry";
export default {
  id: "investment",
  settings: lazy(() => import("./InvestmentSettings")),
  pages: { "investment.overview": lazy(() => import("./InvestmentPage")) },
  widgets: { "investment.overview": lazy(() => import("./InvestmentWidget")) },
  icons: { "investment.chart": ChartNoAxesCombined }
} satisfies ModuleUI;
