import type { ComponentType } from "react";
import { SlidersHorizontal } from "lucide-react";
import type { ModuleMenuItem } from "../modules/registry";

export type NavEntry = {
  key: string;
  label: string;
  to: string;
  icon: ComponentType<{ size?: number }>;
  badge?: number;
  /** Dropdown entries; empty for a plain link. Only modules that declare `menu` get one. */
  menu: ModuleMenuItem[];
};

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
