import { infiniteQueryOptions, useInfiniteQuery, useQuery, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { apiResponse, apiValidated } from "../../shared/api";
import {
  aiStatusSchema, chatCompletedSchema, chatDeltaSchema, chatErrorSchema, chatStartedSchema, chatToolSchema,
  type ChatResponse, type ChatTool,
} from "../../shared/schema";
import { conversationMessagesPageSchema, conversationsPageSchema, type ConversationMessage, type ConversationMessagesPage } from "./schema";

export type AIPageContext = { route: string; label: string };
export async function streamChat(
  conversationId: string,
  message: string,
  context: AIPageContext | undefined,
  model: string | undefined,
  onStarted: (conversationId: string, messageId: string, assistantMessageId?: string) => void,
  onDelta: (text: string) => void,
  onTool: (tool: ChatTool) => void,
): Promise<ChatResponse> {
  const response = await apiResponse("/api/chat/stream", {
    method: "POST",
    body: JSON.stringify({
      conversationId, message, timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      ...(context ? { context } : {}),
      ...(model ? { model } : {}),
    }),
  });
  if (!response.body) throw new Error("浏览器不支持流式响应");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let completed: ChatResponse | undefined;
  const consume = (frame: string) => {
    let event = "";
    let data = "";
    for (const line of frame.split("\n")) {
      if (line.startsWith("event:")) event = line.slice(6).trim();
      if (line.startsWith("data:")) data += line.slice(5).trimStart();
    }
    if (!event || !data) return;
    const payload: unknown = JSON.parse(data);
    if (event === "chat.started") {
      const started = chatStartedSchema.parse(payload);
      onStarted(started.conversationId, started.messageId, started.assistantMessageId);
    }
    if (event === "chat.delta") onDelta(chatDeltaSchema.parse(payload).text);
    if (event === "chat.tool") onTool(chatToolSchema.parse(payload));
    if (event === "chat.error") throw new Error(chatErrorSchema.parse(payload).message);
    if (event === "chat.completed") completed = chatCompletedSchema.parse(payload);
  };
  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done }).replaceAll("\r\n", "\n");
    let boundary = buffer.indexOf("\n\n");
    while (boundary >= 0) {
      consume(buffer.slice(0, boundary));
      buffer = buffer.slice(boundary + 2);
      boundary = buffer.indexOf("\n\n");
    }
    if (done) break;
  }
  if (buffer.trim()) consume(buffer);
  if (!completed) throw new Error("AI 流式响应意外结束");
  return completed;
}

export function aiStatusQueryOptions() {
  return { queryKey: ["ai", "status"], queryFn: () => apiValidated("/api/ai/status", aiStatusSchema) };
}
export function useAIStatus(enabled: boolean) {
  return useQuery({ ...aiStatusQueryOptions(), enabled });
}

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

const messagesKey = (conversationId: string) => ["ai", "conversation", conversationId, "messages"];
type MessagesData = InfiniteData<ConversationMessagesPage, string>;

export function mergeConversationMessages(
  history: ConversationMessage[], local: ConversationMessage[], activeMessageIds: string[] = [],
): ConversationMessage[] {
  const activeIds = new Set(activeMessageIds);
  const localById = new Map(local.map((message) => [message.id, message]));
  const seen = new Set(history.map((message) => message.id));
  return [
    ...history.map((message) => {
      const localMessage = localById.get(message.id);
      if (!localMessage) return message;
      if (activeIds.has(message.id)) return { ...message, ...localMessage };
      return localMessage.tools && !message.tools ? { ...message, tools: localMessage.tools } : message;
    }),
    ...local.filter((message) => !seen.has(message.id)),
  ];
}

export function appendConversationMessages(client: QueryClient, conversationId: string, messages: ConversationMessage[]) {
  client.setQueryData<MessagesData>(messagesKey(conversationId), (current) => {
    const first = current?.pages[0];
    const page = {
      ...first,
      messages: mergeConversationMessages(first?.messages ?? [], messages, messages.map((message) => message.id)),
    };
    return current
      ? { ...current, pages: [page, ...current.pages.slice(1)] }
      : { pages: [page], pageParams: [""] };
  });
}

export function conversationMessagesQueryOptions(conversationId: string) {
  const queryKey = messagesKey(conversationId);
  return infiniteQueryOptions({
    queryKey,
    initialPageParam: "",
    staleTime: 0,
    refetchOnMount: "always",
    refetchOnWindowFocus: "always",
    queryFn: async ({ pageParam, client, signal }) => {
      const load = (cursor: string) => apiValidated(
        `/api/conversations/${encodeURIComponent(conversationId)}/messages?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
        conversationMessagesPageSchema,
        { signal },
      );
      let page = await load(pageParam);
      if (pageParam) return page;
      const cached = client.getQueryData<MessagesData>(queryKey)?.pages[0];
      if (!cached?.messages.length) return page;

      // Keep the loaded prefix and its pagination boundary when the latest window moves.
      const cachedIds = new Set(cached.messages.map((message) => message.id));
      let latest = page.messages;
      while (page.nextCursor && !latest.some((message) => cachedIds.has(message.id))) {
        page = await load(page.nextCursor);
        latest = [...page.messages, ...latest];
      }
      const latestIds = new Set(latest.map((message) => message.id));
      const cachedById = new Map(cached.messages.map((message) => [message.id, message]));
      return {
        ...cached,
        messages: [
          ...cached.messages.filter((message) => !latestIds.has(message.id)),
          ...latest.map((message) => ({ ...message, tools: cachedById.get(message.id)?.tools })),
        ],
      };
    },
    getNextPageParam: (page) => page.nextCursor || undefined,
    select: (data) => {
      const seen = new Set<string>();
      return [...data.pages].reverse().flatMap((page) => page.messages).filter((message) => {
        if (seen.has(message.id)) return false;
        seen.add(message.id);
        return true;
      });
    },
    retry: false,
  });
}

export function useConversationMessages(conversationId: string, enabled: boolean) {
  return useInfiniteQuery({ ...conversationMessagesQueryOptions(conversationId), enabled: enabled && Boolean(conversationId) });
}
