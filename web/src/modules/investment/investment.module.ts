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
    { label: "图表", to: "/investment", description: "图表、下单与账户管理", icon: CandlestickChart },
    { label: "价格监控", to: "/investment/monitor", description: "价格规则与触发记录", icon: BellRing },
    { label: "持仓", to: "/investment/positions", description: "账户净值与持仓明细", icon: Wallet },
    { label: "订单", to: "/investment/orders", description: "委托与成交记录", icon: ClipboardList },
  ],
} satisfies ModuleUI;
