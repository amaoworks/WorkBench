import { createContext, useContext } from "react";
import type { ZodType } from "zod";
import type { Widget } from "./schema";

/**
 * Cross-card signals on the overview. Business modules must not import each other, so cards that
 * need another module's facts (news needs held symbols from investment) exchange plain values here.
 */
export type DashboardSignals = {
  heldSymbols: readonly string[];
  setHeldSymbols: (symbols: readonly string[]) => void;
};

export const DashboardSignalsContext = createContext<DashboardSignals>({ heldSymbols: [], setHeldSymbols: () => {} });

export function useDashboardSignals() {
  return useContext(DashboardSignalsContext);
}

/** Element in the card header where a widget may render its badges and actions. */
export const WidgetHeaderSlotContext = createContext<HTMLElement | null>(null);

/**
 * Query options that seed a widget query from the aggregated dashboard response. Invalid or absent
 * data is ignored, so the widget simply fetches its own route as before.
 */
export function widgetSeed<T>(widget: Widget, schema: ZodType<T>): { initialData?: T; initialDataUpdatedAt?: number } {
  if (widget.data === undefined) return {};
  const parsed = schema.safeParse(widget.data);
  return parsed.success ? { initialData: parsed.data, initialDataUpdatedAt: Date.now() } : {};
}
