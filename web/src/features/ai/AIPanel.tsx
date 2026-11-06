import type { ChatTool } from "../../shared/schema";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { MapPin, Send, Sparkles, SquarePen, X } from "lucide-react";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Select } from "../../components/ui/Select";
import { cn } from "../../shared/cn";
import { useCurrentUser, userStorageKey } from "../../shared/auth";
import { aiShortcut } from "../../shared/keyboard";
import { appendConversationMessages, mergeConversationMessages, streamChat, useAIStatus, useConversationMessages, useConversations } from "./queries";
import type { AIPageContext } from "./queries";
export type { AIPageContext } from "./queries";
import type { ConversationMessage } from "./schema";
import * as Dialog from "@radix-ui/react-dialog";

type Message = ConversationMessage & { active?: boolean };
type ChatInput = { message: string; context?: AIPageContext; model?: string };

const suggestions = ["帮我创建明天上午 10 点开周会的待办", "今天下午 5 点前提醒我提交报销", "你能帮我做哪些事？"];

export function AIPanel({ open, onClose, context }: { open: boolean; onClose: () => void; context?: AIPageContext }) {
  const user = useCurrentUser();
  const conversationKey = userStorageKey(user.id, "conversation-id");
  const [input, setInput] = useState("");
  const [attachContext, setAttachContext] = useState(true);
  const [selectedModel, setSelectedModel] = useState("");
  const [conversationId, setConversationId] = useState(() => localStorage.getItem(conversationKey) ?? "");
  const [messages, setMessages] = useState<Message[]>([]);
  const messageSequence = useRef(0);
  const queryClient = useQueryClient();
  const status = useAIStatus(open);
  const selectedModelRemoved = Boolean(selectedModel && status.data && !status.data.models.includes(selectedModel));
  const conversations = useConversations(open);
  useEffect(() => {
    if (conversationId) localStorage.setItem(conversationKey, conversationId);
  }, [conversationId, conversationKey]);
  const chat = useMutation({
    mutationFn: async ({ message, context: messageContext, model }: ChatInput) => {
      await queryClient.cancelQueries({ queryKey: ["ai", "conversation", conversationId, "messages"] });
      let receivedDelta = false;
      let userMessageId = "";
      const tools: ChatTool[] = [];
      const response = await streamChat(
        conversationId,
        message,
        messageContext,
        model,
        (id, messageId, assistantMessageId) => {
          userMessageId = messageId;
          setConversationId(id);
          setMessages((current) => current.map((item, index) => {
            if (index === current.length - 2) return { ...item, id: messageId };
            if (index === current.length - 1 && assistantMessageId) return { ...item, id: assistantMessageId };
            return item;
          }));
        },
        (delta) => {
          setMessages((current) =>
            current.map((item, index) => {
              if (index !== current.length - 1 || item.role !== "assistant") return item;
              return { ...item, content: receivedDelta ? item.content + delta : delta };
            }),
          );
          receivedDelta = true;
        },
        (tool) => {
          const previous = tools.findIndex((item) => item.callId === tool.callId);
          if (previous >= 0) tools[previous] = tool;
          else tools.push(tool);
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
      return { ...response, userMessageId, tools };
    },
    onSuccess: async (response, variables) => {
      setConversationId(response.conversationId);
      const queryKey = ["ai", "conversation", response.conversationId, "messages"];
      await queryClient.cancelQueries({ queryKey });
      appendConversationMessages(queryClient, response.conversationId, [
        { id: response.userMessageId, role: "user", content: variables.message },
        { id: response.messageId, role: "assistant", content: response.text, tools: response.tools },
      ]);
      setMessages((current) => current.slice(0, -2));
      void queryClient.invalidateQueries({ queryKey: ["ai", "conversations"] });
      void queryClient.invalidateQueries({ queryKey });
    },
    onError: (error) => {
      setMessages((current) =>
        current.map((item, index) => {
          if (index !== current.length - 1 || item.role !== "assistant") return { ...item, active: false };
          const prefix = item.content === "正在思考…" ? "" : `${item.content}\n\n`;
          return { ...item, active: false, content: `${prefix}请求失败：${error.message}` };
        }),
      );
      void queryClient.invalidateQueries({ queryKey: ["ai", "conversation"] });
      void queryClient.invalidateQueries({ queryKey: ["ai", "conversations"] });
    },
  });
  const history = useConversationMessages(conversationId, open);
  const activeMessageIds = messages.filter((message) => message.active).map((message) => message.id);
  const visibleMessages = mergeConversationMessages(history.data ?? [], messages, activeMessageIds);
  const historyReady = !conversationId || history.data !== undefined;
  function handleSend() {
    const message = input.trim();
    if (!message || chat.isPending || !historyReady || !status.data?.available || selectedModelRemoved) return;
    const localId = `local-${++messageSequence.current}`;
    setMessages((current) => [
      ...current,
      { id: `${localId}-user`, role: "user", content: message, active: true },
      { id: `${localId}-assistant`, role: "assistant", content: "正在思考…", active: true },
    ]);
    setInput("");
    chat.mutate({ message, context: context && attachContext ? context : undefined, model: selectedModel || undefined });
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    handleSend();
  }
  function selectConversation(nextConversationId: string) {
    if (chat.isPending || nextConversationId === conversationId) return;
    setMessages([]);
    setSelectedModel("");
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
          <div className="shrink-0 border-b border-[var(--border)] px-4 py-3">
            <Select
              aria-label="选择模型"
              className="min-w-0 w-full"
              value={selectedModel}
              onValueChange={setSelectedModel}
              disabled={chat.isPending || !status.data?.available}
              options={[
                { value: "", label: status.data?.defaultModel ? `默认 · ${status.data.defaultModel}` : "默认模型" },
                ...(status.data?.models.map((id) => ({ value: id, label: id })) ?? []),
                ...(selectedModelRemoved ? [{ value: selectedModel, label: `${selectedModel}（已移除）` }] : []),
              ]}
            />
          </div>
          <div className="flex-1 space-y-4 overflow-y-auto p-4" role="log" aria-live="polite" aria-relevant="additions text">
            {selectedModelRemoved && (
              <Notice tone="warning" title="所选模型已移除" action={
                <Button variant="secondary" size="sm" onClick={() => setSelectedModel("")} disabled={chat.isPending}>使用默认模型</Button>
              }>选择其他模型后继续。</Notice>
            )}
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
                  <ButtonLink variant="secondary" size="sm" to="/admin/settings?tab=ai" onClick={onClose}>
                    后台设置
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
            {visibleMessages.map((message) => (
              <div key={message.id} className={cn("ai-message", message.role === "user" && "user")}>
                <div>{message.content}</div>
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
            <Button size="icon" aria-label="发送消息" disabled={!input.trim() || chat.isPending || !historyReady || !status.data?.available || selectedModelRemoved}>
              <Send size={17} />
            </Button>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
