import { z } from "zod";

export const appearanceSchema = z.object({ theme: z.enum(["light", "dark", "system"]), motion: z.enum(["full", "reduced"]) });
export const aiSettingsSchema = z.object({ enabled: z.boolean(), model: z.string(), baseUrl: z.string(), hasApiKey: z.boolean() });
export const loggingSettingsSchema = z.object({ level: z.enum(["debug", "info", "warn", "error"]) });
export const settingsSchema = z.object({
  ai: aiSettingsSchema, appearance: appearanceSchema, logging: loggingSettingsSchema,
  deployment: z.object({ listenAddress: z.string(), dataPath: z.string(), authMode: z.enum(["local", "password"]), publicUrl: z.string(), allowedHosts: z.array(z.string()).nullable() })
});
export const aiTestSchema = z.object({ latencyMs: z.number() });
export const backupSchema = z.object({ file: z.string() });
export type Appearance = z.infer<typeof appearanceSchema>;
export type AISettings = z.infer<typeof aiSettingsSchema>;
export type LoggingSettings = z.infer<typeof loggingSettingsSchema>;
export type Settings = z.infer<typeof settingsSchema>;
