import * as Dialog from "@radix-ui/react-dialog";
import { Search, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

type NavItem = { label: string; route: string; pageKey: string };

export function CommandPalette({ open, onOpenChange, navigation }: { open: boolean; onOpenChange: (open: boolean) => void; navigation: NavItem[] }) {
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const items = useMemo(() => [{ label: "总览", route: "/", pageKey: "dashboard" }, { label: "通知", route: "/notifications", pageKey: "notifications" }, { label: "设置", route: "/settings", pageKey: "settings" }, ...navigation].filter((item) => item.label.toLowerCase().includes(query.toLowerCase())), [navigation, query]);
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal><Dialog.Overlay className="command-overlay z-40" /><Dialog.Content className="command-content z-50">
        <Dialog.Title className="sr-only">搜索页面</Dialog.Title><Dialog.Description className="sr-only">输入名称并选择要打开的页面。</Dialog.Description><div className="flex items-center gap-3 border-b border-[var(--border)] px-4"><Search size={18} className="text-[var(--muted)]" /><input autoFocus aria-label="搜索页面名称" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入页面名称…" className="h-14 min-w-0 flex-1 bg-transparent outline-none" /><Dialog.Close aria-label="关闭搜索" className="focus-ring rounded p-1"><X size={17} /></Dialog.Close></div>
        <div className="max-h-80 overflow-y-auto p-2">{items.map((item) => <button key={item.pageKey} onClick={() => { navigate(item.route); onOpenChange(false); setQuery(""); }} className="command-item focus-ring flex w-full rounded-lg px-3 py-2.5 text-left">{item.label}</button>)}{items.length === 0 && <p className="p-6 text-center text-[var(--muted)]">没有匹配的页面</p>}</div>
      </Dialog.Content></Dialog.Portal>
    </Dialog.Root>
  );
}
