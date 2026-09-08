import { lazy } from "react";
import { CheckSquare2 } from "lucide-react";
import type { ModuleUI } from "../registry";

export default {
  id: "todo",
  settings: lazy(() => import("./WallosSettings")),
  pages: { "todo.list": lazy(() => import("./TodoPage")) },
  widgets: { "todo.summary": lazy(() => import("./TodoSummaryWidget")) },
  icons: { "check-square": CheckSquare2 }
} satisfies ModuleUI;
