import type { ComponentType, LazyExoticComponent } from "react";
import type { Widget } from "../shared/schema";

/** One entry in a module's top-navigation dropdown. `to` is an in-app path, optionally with a query string. */
export type ModuleMenuItem = {
  label: string;
  to: string;
  description?: string;
  icon?: ComponentType<{ size?: number }>;
};

export type ModuleUI = {
  id: string;
  settings?: LazyExoticComponent<ComponentType<{ enabled: boolean }>>;
  pages: Record<string, LazyExoticComponent<ComponentType>>;
  widgets: Record<string, LazyExoticComponent<ComponentType<{ widget: Widget }>>>;
  icons?: Record<string, ComponentType<{ size?: number }>>;
  /** Dropdown entries under the module's top-navigation item. Omitted: the item is a plain link. */
  menu?: ModuleMenuItem[];
  /**
   * Extra pages mounted below the module's navigation route, keyed by relative path (`positions` →
   * `/investment/positions`). They render only while the module's navigation route is available.
   */
  subpages?: Record<string, LazyExoticComponent<ComponentType>>;
};

const definitions = import.meta.glob<{ default: ModuleUI }>("./*/*.module.ts", { eager: true });
export const pageRegistry: ModuleUI["pages"] = {};
export const widgetRegistry: ModuleUI["widgets"] = {};
export const iconRegistry: NonNullable<ModuleUI["icons"]> = {};
export const settingsRegistry: Record<string, NonNullable<ModuleUI["settings"]>> = {};
export const menuRegistry: Record<string, ModuleMenuItem[]> = {};
export const subpageRegistry: Record<string, NonNullable<ModuleUI["subpages"]>> = {};
const ids = new Set<string>();
for (const { default: module } of Object.values(definitions)) {
  if (ids.has(module.id)) throw new Error(`重复的业务模块：${module.id}`);
  ids.add(module.id);
  if (module.settings) settingsRegistry[module.id] = module.settings;
  if (module.menu?.length) menuRegistry[module.id] = module.menu;
  if (module.subpages) {
    for (const path of Object.keys(module.subpages)) {
      if (!/^[a-z0-9-]+(\/[a-z0-9-]+)*$/.test(path)) throw new Error(`无效的业务子页面路径：${module.id}/${path}`);
    }
    subpageRegistry[module.id] = module.subpages;
  }
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
