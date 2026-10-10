import { z } from "zod";

export const priceRuleSchema = z.object({
  id: z.string(), symbol: z.string(), direction: z.enum(["up", "down"]), thresholdPercent: z.number(), enabled: z.boolean(),
  price: z.number().nullable(), previousClose: z.number().nullable(), changePercent: z.number().nullable(),
  quoteAt: z.string().nullable(), checkedAt: z.string().nullable(), lastError: z.string()
});
export const priceMonitorSchema = z.object({
  items: z.array(priceRuleSchema), status: z.enum(["idle", "waiting", "monitoring", "closed", "error", "stale"]),
  checkedAt: z.string().nullable(), lastError: z.string(), intervalSeconds: z.number()
});
export const priceHistorySchema = z.object({
  items: z.array(z.object({ id: z.string(), ruleId: z.string(), symbol: z.string(), direction: z.enum(["up", "down"]),
    thresholdPercent: z.number(), price: z.number(), previousClose: z.number(), changePercent: z.number(),
    quoteAt: z.string(), triggeredAt: z.string(), tradingDate: z.string(), notificationId: z.string() })),
  nextCursor: z.string()
});
export type PriceRule = z.infer<typeof priceRuleSchema>;
export type PriceRuleInput = Pick<PriceRule, "symbol" | "direction" | "thresholdPercent" | "enabled">;

export const schwabSettingsSchema = z.object({
  appKey: z.string(),
  callbackUrl: z.string(),
  hasAppSecret: z.boolean(),
  connected: z.boolean(),
  reauthorizationRequired: z.boolean(),
  tokenExpiresAt: z.string().optional(),
  lastError: z.string()
});
export type SchwabSettings = z.infer<typeof schwabSettingsSchema>;
export type SchwabSettingsInput = { appKey: string; appSecret: string; callbackUrl: string };

export const futuSettingsSchema = z.object({
  enabled: z.boolean(),
  overnightEnabled: z.boolean(),
  managed: z.boolean(),
  serviceState: z.enum(["external", "stopped", "installing", "starting", "running", "stopping", "error"]),
  serviceError: z.string(),
  account: z.string(),
  hasPassword: z.boolean(),
  connected: z.boolean(),
  qotLogined: z.boolean(),
  lastError: z.string(),
  subUsed: z.number().optional(),
  subRemain: z.number().optional(),
  historyRemain: z.number().optional()
});
export type FutuSettings = z.infer<typeof futuSettingsSchema>;
export type FutuSettingsInput = { enabled: boolean; account: string; password: string; clearPassword: boolean };

export const overnightSettingsSchema = z.object({ enabled: z.boolean(), provider: z.literal("futu"), providerEnabled: z.boolean() });

export const investmentViewSchema = z.object({ tab: z.enum(["terminal", "monitor"]), expanded: z.boolean() });
export const investmentViewPreferencesSchema = z.object({
  entries: z.object({
    view: z.object({
      revision: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER),
      value: investmentViewSchema.nullable(),
    }).optional(),
  }),
});
export const investmentPreferenceSavedSchema = z.object({
  revision: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER),
  sequence: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER),
});
export type InvestmentView = z.infer<typeof investmentViewSchema>;
