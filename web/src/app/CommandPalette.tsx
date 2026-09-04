import * as Dialog from "@radix-ui/react-dialog";
import { Search, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

type NavItem = { label: string; route: string; pageKey: string };

export function CommandPalette({ open, onOpenChange, navigation }: { open: boolean; onOpenChange: (open: boolean) => void; navigation: NavItem[] }) {
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const items = useMemo(() => [{ label: "总览", route: "/", pageKey: "dashboard" }, { label: "通知", route: "/notifications", pageKey: "notifications" }, ...navigation].filter((item) => item.label.toLowerCase().includes(query.toLowerCase())), [navigation, query]);
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-40 bg-slate-950/35 backdrop-blur-sm" /><Dialog.Content className="fixed left-1/2 top-[18%] z-50 w-[min(560px,calc(100%-2rem))] -translate-x-1/2 overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--surface)] shadow-2xl">
        <Dialog.Title className="sr-only">命令面板</Dialog.Title><div className="flex items-center gap-3 border-b border-[var(--border)] px-4"><Search size={18} className="text-[var(--muted)]" /><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入页面名称…" className="h-14 flex-1 bg-transparent outline-none" /><Dialog.Close className="focus-ring rounded p-1"><X size={17} /></Dialog.Close></div>
        <div className="max-h-80 p-2">{items.map((item) => <button key={item.pageKey} onClick={() => { navigate(item.route); onOpenChange(false); setQuery(""); }} className="focus-ring flex w-full rounded-lg px-3 py-2.5 text-left hover:bg-slate-100 dark:hover:bg-slate-800">{item.label}</button>)}{items.length === 0 && <p className="p-6 text-center text-[var(--muted)]">没有匹配的命令</p>}</div>
      </Dialog.Content></Dialog.Portal>
    </Dialog.Root>
  );
}

