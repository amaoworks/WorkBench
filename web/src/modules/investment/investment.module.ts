import { lazy } from "react";
import { BellRing, CandlestickChart, ChartNoAxesCombined, ClipboardList, Wallet } from "lucide-react";
import type { ModuleUI } from "../registry";
export default {
  id: "investment",
  settings: lazy(() => import("./InvestmentSettings")),
  pages: { "investment.overview": lazy(() => import("./InvestmentPage")) },
  subpages: {
    positions: lazy(() => import("./PositionsPage")),
    orders: lazy(() => import("./OrdersPage")),
    monitor: lazy(() => import("./MonitorPage")),
  },
  widgets: {
    "investment.snapshot": lazy(() => import("./InvestmentSnapshotWidget")),
    "investment.quotes": lazy(() => import("./InvestmentQuotesWidget")),
    "investment.alerts": lazy(() => import("./InvestmentAlertsWidget"))
  },
  icons: { "investment.chart": ChartNoAxesCombined },
  menu: [
    { label: "图表", to: "/investment", icon: CandlestickChart },
    { label: "价格监控", to: "/investment/monitor", icon: BellRing },
    { label: "持仓", to: "/investment/positions", icon: Wallet },
    { label: "订单", to: "/investment/orders", icon: ClipboardList },
  ],
} satisfies ModuleUI;
