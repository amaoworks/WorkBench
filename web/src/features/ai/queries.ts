import { infiniteQueryOptions, useInfiniteQuery } from "@tanstack/react-query";
import { apiValidated } from "../../shared/api";
import { conversationMessagesPageSchema, conversationsPageSchema } from "./schema";

export function conversationsQueryOptions() {
  return infiniteQueryOptions({
    queryKey: ["ai", "conversations"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => apiValidated(
      `/api/conversations?limit=100${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`,
      conversationsPageSchema,
    ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    select: (data) => {
      const seen = new Set<string>();
      return data.pages.flatMap((page) => page.items).filter((item) => {
        if (seen.has(item.id)) return false;
        seen.add(item.id);
        return true;
      });
    },
  });
}

export function useConversations(enabled: boolean) {
  return useInfiniteQuery({ ...conversationsQueryOptions(), enabled });
}

export function conversationMessagesQueryOptions(conversationId: string) {
  return infiniteQueryOptions({
    queryKey: ["ai", "conversation", conversationId, "messages"],
    initialPageParam: "",
    queryFn: ({ pageParam }) => apiValidated(
      `/api/conversations/${encodeURIComponent(conversationId)}/messages?limit=50${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`,
      conversationMessagesPageSchema,
    ),
    getNextPageParam: (page) => page.nextCursor || undefined,
    select: (data) => [...data.pages].reverse().flatMap((page) => page.messages),
    retry: false,
  });
}

export function useConversationMessages(conversationId: string, enabled: boolean) {
  return useInfiniteQuery({ ...conversationMessagesQueryOptions(conversationId), enabled: enabled && Boolean(conversationId) });
}
