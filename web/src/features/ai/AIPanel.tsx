import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Bot, Send, X } from "lucide-react";
import { api, apiResponse } from "../../shared/api";
import { Button } from "../../components/ui/Button";
import { cn } from "../../shared/cn";
import { Link } from "react-router-dom";
import * as Dialog from "@radix-ui/react-dialog";

type Message = { role: "user" | "assistant"; text: string };
type ChatResponse = { conversationId: string; messageId: string; text: string };

async function streamChat(
  conversationId: string,
  message: string,
  onStarted: (conversationId: string) => void,
  onDelta: (text: string) => void
): Promise<ChatResponse> {
  const response = await apiResponse("/api/chat/stream", {
    method: "POST",
    body: JSON.stringify({ conversationId, message })
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

export function AIPanel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [input, setInput] = useState("");
  const [conversationId, setConversationId] = useState(() => localStorage.getItem("workbench-conversation-id") ?? "");
  const [messages, setMessages] = useState<Message[]>([]);
  const status = useQuery({ queryKey: ["ai", "status"], queryFn: () => api<{ available: boolean }>("/api/ai/status"), enabled: open });
  useEffect(() => {
    if (conversationId) localStorage.setItem("workbench-conversation-id", conversationId);
  }, [conversationId]);
  const chat = useMutation({
    mutationFn: (message: string) => {
      let receivedDelta = false;
      return streamChat(conversationId, message, setConversationId, (delta) => {
        setMessages((current) => current.map((item, index) => {
          if (index !== current.length - 1 || item.role !== "assistant") return item;
          return { ...item, text: receivedDelta ? item.text + delta : delta };
        }));
        receivedDelta = true;
      });
    },
    onSuccess: (response) => {
      setConversationId(response.conversationId);
      setMessages((current) => current.map((item, index) => index === current.length - 1 && item.role === "assistant" ? { ...item, text: response.text } : item));
    },
    onError: (error) => setMessages((current) => current.map((item, index) => {
      if (index !== current.length - 1 || item.role !== "assistant") return item;
      const prefix = item.text === "正在思考…" ? "" : `${item.text}\n\n`;
      return { ...item, text: `${prefix}请求失败：${error.message}` };
    }))
  });
  function submit(event: FormEvent) {
    event.preventDefault();
    const message = input.trim();
    if (!message || chat.isPending) return;
    setMessages((current) => [...current, { role: "user", text: message }, { role: "assistant", text: "正在思考…" }]);
    setInput("");
    chat.mutate(message);
  }
  return (
    <Dialog.Root open={open} onOpenChange={(value) => { if (!value) onClose(); }}><Dialog.Portal><Dialog.Overlay className="ai-overlay z-30" /><Dialog.Content className="ai-panel z-40">
      <div className="flex h-16 items-center justify-between border-b border-[var(--border)] px-4">
        <Dialog.Title className="flex items-center gap-2 font-semibold"><Bot size={19} className="text-brand" />Workbench AI</Dialog.Title>
        <Dialog.Description className="sr-only">与 AI 对话，协助安排工作和处理待办。</Dialog.Description>
        <Dialog.Close asChild><Button variant="ghost" size="icon" aria-label="关闭 AI 面板"><X size={18} /></Button></Dialog.Close>
      </div>
      <div className="flex-1 space-y-4 overflow-y-auto p-4" role="log" aria-live="polite" aria-relevant="additions text">
        {status.isError && <p className="text-sm text-danger" role="alert">无法获取 AI 状态，请稍后重试。</p>}
        {status.data && !status.data.available && <div className="rounded-lg border border-[var(--border)] bg-[var(--soft)] p-3 text-sm text-[var(--muted)]">AI 尚未启用。<Link to="/settings?tab=ai" onClick={onClose} className="text-link ml-1">前往设置连接服务</Link></div>}
        {messages.length === 0 && <div className="empty-state"><div className="icon-tile"><Bot size={22} /></div><h3>从一句话开始</h3><p>例如：“帮我创建明天下午完成报告的待办”</p></div>}
        {messages.map((message, index) => <div key={index} className={cn("ai-message", message.role === "user" && "user")}>{message.text}</div>)}
      </div>
      <form onSubmit={submit} className="flex gap-2 border-t border-[var(--border)] p-4">
        <textarea aria-label="发送给 Workbench AI 的消息" rows={2} value={input} onChange={(event) => setInput(event.target.value)} placeholder="输入消息…" className="ui-input min-h-11 flex-1 resize-none" />
        <Button size="icon" aria-label="发送消息" disabled={!input.trim() || chat.isPending || status.data?.available === false}><Send size={17} /></Button>
      </form>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
  );
}
