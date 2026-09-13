import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { futuSettingsSchema, schwabSettingsSchema, type FutuSettingsInput, type SchwabSettingsInput } from "./schema";

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
  return useQuery({ queryKey: ["investment", "futu"], enabled, queryFn: async () => futuSettingsSchema.parse(await api<unknown>("/api/modules/investment/futu")), refetchInterval: 30_000 });
}

export function saveFutuSettings(input: FutuSettingsInput) {
  return api("/api/modules/investment/futu", { method: "PUT", body: JSON.stringify(input) });
}

export function disconnectFutu() {
  return api("/api/modules/investment/futu/disconnect", { method: "POST", body: "{}" });
}

export function useRefreshInvestment() {
  const client = useQueryClient();
  return () => {
    for (const queryKey of [["investment"], ["widget", "investment.overview"]]) void client.invalidateQueries({ queryKey });
  };
}
