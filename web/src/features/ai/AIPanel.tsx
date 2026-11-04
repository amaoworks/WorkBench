import {
  aiStatusSchema, chatCompletedSchema, chatDeltaSchema, chatErrorSchema, chatStartedSchema, chatToolSchema,
  type ChatResponse, type ChatTool,
} from "../../shared/schema";
import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MapPin, Send, Sparkles, SquarePen, X } from "lucide-react";
import { apiValidated, apiResponse } from "../../shared/api";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Select } from "../../components/ui/Select";
import { cn } from "../../shared/cn";
import { useCurrentUser, userStorageKey } from "../../shared/auth";
import { aiShortcut } from "../../shared/keyboard";
import { useConversationMessages, useConversations } from "./queries";
import * as Dialog from "@radix-ui/react-dialog";

type Message = { role: "user" | "assistant"; text: string; tools?: ChatTool[] };
export type AIPageContext = { route: string; label: string };
type ChatInput = { message: string; context?: AIPageContext };

async function streamChat(
  conversationId: string,
  message: string,
  context: AIPageContext | undefined,
  onStarted: (conversationId: string) => void,
  onDelta: (text: string) => void,
  onTool: (tool: ChatTool) => void,
): Promise<ChatResponse> {
  const response = await apiResponse("/api/chat/stream", {
    method: "POST",
    body: JSON.stringify({
      conversationId, message, timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      ...(context ? { context } : {}),
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
    if (event === "chat.started") onStarted(chatStartedSchema.parse(payload).conversationId);
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

const suggestions = ["帮我创建明天上午 10 点开周会的待办", "今天下午 5 点前提醒我提交报销", "你能帮我做哪些事？"];

export function AIPanel({ open, onClose, context }: { open: boolean; onClose: () => void; context?: AIPageContext }) {
  const user = useCurrentUser();
  const conversationKey = userStorageKey(user.id, "conversation-id");
  const [input, setInput] = useState("");
  const [attachContext, setAttachContext] = useState(true);
  const [conversationId, setConversationId] = useState(() => localStorage.getItem(conversationKey) ?? "");
  const [messages, setMessages] = useState<Message[]>([]);
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["ai", "status"], queryFn: () => apiValidated("/api/ai/status", aiStatusSchema), enabled: open });
  const conversations = useConversations(open);
  const history = useConversationMessages(conversationId, open && messages.length === 0);
  const historyMessages: Message[] = history.data?.map((message) => ({ role: message.role, text: message.content })) ?? [];
  const visibleMessages = [...historyMessages, ...messages];
  const historyReady = !conversationId || messages.length > 0 || (history.isSuccess && !history.isRefetching);
  useEffect(() => {
    if (conversationId) localStorage.setItem(conversationKey, conversationId);
  }, [conversationId, conversationKey]);
  const chat = useMutation({
    mutationFn: ({ message, context: messageContext }: ChatInput) => {
      let receivedDelta = false;
      return streamChat(
        conversationId,
        message,
        messageContext,
        setConversationId,
        (delta) => {
          setMessages((current) =>
            current.map((item, index) => {
              if (index !== current.length - 1 || item.role !== "assistant") return item;
              return { ...item, text: receivedDelta ? item.text + delta : delta };
            }),
          );
          receivedDelta = true;
        },
        (tool) => {
          setMessages((current) =>
            current.map((item, index) => {
              if (index !== current.length - 1 || item.role !== "assistant") return item;
              const tools = [...(item.tools ?? [])];
              const existing = tools.findIndex((candidate) => candidate.callId === tool.callId);
              if (existing >= 0) tools[existing] = tool;
              else tools.push(tool);
              return { ...item, tools };
            }),
          );
          if (tool.status === "succeeded" && tool.risk !== "read_only") {
            const moduleId = tool.name.split(".", 1)[0];
            void Promise.all([
              queryClient.invalidateQueries({ queryKey: [moduleId] }),
              queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
              queryClient.invalidateQueries({ queryKey: ["notifications"] }),
            ]);
          }
        },
      );
    },
    onSuccess: (response) => {
      setConversationId(response.conversationId);
      void queryClient.invalidateQueries({ queryKey: ["ai", "conversations"] });
      void queryClient.invalidateQueries({ queryKey: ["ai", "conversation", response.conversationId, "messages"] });
      setMessages((current) =>
        current.map((item, index) => (index === current.length - 1 && item.role === "assistant" ? { ...item, text: response.text } : item)),
      );
    },
    onError: (error) =>
      setMessages((current) =>
        current.map((item, index) => {
          if (index !== current.length - 1 || item.role !== "assistant") return item;
          const prefix = item.text === "正在思考…" ? "" : `${item.text}\n\n`;
          return { ...item, text: `${prefix}请求失败：${error.message}` };
        }),
      ),
  });
  function handleSend() {
    const message = input.trim();
    if (!message || chat.isPending || !historyReady || status.data?.available === false) return;
    setMessages((current) => [...current, { role: "user", text: message }, { role: "assistant", text: "正在思考…" }]);
    setInput("");
    chat.mutate({ message, context: context && attachContext ? context : undefined });
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    handleSend();
  }
  function selectConversation(nextConversationId: string) {
    if (chat.isPending || nextConversationId === conversationId) return;
    setMessages([]);
    setConversationId(nextConversationId);
    if (!nextConversationId) localStorage.removeItem(conversationKey);
  }
  const conversationOptions = [
    { value: "", label: "新对话" },
    ...(conversationId && !conversations.data?.some((item) => item.id === conversationId)
      ? [{ value: conversationId, label: "当前对话" }] : []),
    ...(conversations.data?.map((item) => ({ value: item.id, label: item.title })) ?? []),
  ];
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(value) => {
        if (!value) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="ai-overlay z-30" />
        <Dialog.Content className="ai-panel z-40">
          <div className="flex h-16 shrink-0 items-center justify-between gap-2 px-4">
            <Dialog.Title className="flex shrink-0 items-center gap-2 font-semibold">
              <Sparkles size={18} className="text-brand" />
              问 AI
              <kbd className="workspace-kbd hidden sm:inline-flex">{aiShortcut.label}</kbd>
            </Dialog.Title>
            <Dialog.Description className="sr-only">与 AI 对话，协助安排工作和处理待办。</Dialog.Description>
            <div className="flex shrink-0 items-center gap-1">
              <Button
                variant="ghost"
                size="icon"
                aria-label="新建对话"
                title="新建对话"
                disabled={chat.isPending || (visibleMessages.length === 0 && !conversationId)}
                onClick={() => {
                  selectConversation("");
                }}
              >
                <SquarePen size={18} />
              </Button>
              <Dialog.Close asChild>
                <Button variant="ghost" size="icon" aria-label="关闭 AI 面板">
                  <X size={18} />
                </Button>
              </Dialog.Close>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2 border-b border-[var(--border)] px-4 pb-3">
            <Select
              aria-label="选择对话"
              className="min-w-0 flex-1"
              value={conversationId}
              options={conversationOptions}
              disabled={chat.isPending || conversations.isPending}
              onValueChange={selectConversation}
            />
            {conversations.hasNextPage && (
              <Button
                variant="secondary"
                size="sm"
                disabled={conversations.isFetching || chat.isPending}
                onClick={() => conversations.fetchNextPage()}
              >
                {conversations.isFetchingNextPage ? "加载中…" : "更多对话"}
              </Button>
            )}
          </div>
          <div className="flex-1 space-y-4 overflow-y-auto p-4" role="log" aria-live="polite" aria-relevant="additions text">
            {status.isError && (
              <Notice tone="danger" title="AI 状态加载失败">
                {status.error.message}
              </Notice>
            )}
            {conversations.isError && (
              <Notice
                tone="danger"
                title="对话列表加载失败"
                action={(
                  <Button variant="secondary" size="sm" onClick={() =>
                    conversations.isFetchNextPageError ? conversations.fetchNextPage() : conversations.refetch()
                  }>重试</Button>
                )}
              />
            )}
            {status.data && !status.data.available && (
              <Notice
                title="AI 尚未启用"
                action={user.role === "admin" ? (
                  <ButtonLink variant="secondary" size="sm" to="/settings?tab=ai" onClick={onClose}>
                    打开设置
                  </ButtonLink>
                ) : undefined}
              />
            )}
            {Boolean(conversationId) && !historyReady && history.isFetching && <Skeleton className="h-32" />}
            {history.isError && (
              <Notice
                tone="danger"
                title="对话记录加载失败"
                action={(
                  <Button variant="secondary" size="sm" onClick={() =>
                    history.isFetchNextPageError ? history.fetchNextPage() : history.refetch()
                  }>重试</Button>
                )}
              >
                可重试加载，或新建对话。
              </Notice>
            )}
            {history.hasNextPage && (
              <div className="flex justify-center">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={history.isFetching || chat.isPending}
                  onClick={() => history.fetchNextPage()}
                >
                  {history.isFetchingNextPage ? "加载中…" : "更早消息"}
                </Button>
              </div>
            )}
            {historyReady && visibleMessages.length === 0 && (
              <div className="ai-suggestions">
                <p>试试这样问</p>
                {suggestions.map((text) => (
                  <button key={text} type="button" className="focus-ring" onClick={() => setInput(text)}>
                    {text}
                  </button>
                ))}
              </div>
            )}
            {visibleMessages.map((message, index) => (
              <div key={index} className={cn("ai-message", message.role === "user" && "user")}>
                <div>{message.text}</div>
                {message.role === "assistant" && message.tools?.map((tool) => (
                  <div key={tool.callId} className="mt-2 flex flex-wrap items-center gap-x-2 text-xs text-[var(--muted)]">
                    <span>{tool.label}</span>
                    <span>{tool.status === "succeeded" ? "已完成" : "失败"}</span>
                    {tool.status === "failed" && tool.error && <span>{tool.error}</span>}
                  </div>
                ))}
              </div>
            ))}
          </div>
          {context && (
            <div className="ai-context">
              <button type="button" className="focus-ring" aria-pressed={attachContext} onClick={() => setAttachContext((value) => !value)}>
                <MapPin size={13} />
                {attachContext ? `页面位置：${context.label}` : "不附带页面位置"}
              </button>
            </div>
          )}
          <form onSubmit={submit} className="flex gap-2 border-t border-[var(--border)] p-4">
            <textarea
              aria-label="发送给 Workbench AI 的消息"
              rows={2}
              disabled={!historyReady}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  handleSend();
                }
              }}
              placeholder={historyReady ? "输入消息…" : "正在加载对话…"}
              className="ui-input min-h-11 flex-1 resize-none"
            />
            <Button size="icon" aria-label="发送消息" disabled={!input.trim() || chat.isPending || !historyReady || status.data?.available === false}>
              <Send size={17} />
            </Button>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
