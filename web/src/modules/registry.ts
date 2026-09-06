import type { ComponentType, LazyExoticComponent } from "react";
import type { Widget } from "../shared/schema";

export type ModuleUI = {
  id: string;
  pages: Record<string, LazyExoticComponent<ComponentType>>;
  widgets: Record<string, LazyExoticComponent<ComponentType<{ widget: Widget }>>>;
  icons?: Record<string, ComponentType<{ size?: number }>>;
};

const definitions = import.meta.glob<{ default: ModuleUI }>("./*/*.module.ts", { eager: true });
export const pageRegistry: ModuleUI["pages"] = {};
export const widgetRegistry: ModuleUI["widgets"] = {};
export const iconRegistry: NonNullable<ModuleUI["icons"]> = {};
const ids = new Set<string>();
for (const { default: module } of Object.values(definitions)) {
  if (ids.has(module.id)) throw new Error(`重复的业务模块：${module.id}`);
  ids.add(module.id);
  for (const [target, entries] of [[pageRegistry, module.pages], [widgetRegistry, module.widgets]] as const) {
    for (const key of Object.keys(entries)) {
      if (!key.startsWith(`${module.id}.`) || key in target) throw new Error(`无效或重复的业务界面标识：${key}`);
    }
    Object.assign(target, entries);
  }
  for (const [key, icon] of Object.entries(module.icons ?? {})) {
    if (key in iconRegistry) throw new Error(`重复的图标标识：${key}`);
    iconRegistry[key] = icon;
  }
}
