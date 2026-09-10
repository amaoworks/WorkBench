import { useQuery } from "@tanstack/react-query";
import { api } from "../../shared/api";
import type { Widget } from "../../shared/schema";
import { overviewSchema } from "./schema";

export function useOverview() {
  return useQuery({ queryKey: ["investment", "overview"], queryFn: async () => overviewSchema.parse(await api<unknown>("/api/modules/investment/overview")), refetchInterval: 60_000 });
}

export function useInvestmentWidget(widget: Widget) {
  return useQuery({ queryKey: ["widget", widget.id], queryFn: async () => overviewSchema.parse(await api<unknown>(widget.dataRoute)), refetchInterval: 60_000 });
}

export function syncQuotes() {
  return api("/api/modules/investment/sync", { method: "POST", body: "{}" });
}

export function generateSummary() {
  return api("/api/modules/investment/summary", { method: "POST", body: "{}" });
}
