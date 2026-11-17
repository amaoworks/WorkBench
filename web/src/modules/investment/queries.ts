import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { moduleQueryKey } from "../../shared/queryKeys";
import { widgetSeed } from "../../shared/dashboard";
import { marketDataStatusSchema, overviewAlertsSchema, overviewQuotesSchema, overviewSnapshotSchema, positionsResponseSchema, ordersResponseSchema, netWorthHistorySchema, futuSettingsSchema, overnightSettingsSchema, schwabSettingsSchema, priceMonitorSchema, priceHistorySchema, priceRuleSchema, type PriceRuleInput, type FutuSettingsInput, type SchwabSettingsInput } from "./schema";

const investmentKey = (...parts: readonly unknown[]) => moduleQueryKey("investment", ...parts);

const MONITOR = "/api/modules/investment/monitor";
export function usePriceMonitor() {
  return useQuery({ queryKey: investmentKey("monitor"), queryFn: () => apiValidated(MONITOR, priceMonitorSchema), refetchInterval: 15_000 });
}
export function usePriceHistory() {
  return useInfiniteQuery({ queryKey: investmentKey("price-history"), initialPageParam: "",
    queryFn: ({ pageParam }) => apiValidated(`${MONITOR}/history?cursor=${encodeURIComponent(pageParam)}`, priceHistorySchema),
    getNextPageParam: (page) => page.nextCursor || undefined, refetchInterval: 30_000 });
}
export function savePriceRule({ id, ...input }: PriceRuleInput & { id?: string }) {
  return apiValidated(`${MONITOR}/rules${id ? `/${encodeURIComponent(id)}` : ""}`, priceRuleSchema, { method: id ? "PUT" : "POST", body: JSON.stringify(input) });
}
export function deletePriceRule(id: string) {
  return api(`${MONITOR}/rules/${encodeURIComponent(id)}`, { method: "DELETE" });
}

// Overview cards. Seeded from /api/dashboard?include=data, then polled on their own routes.
export function useOverviewSnapshot(widget: Widget) {
  return useQuery({ queryKey: investmentKey("widget", widget.id), ...widgetSeed(widget, overviewSnapshotSchema),
    queryFn: () => apiValidated(widget.dataRoute, overviewSnapshotSchema), refetchInterval: 60_000 });
}
export function useOverviewQuotes(widget: Widget) {
  return useQuery({ queryKey: investmentKey("widget", widget.id), ...widgetSeed(widget, overviewQuotesSchema),
    queryFn: () => apiValidated(widget.dataRoute, overviewQuotesSchema), refetchInterval: 30_000 });
}
export function useOverviewAlerts(widget: Widget) {
  return useQuery({ queryKey: investmentKey("widget", widget.id), ...widgetSeed(widget, overviewAlertsSchema),
    queryFn: () => apiValidated(widget.dataRoute, overviewAlertsSchema), refetchInterval: 60_000 });
}

export function useMarketDataStatus(enabled = true) {
  return useQuery({ queryKey: investmentKey("market-data"), enabled,
    queryFn: () => apiValidated("/api/modules/investment/market-data", marketDataStatusSchema), refetchInterval: 15_000 });
}

export function useSchwabSettings(enabled: boolean) {
  return useQuery({ queryKey: investmentKey("schwab"), enabled, queryFn: () => apiValidated("/api/modules/investment/schwab", schwabSettingsSchema), refetchInterval: 30_000 });
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
  return useQuery({ queryKey: investmentKey("futu"), enabled, queryFn: () => apiValidated("/api/modules/investment/futu", futuSettingsSchema), refetchInterval: (query) => query.state.data?.enabled && !query.state.data?.qotLogined ? 2_000 : 30_000 });
}

export function saveFutuSettings(input: FutuSettingsInput) {
  return apiValidated("/api/modules/investment/futu", futuSettingsSchema, { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectFutu() {
  return api("/api/modules/investment/futu/disconnect", { method: "POST", body: "{}" });
}

export function useOvernightSettings(enabled: boolean) {
  return useQuery({ queryKey: investmentKey("overnight"), enabled, queryFn: () => apiValidated("/api/modules/investment/overnight", overnightSettingsSchema), refetchInterval: 30_000 });
}

export function saveOvernightSettings(enabled: boolean) {
  return apiValidated("/api/modules/investment/overnight", overnightSettingsSchema, { method: "PUT", body: JSON.stringify({ enabled }) });
}

export function useRefreshInvestment() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: investmentKey() });
}

export function usePositions() {
  return useQuery({
    queryKey: investmentKey("positions"),
    queryFn: () => apiValidated("/api/modules/investment/positions", positionsResponseSchema),
    refetchInterval: 30_000,
  });
}

export function useOrders(days: number, status: string) {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return useQuery({
    queryKey: investmentKey("orders", days, status, tz),
    queryFn: () => apiValidated(`/api/modules/investment/orders?${new URLSearchParams({ days: String(days), status, tz })}`, ordersResponseSchema),
    refetchInterval: 30_000,
  });
}

export function useNetWorthHistory(range: string, account: string) {
  return useQuery({
    queryKey: investmentKey("networth", range, account),
    queryFn: () => apiValidated(`/api/modules/investment/networth?${new URLSearchParams({ range, account })}`, netWorthHistorySchema),
    refetchInterval: 60_000,
  });
}
