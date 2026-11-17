import { lazy } from "react";
import { Newspaper } from "lucide-react";
import type { ModuleUI } from "../registry";

export default {
  id: "news",
  settings: lazy(() => import("./NewsSettings")),
  pages: { "news.list": lazy(() => import("./NewsPage")) },
  widgets: { "news.brief": lazy(() => import("./NewsBriefWidget")) },
  widgetMeta: { "news.brief": { icon: Newspaper, summaryOrder: 2 } },
  icons: { newspaper: Newspaper },
} satisfies ModuleUI;
