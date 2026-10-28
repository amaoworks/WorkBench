import { createContext, useContext, useEffect } from "react";
import type { ZodType } from "zod";
import type { Widget } from "./schema";

/**
 * Cross-card signals on the overview. Business modules must not import each other, so cards that
 * need another module's facts (news needs held symbols from investment) exchange plain values here.
 */
export type DashboardSignals = {
  heldSymbols: readonly string[];
  setHeldSymbols: (symbols: readonly string[]) => void;
  summaries: Readonly<Record<string, OverviewSummary>>;
  setSummary: (id: string, summary: OverviewSummary | null) => void;
};

/**
 * Compact figure a card contributes to the 今日概况 column and the headline sentence, e.g.
 * `{ title: "今日待办", value: 3, unit: "项待完成" }`.
 */
export type OverviewSummary = {
  title: string;
  value: number;
  unit: string;
  detail?: string;
  to: string;
};

export const DashboardSignalsContext = createContext<DashboardSignals>({
  heldSymbols: [],
  setHeldSymbols: () => {},
  summaries: {},
  setSummary: () => {},
});

export function useDashboardSignals() {
  return useContext(DashboardSignalsContext);
}

/** Publishes (or withdraws, with `null`) a card's summary while the card is mounted. Keyed by widget kind. */
export function useOverviewSummary(id: string, summary: OverviewSummary | null) {
  const { setSummary } = useDashboardSignals();
  const serialized = summary ? JSON.stringify(summary) : "";
  useEffect(() => {
    setSummary(id, serialized ? (JSON.parse(serialized) as OverviewSummary) : null);
  }, [id, serialized, setSummary]);
  useEffect(() => () => setSummary(id, null), [id, setSummary]);
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
