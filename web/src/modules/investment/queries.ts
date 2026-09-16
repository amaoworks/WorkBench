import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { futuSettingsSchema, overnightSettingsSchema, schwabSettingsSchema, type FutuSettingsInput, type SchwabSettingsInput } from "./schema";

export function useInvestmentWidget(widget: Widget) {
  return useQuery({ queryKey: ["widget", widget.id], queryFn: async () => schwabSettingsSchema.parse(await api<unknown>(widget.dataRoute)), refetchInterval: 30_000 });
}

export function useSchwabSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "schwab"], enabled, queryFn: async () => schwabSettingsSchema.parse(await api<unknown>("/api/modules/investment/schwab")), refetchInterval: 30_000 });
}

export function saveSchwabSettings(input: SchwabSettingsInput) {
  return api("/api/modules/investment/schwab", { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectSchwab() {
  return api("/api/modules/investment/schwab/disconnect", { method: "POST", body: "{}" });
}

export function refreshSchwabToken() {
  return api("/api/modules/investment/schwab/oauth/refresh", { method: "POST", body: "{}" });
}

export function useFutuSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "futu"], enabled, queryFn: async () => futuSettingsSchema.parse(await api<unknown>("/api/modules/investment/futu")), refetchInterval: (query) => query.state.data?.enabled && !query.state.data?.qotLogined ? 2_000 : 30_000 });
}

export function saveFutuSettings(input: FutuSettingsInput) {
  return api("/api/modules/investment/futu", { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectFutu() {
  return api("/api/modules/investment/futu/disconnect", { method: "POST", body: "{}" });
}

export function useOvernightSettings(enabled: boolean) {
  return useQuery({ queryKey: ["investment", "overnight"], enabled, queryFn: async () => overnightSettingsSchema.parse(await api<unknown>("/api/modules/investment/overnight")), refetchInterval: 30_000 });
}

export function saveOvernightSettings(enabled: boolean) {
  return api("/api/modules/investment/overnight", { method: "PUT", body: JSON.stringify({ enabled }) });
}

export function useRefreshInvestment() {
  const client = useQueryClient();
  return () => Promise.all([["investment"], ["widget", "investment.overview"]].map((queryKey) => client.invalidateQueries({ queryKey })));
}
