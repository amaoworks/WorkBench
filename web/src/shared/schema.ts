import { z } from "zod";

export const navigationSchema = z.object({
  label: z.string(), route: z.string(), pageKey: z.string(), order: z.number()
});

export const moduleSchema = z.object({
  id: z.string(), name: z.string(), version: z.string(), contractVersion: z.literal(1),
  icon: z.string(), navigation: z.array(navigationSchema), enabled: z.boolean()
});

export const modulesResponseSchema = z.object({ items: z.array(moduleSchema) });
export type WorkbenchModule = z.infer<typeof moduleSchema>;

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
