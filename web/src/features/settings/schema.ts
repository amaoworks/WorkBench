import { z } from "zod";

export const appearanceSchema = z.object({
  theme: z.enum(["light", "dark", "system"]),
  motion: z.enum(["full", "reduced"]),
  palette: z.enum(["default", "claude"]).default("default"),
});
export const aiSettingsSchema = z.object({ enabled: z.boolean(), model: z.string(), baseUrl: z.string(), hasApiKey: z.boolean() });
export const loggingSettingsSchema = z.object({ level: z.enum(["debug", "info", "warn", "error"]) });
export const settingsSchema = z.object({
  ai: aiSettingsSchema.optional(), appearance: appearanceSchema, logging: loggingSettingsSchema.optional(),
  deployment: z.object({ listenAddress: z.string(), dataPath: z.string(), databaseDriver: z.enum(["sqlite", "postgres"]), backupSupported: z.boolean(), authMode: z.enum(["local", "password"]), publicUrl: z.string(), allowedHosts: z.array(z.string()).nullable() }).optional()
});
export const aiTestSchema = z.object({ latencyMs: z.number() });
export const telegramSettingsSchema = z.object({
  enabled: z.boolean(), chatId: z.string(), hasBotToken: z.boolean(), failedDeliveries: z.number().int(),
  status: z.object({ lastAttemptAt: z.string().nullable(), lastSuccessAt: z.string().nullable(), lastError: z.string() })
});
export const telegramRecipientSchema = z.object({
  enabled: z.boolean(), chatId: z.string(), configured: z.boolean(), hasBotToken: z.boolean(),
  status: z.object({ lastAttemptAt: z.string().nullable(), lastSuccessAt: z.string().nullable(), lastError: z.string() }),
});
export type TelegramRecipient = z.infer<typeof telegramRecipientSchema>;
export const telegramTestSchema = z.object({ ok: z.boolean() });
export type TelegramSettings = z.infer<typeof telegramSettingsSchema>;
export const backupSchema = z.object({ file: z.string() });
export const managedUserSchema = z.object({
  id: z.string().min(1), username: z.string().min(1), displayName: z.string(),
  role: z.enum(["admin", "user"]), status: z.enum(["active", "disabled"]),
  mustChangePassword: z.boolean(), createdAt: z.iso.datetime({ offset: true }), updatedAt: z.iso.datetime({ offset: true }),
});
export const managedUsersSchema = z.object({ items: z.array(managedUserSchema) });
export const createdUserSchema = z.object({ user: managedUserSchema, initialPassword: z.string().min(1) });
export const updatedUserSchema = z.object({ user: managedUserSchema });
export const resetUserPasswordSchema = z.object({ initialPassword: z.string().min(1) });
export type ManagedUser = z.infer<typeof managedUserSchema>;
const sessionTimestampSchema = z.iso.datetime({ offset: true });
export const deviceSessionSchema = z.object({
  id: z.string().min(1),
  deviceName: z.string().min(1),
  platform: z.enum(["web", "ios", "android", "other"]),
  createdAt: sessionTimestampSchema,
  lastSeenAt: sessionTimestampSchema,
  expiresAt: sessionTimestampSchema,
  current: z.boolean(),
});
export const deviceSessionsSchema = z.object({ items: z.array(deviceSessionSchema) });
export const revokedSessionSchema = z.object({ revoked: z.literal(true) });
export const logoutAllSchema = z.object({ reauthenticate: z.literal(true) });
export type DeviceSession = z.infer<typeof deviceSessionSchema>;
export type Appearance = z.infer<typeof appearanceSchema>;
export type AISettings = z.infer<typeof aiSettingsSchema>;
export type LoggingSettings = z.infer<typeof loggingSettingsSchema>;
export type Settings = z.infer<typeof settingsSchema>;
