import { z } from "zod";

export const taskSchema = z.object({
  id: z.string(), title: z.string(), description: z.string(),
  dueAt: z.string().optional(), completedAt: z.string().optional(),
  createdAt: z.string(), updatedAt: z.string()
});
export type Task = z.infer<typeof taskSchema>;

export const tasksResponseSchema = z.object({ items: z.array(taskSchema), nextCursor: z.string().optional() });
export const summarySchema = z.object({ open: z.number(), overdue: z.number(), completed: z.number() });

export const wallosSettingsSchema = z.object({
  enabled: z.boolean(), baseUrl: z.string(), hasApiKey: z.boolean(),
  daysBefore: z.number(), reminderHour: z.number(), timeZone: z.string(),
  lastSync: z.string().optional(), lastError: z.string()
});
export type WallosSettings = z.infer<typeof wallosSettingsSchema>;
export type WallosSettingsInput = Omit<WallosSettings, "hasApiKey" | "lastSync" | "lastError"> & { apiKey: string };
export const wallosSyncSchema = z.object({ created: z.number() });
