import { z } from "zod";

export const overviewSchema = z.object({
  source: z.literal("mock"),
  items: z.array(z.object({ symbol: z.string(), name: z.string(), priceCents: z.number(), changeBps: z.number(), asOf: z.string() })),
  summary: z.object({ content: z.string(), createdAt: z.string() }).nullable()
});
