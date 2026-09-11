import { z } from "zod";

export const schwabSettingsSchema = z.object({
  appKey: z.string(),
  callbackUrl: z.string(),
  hasAppSecret: z.boolean(),
  connected: z.boolean(),
  tokenExpiresAt: z.string().optional(),
  lastError: z.string()
});
export type SchwabSettings = z.infer<typeof schwabSettingsSchema>;
export type SchwabSettingsInput = { appKey: string; appSecret: string; callbackUrl: string };
