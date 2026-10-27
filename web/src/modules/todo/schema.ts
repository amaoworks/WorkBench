import { z } from "zod";

export const taskSchema = z.object({
  id: z.string(), title: z.string(), description: z.string(),
  dueAt: z.string().optional(), completedAt: z.string().optional(),
  priority: z.enum(["none", "low", "medium", "high"]).default("none"),
  category: z.string().max(40).optional(),
  createdAt: z.string(), updatedAt: z.string()
});
export type Task = z.infer<typeof taskSchema>;

export const tasksResponseSchema = z.object({ items: z.array(taskSchema), nextCursor: z.string().optional() });
export const summarySchema = z.object({ open: z.number(), overdue: z.number(), completed: z.number() });
export const taskCountsSchema = z.object({
  overdue: z.number().int().nonnegative(), today: z.number().int().nonnegative(), week: z.number().int().nonnegative(),
  later: z.number().int().nonnegative(), none: z.number().int().nonnegative(), done: z.number().int().nonnegative()
});
export type TaskPriority = Task["priority"];

export const wallosSettingsSchema = z.object({
  enabled: z.boolean(), baseUrl: z.string(), hasApiKey: z.boolean(),
  daysBefore: z.number(), reminderHour: z.number(), timeZone: z.string(),
  lastSync: z.string().optional(), lastError: z.string()
});
export type WallosSettings = z.infer<typeof wallosSettingsSchema>;
export type WallosSettingsInput = Omit<WallosSettings, "hasApiKey" | "lastSync" | "lastError"> & { apiKey: string };
export const wallosSyncSchema = z.object({ created: z.number() });

// Overview card: open tasks due today or overdue plus upcoming Wallos renewals.
export const todoTodaySchema = z.object({
  timeZone: z.string(), total: z.number(), overdue: z.number(), completedToday: z.number().int().nonnegative().optional(),
  items: z.array(taskSchema.extend({ overdue: z.boolean() })),
  renewals: z.array(z.object({ taskId: z.string(), name: z.string(), paymentDate: z.string(), amount: z.string().optional(), dueAt: z.string().optional() })).optional(),
  wallos: z.boolean()
});
export type TodoToday = z.infer<typeof todoTodaySchema>;
