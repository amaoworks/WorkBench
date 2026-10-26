import { z } from "zod";

export const sourceKindSchema = z.enum(["rss", "google", "yahoo", "rsshub"]);
export const sourceSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: sourceKindSchema,
  url: z.string(),
  query: z.string(),
  topic: z.string(),
  enabled: z.boolean(),
  intervalMinutes: z.number(),
  lastFetchedAt: z.string().optional(),
  nextFetchAt: z.string().optional(),
  lastError: z.string(),
  itemCount: z.number()
});
export type Source = z.infer<typeof sourceSchema>;
export type SourceInput = Omit<Source, "id" | "lastFetchedAt" | "nextFetchAt" | "lastError" | "itemCount">;
export const sourcesResponseSchema = z.object({ items: z.array(sourceSchema) });
export const refreshSourceSchema = z.object({ fetched: z.number(), added: z.number(), notModified: z.boolean() });
export const refreshAllSchema = z.object({ fetched: z.number(), added: z.number(), failed: z.number() });

export const ruleSchema = z.object({
  id: z.string(),
  name: z.string(),
  includeKeywords: z.array(z.string()),
  excludeKeywords: z.array(z.string()),
  sourceIds: z.array(z.string()),
  topic: z.string(),
  enabled: z.boolean()
});
export type Rule = z.infer<typeof ruleSchema>;
export type RuleInput = Omit<Rule, "id">;
export const rulesResponseSchema = z.object({ items: z.array(ruleSchema) });

export const articleSchema = z.object({
  id: z.string(),
  title: z.string(),
  url: z.string(),
  sourceName: z.string(),
  topic: z.string(),
  content: z.string(),
  language: z.string(),
  publishedAt: z.string(),
  createdAt: z.string(),
  read: z.boolean(),
  starred: z.boolean(),
  matched: z.boolean(),
  translatedTitle: z.string(),
  translatedContent: z.string(),
  summary: z.string(),
  aiStatus: z.enum(["none", "running", "ready", "error"]),
  aiError: z.string(),
  origins: z.array(z.object({ sourceId: z.string(), sourceName: z.string(), url: z.string() }))
});
export type Article = z.infer<typeof articleSchema>;
export const articlesResponseSchema = z.object({ items: z.array(articleSchema), nextCursor: z.string().optional() });

export const settingsSchema = z.object({
  autoAI: z.boolean(),
  targetLanguage: z.string(),
  dailyAILimit: z.number(),
  retentionDays: z.number(),
  digestEnabled: z.boolean(),
  digestHour: z.number(),
  digestMinute: z.number(),
  timeZone: z.string(),
  digestMaxItems: z.number(),
  digestTopic: z.string(),
  pushEnabled: z.boolean()
});
export type NewsSettings = z.infer<typeof settingsSchema>;

export const statsSchema = z.object({
  sources: z.number(), articles: z.number(), unread: z.number(), starred: z.number(),
  aiCallsToday: z.number(), aiLimit: z.number(), lastDigestAt: z.string().optional(), lastDigestError: z.string()
});

export const digestSchema = z.object({
  id: z.string(),
  title: z.string(),
  topic: z.string(),
  content: z.string(),
  items: z.array(z.object({ id: z.string(), title: z.string(), url: z.string(), sourceName: z.string() })),
  createdAt: z.string(),
  aiUsed: z.boolean(),
  notificationId: z.string().optional()
});
export type Digest = z.infer<typeof digestSchema>;
export const digestsResponseSchema = z.object({ items: z.array(digestSchema), nextCursor: z.string().optional() });
export type DigestInput = { topic: string; periodHours: number; maxItems: number; push: boolean };

// Overview card (GET /api/modules/news/brief).
const briefArticleSchema = z.object({
  id: z.string(), title: z.string(), url: z.string(), sourceName: z.string(), summary: z.string().optional(),
  publishedAt: z.string(), read: z.boolean(), matched: z.boolean(), symbols: z.array(z.string()).optional()
});
export const newsBriefSchema = z.object({
  sources: z.number(), unread: z.number(), digest: digestSchema.optional(),
  latest: z.array(briefArticleSchema), related: z.array(briefArticleSchema)
});
export type NewsBrief = z.infer<typeof newsBriefSchema>;
