import { useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { futuSettingsSchema, overnightSettingsSchema, schwabSettingsSchema, priceMonitorSchema, priceHistorySchema, priceRuleSchema, investmentPreferencesSchema, investmentPreferenceSavedSchema, investmentViewSchema, type InvestmentView, type PriceRuleInput, type FutuSettingsInput, type SchwabSettingsInput } from "./schema";

const PREFERENCES = "/api/modules/investment/preferences";
const VIEW_KEY = ["investment", "preferences", "view"];
export function useInvestmentView() {
  const [writer] = useState(() => Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join(""));
  const [draft, setDraft] = useState<InvestmentView | null>(null);
  const base = useRef<number | null>(null);
  const sequence = useRef(0);
  const query = useQuery({ queryKey: VIEW_KEY, gcTime: 0, refetchOnWindowFocus: false, queryFn: async () => {
    const result = await apiValidated(PREFERENCES, investmentPreferencesSchema);
    const entry = result.entries.view;
    return { revision: entry?.revision ?? 0, value: investmentViewSchema.parse(entry?.value ?? { tab: "terminal", expanded: false }) };
  } });
  const save = useMutation({
    scope: { id: "investment-view" },
    mutationFn: (value: InvestmentView) => {
      base.current ??= query.data?.revision ?? 0;
      return apiValidated(PREFERENCES, investmentPreferenceSavedSchema, { method: "PUT", keepalive: true,
        body: JSON.stringify({ name: "view", value, revision: base.current, writer, sequence: ++sequence.current }),
      });
    },
  });
  return { query, save, view: draft ?? query.data?.value,
    setView(value: InvestmentView) { setDraft(value); save.mutate(value); },
  };
}

const MONITOR = "/api/modules/investment/monitor";
export function usePriceMonitor() {
  return useQuery({ queryKey: ["investment", "monitor"], queryFn: () => apiValidated(MONITOR, priceMonitorSchema), refetchInterval: 15_000 });
}
export function usePriceHistory() {
  return useInfiniteQuery({ queryKey: ["investment", "price-history"], initialPageParam: "",
    queryFn: ({ pageParam }) => apiValidated(`${MONITOR}/history?cursor=${encodeURIComponent(pageParam)}`, priceHistorySchema),
    getNextPageParam: (page) => page.nextCursor || undefined, refetchInterval: 30_000 });
}
export function savePriceRule({ id, ...input }: PriceRuleInput & { id?: string }) {
  return apiValidated(`${MONITOR}/rules${id ? `/${encodeURIComponent(id)}` : ""}`, priceRuleSchema, { method: id ? "PUT" : "POST", body: JSON.stringify(input) });
}
export function deletePriceRule(id: string) {
  return api(`${MONITOR}/rules/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function useInvestmentWidget(widget: Widget) {
  return useQuery({ queryKey: ["widget", widget.id], queryFn: async () => schwabSettingsSchema.parse(await api(widget.dataRoute)), refetchInterval: 30_000 });
}

export function useSchwabSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "schwab"], enabled, queryFn: async () => schwabSettingsSchema.parse(await api("/api/modules/investment/schwab")), refetchInterval: 30_000 });
}

export function saveSchwabSettings(input: SchwabSettingsInput) {
  return apiValidated("/api/modules/investment/schwab", schwabSettingsSchema, { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectSchwab() {
  return api("/api/modules/investment/schwab/disconnect", { method: "POST", body: "{}" });
}

export function refreshSchwabToken() {
  return api("/api/modules/investment/schwab/oauth/refresh", { method: "POST", body: "{}" });
}

export function useFutuSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "futu"], enabled, queryFn: async () => futuSettingsSchema.parse(await api("/api/modules/investment/futu")), refetchInterval: (query) => query.state.data?.enabled && !query.state.data?.qotLogined ? 2_000 : 30_000 });
}

export function saveFutuSettings(input: FutuSettingsInput) {
  return apiValidated("/api/modules/investment/futu", futuSettingsSchema, { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectFutu() {
  return api("/api/modules/investment/futu/disconnect", { method: "POST", body: "{}" });
}

export function useOvernightSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "overnight"], enabled, queryFn: async () => overnightSettingsSchema.parse(await api("/api/modules/investment/overnight")), refetchInterval: 30_000 });
}

export function saveOvernightSettings(enabled: boolean) {
  return apiValidated("/api/modules/investment/overnight", overnightSettingsSchema, { method: "PUT", body: JSON.stringify({ enabled }) });
}

export function useRefreshInvestment() {
  const client = useQueryClient();
  return () => Promise.all([["investment"], ["widget", "investment.overview"]].map((queryKey) => client.invalidateQueries({ queryKey })));
}
