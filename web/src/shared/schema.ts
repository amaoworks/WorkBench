import { z } from "zod";

export const navigationSchema = z.object({
  label: z.string(), route: z.string(), pageKey: z.string(), order: z.number()
});

const listedPageSchema = z.object({
  key: z.string(), label: z.string(), entry: z.string(), order: z.number()
});

const moduleBase = {
  id: z.string(), name: z.string(), version: z.string(), icon: z.string(),
  navigation: z.array(navigationSchema), enabled: z.boolean(),
  hasSettings: z.boolean().optional(),
  observedEnabled: z.boolean().nullable(),
  health: z.string().optional(),
  pending: z.boolean().optional(),
  lastError: z.string().optional(),
  lastCheckedAt: z.number().nullable().optional()
};

export const builtinModuleSchema = z.object({
  ...moduleBase,
  kind: z.literal("builtin"),
  contractVersion: z.literal(1)
});

export const externalModuleSchema = z.object({
  ...moduleBase,
  kind: z.literal("external"),
  protocolVersion: z.literal(1),
  pages: z.array(listedPageSchema).optional(),
  settingsEntry: z.string().optional(),
  baseUrl: z.string().optional(),
  allowNonLocal: z.boolean().optional(),
  hasServiceToken: z.boolean().optional(),
  connectionNote: z.string().optional(),
  generation: z.number().optional(),
  observedGeneration: z.number().optional()
});

export const moduleSchema = z.discriminatedUnion("kind", [builtinModuleSchema, externalModuleSchema]);
export const modulesResponseSchema = z.object({ items: z.array(moduleSchema) });
export type WorkbenchModule = z.infer<typeof moduleSchema>;
export type ExternalModule = z.infer<typeof externalModuleSchema>;

export const widgetSchema = z.object({
  id: z.string(), module: z.string(), schemaVersion: z.literal(1), title: z.string(), widgetKind: z.string(),
  dataRoute: z.string(), size: z.enum(["small", "medium", "large"]), order: z.number()
});
export const dashboardSchema = z.object({ widgets: z.array(widgetSchema) });
export type Widget = z.infer<typeof widgetSchema>;
export const configurableWidgetSchema = widgetSchema.extend({ visible: z.boolean(), enabled: z.boolean() });
export const widgetCatalogSchema = z.object({ widgets: z.array(configurableWidgetSchema) });
export type ConfigurableWidget = z.infer<typeof configurableWidgetSchema>;

export const notificationSchema = z.object({
  id: z.string(), sourceModule: z.string(), severity: z.enum(["info", "success", "warning", "error"]),
  title: z.string(), content: z.string(), actionLabel: z.string().optional(), actionRoute: z.string().optional(),
  sourceEventId: z.string().optional(), createdAt: z.string(), readAt: z.string().optional(),
  archivedAt: z.string().optional(), expiresAt: z.string().optional()
});
export type Notification = z.infer<typeof notificationSchema>;
export const notificationsPageSchema = z.object({ items: z.array(notificationSchema), nextCursor: z.string().optional() });

export const authStatusSchema = z.object({ authenticated: z.boolean(), mode: z.enum(["local", "password"]) });
export const unreadCountSchema = z.object({ count: z.number() });
export const aiStatusSchema = z.object({ available: z.boolean() });
export const apiErrorSchema = z.object({ code: z.string(), message: z.string() });

export const savedSchema = z.object({ saved: z.literal(true) });
