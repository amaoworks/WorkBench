import type { ComponentType } from "react";
import {
  Bell,
  BellDot,
  Bot,
  Database,
  Fingerprint,
  LayoutDashboard,
  LayoutGrid,
  Paintbrush,
  Radio,
  SlidersHorizontal,
  Blocks,
} from "lucide-react";
import type { ModuleMenuItem } from "../modules/registry";

export type NavEntry = {
  key: string;
  label: string;
  to: string;
  icon: ComponentType<{ size?: number }>;
  badge?: number;
  menu: ModuleMenuItem[];
};

/** Dropdowns of the platform pages. Module dropdowns come from each module's `menu` field. */
export const overviewMenu: ModuleMenuItem[] = [
  { label: "今日概况", to: "/", description: "待办、资讯与资产一页看完", icon: LayoutDashboard },
  { label: "配置卡片", to: "/?edit=1", description: "显示、排序与尺寸", icon: LayoutGrid },
  { label: "管理业务", to: "/settings?tab=modules", description: "启用或停用业务模块", icon: Blocks },
];

export const notificationsMenu: ModuleMenuItem[] = [
  { label: "全部通知", to: "/notifications", description: "按日期分组的提醒", icon: Bell },
  { label: "未读", to: "/notifications?filter=unread", description: "尚未处理的提醒", icon: BellDot },
  { label: "推送设置", to: "/settings?tab=notifications", description: "Telegram 推送", icon: Radio },
];

export const settingsMenu: ModuleMenuItem[] = [
  { label: "业务", to: "/settings?tab=modules", description: "模块开关与连接", icon: Blocks },
  { label: "AI 配置", to: "/settings?tab=ai", description: "模型与接口", icon: Bot },
  { label: "通知推送", to: "/settings?tab=notifications", description: "Telegram", icon: Radio },
  { label: "账户安全", to: "/settings?tab=security", description: "登录密码", icon: Fingerprint },
  { label: "外观", to: "/settings?tab=appearance", description: "风格、主题与动效", icon: Paintbrush },
  { label: "数据与运行", to: "/settings?tab=data", description: "日志、备份与部署信息", icon: Database },
];

export const settingsIcon = SlidersHorizontal;

/** True when the current location satisfies the path and every query parameter of `to`. */
export function matchesLocation(to: string, pathname: string, search: string) {
  const target = new URL(to, "http://workbench.local");
  if (target.pathname !== pathname) return false;
  const current = new URLSearchParams(search);
  const wanted = [...target.searchParams.entries()];
  if (wanted.length === 0) return [...current.keys()].every((key) => !["view", "tab", "filter", "edit", "new"].includes(key));
  return wanted.every(([key, value]) => current.get(key) === value);
}
