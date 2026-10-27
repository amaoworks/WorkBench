import { aiStatusSchema } from "../../shared/schema";
import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { MapPin, Send, Sparkles, SquarePen, X } from "lucide-react";
import { apiValidated, apiResponse } from "../../shared/api";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Notice } from "../../components/ui/Notice";
import { cn } from "../../shared/cn";
import { aiShortcut } from "../../shared/keyboard";
import * as Dialog from "@radix-ui/react-dialog";

type Message = { role: "user" | "assistant"; text: string };
type ChatResponse = { conversationId: string; messageId: string; text: string };
export type AIPageContext = { route: string; label: string };
type ChatInput = { message: string; context?: AIPageContext };

async function streamChat(
  conversationId: string,
  message: string,
  context: AIPageContext | undefined,
  onStarted: (conversationId: string) => void,
  onDelta: (text: string) => void,
): Promise<ChatResponse> {
  const response = await apiResponse("/api/chat/stream", {
    method: "POST",
    body: JSON.stringify({ conversationId, message, ...(context ? { context } : {}) }),
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
    const payload = JSON.parse(data) as Record<string, unknown>;
    if (event === "chat.started" && typeof payload.conversationId === "string") onStarted(payload.conversationId);
    if (event === "chat.delta" && typeof payload.text === "string") onDelta(payload.text);
    if (event === "chat.error") throw new Error(typeof payload.message === "string" ? payload.message : "AI 流式响应失败");
    if (event === "chat.completed") completed = payload as ChatResponse;
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
  const [input, setInput] = useState("");
  const [attachContext, setAttachContext] = useState(true);
  const [conversationId, setConversationId] = useState(() => localStorage.getItem("workbench-conversation-id") ?? "");
  const [messages, setMessages] = useState<Message[]>([]);
  const status = useQuery({ queryKey: ["ai", "status"], queryFn: () => apiValidated("/api/ai/status", aiStatusSchema), enabled: open });
  useEffect(() => {
    if (conversationId) localStorage.setItem("workbench-conversation-id", conversationId);
  }, [conversationId]);
  const chat = useMutation({
    mutationFn: ({ message, context: messageContext }: ChatInput) => {
      let receivedDelta = false;
      return streamChat(conversationId, message, messageContext, setConversationId, (delta) => {
        setMessages((current) =>
          current.map((item, index) => {
            if (index !== current.length - 1 || item.role !== "assistant") return item;
            return { ...item, text: receivedDelta ? item.text + delta : delta };
          }),
        );
        receivedDelta = true;
      });
    },
    onSuccess: (response) => {
      setConversationId(response.conversationId);
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
    if (!message || chat.isPending || status.data?.available === false) return;
    setMessages((current) => [...current, { role: "user", text: message }, { role: "assistant", text: "正在思考…" }]);
    setInput("");
    chat.mutate({ message, context: context && attachContext ? context : undefined });
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    handleSend();
  }
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
          <div className="flex h-16 items-center justify-between border-b border-[var(--border)] px-4">
            <Dialog.Title className="flex items-center gap-2 font-semibold">
              <Sparkles size={18} className="text-brand" />
              问 AI
              <kbd className="workspace-kbd">{aiShortcut.label}</kbd>
            </Dialog.Title>
            <Dialog.Description className="sr-only">与 AI 对话，协助安排工作和处理待办。</Dialog.Description>
            <div className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="icon"
                aria-label="新建对话"
                title="新建对话"
                disabled={chat.isPending || (messages.length === 0 && !conversationId)}
                onClick={() => {
                  setMessages([]);
                  setConversationId("");
                  localStorage.removeItem("workbench-conversation-id");
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
          <div className="flex-1 space-y-4 overflow-y-auto p-4" role="log" aria-live="polite" aria-relevant="additions text">
            {status.isError && (
              <Notice tone="danger" title="AI 状态加载失败">
                {status.error.message}
              </Notice>
            )}
            {status.data && !status.data.available && (
              <Notice
                title="AI 尚未启用"
                action={
                  <ButtonLink variant="secondary" size="sm" to="/settings?tab=ai" onClick={onClose}>
                    打开设置
                  </ButtonLink>
                }
              />
            )}
            {messages.length === 0 && (
              <div className="ai-suggestions">
                <p>试试这样问</p>
                {suggestions.map((text) => (
                  <button key={text} type="button" className="focus-ring" onClick={() => setInput(text)}>
                    {text}
                  </button>
                ))}
              </div>
            )}
            {messages.map((message, index) => (
              <div key={index} className={cn("ai-message", message.role === "user" && "user")}>
                {message.text}
              </div>
            ))}
          </div>
          {context && (
            <div className="ai-context">
              <button type="button" className="focus-ring" aria-pressed={attachContext} onClick={() => setAttachContext((value) => !value)}>
                <MapPin size={13} />
                {attachContext ? `附带当前页面：${context.label}` : "不附带当前页面"}
              </button>
            </div>
          )}
          <form onSubmit={submit} className="flex gap-2 border-t border-[var(--border)] p-4">
            <textarea
              aria-label="发送给 Workbench AI 的消息"
              rows={2}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  handleSend();
                }
              }}
              placeholder="输入消息…"
              className="ui-input min-h-11 flex-1 resize-none"
            />
            <Button size="icon" aria-label="发送消息" disabled={!input.trim() || chat.isPending || status.data?.available === false}>
              <Send size={17} />
            </Button>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
