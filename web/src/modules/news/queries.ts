import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, apiValidated } from "../../shared/api";
import { moduleQueryKey } from "../../shared/queryKeys";
import {
  articleSchema, articlesResponseSchema, digestSchema, digestsResponseSchema, refreshAllSchema, refreshSourceSchema,
  newsBriefSchema, ruleSchema, rulesResponseSchema, settingsSchema, sourceSchema, sourcesResponseSchema, statsSchema,
  type DigestInput, type NewsSettings, type RuleInput, type SourceInput
} from "./schema";

import type { Widget } from "../../shared/schema";
import { widgetSeed } from "../../shared/dashboard";

const BASE = "/api/modules/news";
const newsKey = (...parts: readonly unknown[]) => moduleQueryKey("news", ...parts);

export function useSources(enabled = true) {
  return useQuery({ queryKey: newsKey("sources"), enabled, queryFn: () => apiValidated(`${BASE}/sources`, sourcesResponseSchema), refetchInterval: 30_000 });
}
export function saveSource(input: SourceInput & { id?: string }) {
  const { id, ...body } = input;
  return apiValidated(`${BASE}/sources${id ? `/${encodeURIComponent(id)}` : ""}`, sourceSchema, { method: id ? "PUT" : "POST", body: JSON.stringify(body) });
}
export function deleteSource(id: string) { return api(`${BASE}/sources/${encodeURIComponent(id)}`, { method: "DELETE" }); }
export function refreshSource(id: string) { return apiValidated(`${BASE}/sources/${encodeURIComponent(id)}/refresh`, refreshSourceSchema, { method: "POST", body: "{}" }); }
export function refreshAllSources() { return apiValidated(`${BASE}/refresh`, refreshAllSchema, { method: "POST", body: "{}" }); }

export function useRules(enabled = true) {
  return useQuery({ queryKey: newsKey("rules"), enabled, queryFn: () => apiValidated(`${BASE}/rules`, rulesResponseSchema) });
}
export function saveRule(input: RuleInput & { id?: string }) {
  const { id, ...body } = input;
  return apiValidated(`${BASE}/rules${id ? `/${encodeURIComponent(id)}` : ""}`, ruleSchema, { method: id ? "PUT" : "POST", body: JSON.stringify(body) });
}
export function deleteRule(id: string) { return api(`${BASE}/rules/${encodeURIComponent(id)}`, { method: "DELETE" }); }

export type ArticleFilters = { q: string; sourceId: string; topic: string; unread: boolean; starred: boolean; matched: boolean };
export function useArticles(filters: ArticleFilters) {
  return useInfiniteQuery({
    queryKey: newsKey("articles", filters),
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ limit: "30" });
      if (filters.q) params.set("q", filters.q);
      if (filters.sourceId) params.set("sourceId", filters.sourceId);
      if (filters.topic) params.set("topic", filters.topic);
      if (filters.unread) params.set("unread", "true");
      if (filters.starred) params.set("starred", "true");
      if (filters.matched) params.set("matched", "true");
      if (pageParam) params.set("cursor", pageParam);
      return apiValidated(`${BASE}/articles?${params}`, articlesResponseSchema);
    },
    getNextPageParam: (page) => page.nextCursor || undefined,
    refetchInterval: 30_000
  });
}
export function useArticle(id: string | null) {
  return useQuery({
    queryKey: newsKey("article", id), enabled: Boolean(id),
    queryFn: () => apiValidated(`${BASE}/articles/${encodeURIComponent(id ?? "")}`, articleSchema),
    refetchInterval: (query) => query.state.data?.aiStatus === "running" ? 5_000 : false
  });
}
export function updateArticle(id: string, patch: { read?: boolean; starred?: boolean }) {
  return apiValidated(`${BASE}/articles/${encodeURIComponent(id)}`, articleSchema, { method: "PATCH", body: JSON.stringify(patch) });
}
export function analyzeArticle(id: string) { return apiValidated(`${BASE}/articles/${encodeURIComponent(id)}/analyze`, articleSchema, { method: "POST", body: "{}" }); }

export function useNewsSettings(enabled = true) {
  return useQuery({ queryKey: newsKey("settings"), enabled, queryFn: () => apiValidated(`${BASE}/settings`, settingsSchema) });
}
export function saveNewsSettings(input: NewsSettings) { return apiValidated(`${BASE}/settings`, settingsSchema, { method: "PUT", body: JSON.stringify(input) }); }
export function useNewsStats() { return useQuery({ queryKey: newsKey("stats"), queryFn: () => apiValidated(`${BASE}/stats`, statsSchema), refetchInterval: 30_000 }); }

export function useDigests() {
  return useInfiniteQuery({
    queryKey: newsKey("digests"), initialPageParam: "",
    queryFn: ({ pageParam }) => apiValidated(`${BASE}/digests?limit=20${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`, digestsResponseSchema),
    getNextPageParam: (page) => page.nextCursor || undefined,
    refetchInterval: 30_000
  });
}
export function createDigest(input: DigestInput) { return apiValidated(`${BASE}/digests`, digestSchema, { method: "POST", body: JSON.stringify(input) }); }
export function pushDigest(id: string) { return apiValidated(`${BASE}/digests/${encodeURIComponent(id)}/push`, digestSchema, { method: "POST", body: "{}" }); }

export function useRefreshNews() {
  const client = useQueryClient();
  return () => client.invalidateQueries({ queryKey: newsKey() });
}

/** Overview brief; held symbols (from the investment card) select holdings-related items. */
export function useNewsBrief(widget: Widget, symbols: readonly string[]) {
  const list = [...symbols].sort().slice(0, 20).join(",");
  return useQuery({
    queryKey: newsKey("widget", widget.id, list), ...(list ? {} : widgetSeed(widget, newsBriefSchema)),
    queryFn: () => apiValidated(`${widget.dataRoute}${list ? `?symbols=${encodeURIComponent(list)}` : ""}`, newsBriefSchema),
    placeholderData: (previous) => previous, refetchInterval: 120_000
  });
}
