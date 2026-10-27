import { lazy } from "react";
import { CalendarCheck2, CalendarDays, CalendarRange, CheckCheck, CheckSquare2, ListTodo, Plus, RefreshCcw } from "lucide-react";
import type { ModuleUI } from "../registry";

export default {
  id: "todo",
  settings: lazy(() => import("./WallosSettings")),
  pages: { "todo.list": lazy(() => import("./TodoPage")) },
  widgets: { "todo.today": lazy(() => import("./TodoTodayWidget")) },
  icons: { "check-square": CheckSquare2 },
  menu: [
    { label: "今天", to: "/todo?view=today", description: "今天到期与已逾期", icon: CalendarCheck2 },
    { label: "本周", to: "/todo?view=week", description: "本周内到期", icon: CalendarRange },
    { label: "以后", to: "/todo?view=later", description: "下周及以后、无截止时间", icon: CalendarDays },
    { label: "全部", to: "/todo", description: "按截止时间分组", icon: ListTodo },
    { label: "已完成", to: "/todo?view=done", description: "完成记录", icon: CheckCheck },
    { label: "新建待办", to: "/todo?new=1", description: "快速记录一件事", icon: Plus },
    { label: "Wallos 续费", to: "/settings?tab=modules&module=todo", description: "订阅续费提醒同步", icon: RefreshCcw },
  ],
} satisfies ModuleUI;
