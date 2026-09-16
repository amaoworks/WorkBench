import { z } from "zod";

export const schwabSettingsSchema = z.object({
  appKey: z.string(),
  callbackUrl: z.string(),
  hasAppSecret: z.boolean(),
  connected: z.boolean(),
  reauthorizationRequired: z.boolean().default(false),
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
