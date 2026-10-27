import { lazy } from "react";
import { BookOpenText, Filter, Newspaper, Rss, ScrollText } from "lucide-react";
import type { ModuleUI } from "../registry";

export default {
  id: "news",
  settings: lazy(() => import("./NewsSettings")),
  pages: { "news.list": lazy(() => import("./NewsPage")) },
  widgets: { "news.brief": lazy(() => import("./NewsBriefWidget")) },
  icons: { newspaper: Newspaper },
  menu: [
    { label: "文章", to: "/news", description: "订阅源的最新文章", icon: BookOpenText },
    { label: "订阅源", to: "/news?tab=sources", description: "添加与管理 RSS 源", icon: Rss },
    { label: "关注规则", to: "/news?tab=rules", description: "关键词与代码匹配", icon: Filter },
    { label: "简报", to: "/news?tab=digests", description: "AI 生成的阅读简报", icon: ScrollText },
  ],
} satisfies ModuleUI;
