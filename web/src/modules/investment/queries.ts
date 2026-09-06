import { useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api } from "../../shared/api";
export const overviewSchema = z.object({
  source: z.literal("mock"),
  items: z.array(z.object({ symbol: z.string(), name: z.string(), priceCents: z.number(), changeBps: z.number(), asOf: z.string() })),
  summary: z.object({ content: z.string(), createdAt: z.string() }).nullable()
});
export function useOverview() {
  return useQuery({ queryKey: ["investment", "overview"], queryFn: async () => overviewSchema.parse(await api<unknown>("/api/modules/investment/overview")), refetchInterval: 60_000 });
}
