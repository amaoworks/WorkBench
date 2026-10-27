import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { Link, NavLink, useLocation } from "react-router-dom";
import * as Dialog from "@radix-ui/react-dialog";
import { Aperture, Check, ChevronDown, Menu, Monitor, Moon, Paintbrush, Fingerprint, Sparkles, Sun, UserRound, X } from "lucide-react";
import { cn } from "../shared/cn";
import { aiShortcut } from "../shared/keyboard";
import { matchesLocation, type NavEntry } from "./navigation";
import type { ModuleMenuItem } from "../modules/registry";

type ThemeChoice = "light" | "dark" | "system";

export function TopNav({
  entries,
  onOpenAI,
  theme,
  onTheme,
  themeDisabled,
}: {
  entries: NavEntry[];
  onOpenAI: () => void;
  theme: ThemeChoice;
  onTheme: (theme: ThemeChoice) => void;
  themeDisabled: boolean;
}) {
  return (
    <header className="workspace-topbar">
      <div className="workspace-topbar-inner">
        <MobileNav entries={entries} />
        <Link to="/" className="workspace-logo focus-ring" aria-label="Workbench 总览">
          <span className="brand-mark">
            <Aperture size={21} strokeWidth={1.6} />
          </span>
          <span className="workspace-logo-text">Workbench</span>
        </Link>
        <nav className="workspace-nav" aria-label="主导航">
          <ul>
            {entries.map((entry) => (
              <li key={entry.key}>
                <NavDropdown entry={entry} />
              </li>
            ))}
          </ul>
        </nav>
        <div className="workspace-topbar-actions">
          <button
            type="button"
            className="workspace-ai-pill focus-ring"
            aria-label="打开 AI"
            onClick={onOpenAI}
            aria-keyshortcuts={aiShortcut.ariaKeyShortcuts}
          >
            <Sparkles size={16} />
            <span>问 AI</span>
            <kbd className="workspace-kbd">{aiShortcut.label}</kbd>
          </button>
          <UserMenu theme={theme} onTheme={onTheme} disabled={themeDisabled} />
        </div>
      </div>
    </header>
  );
}

function useDismiss(open: boolean, root: React.RefObject<HTMLElement | null>, close: () => void) {
  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) close();
    };
    document.addEventListener("pointerdown", onPointer);
    return () => document.removeEventListener("pointerdown", onPointer);
  }, [open, root, close]);
}

function focusItem(menu: HTMLElement | null, index: number | "first" | "last") {
  const items = Array.from(menu?.querySelectorAll<HTMLElement>("[role='menuitem'], [role='menuitemradio']") ?? []);
  if (items.length === 0) return;
  const current = items.indexOf(document.activeElement as HTMLElement);
  const next = index === "first" ? 0 : index === "last" ? items.length - 1 : (current + index + items.length) % items.length;
  items[next]?.focus();
}

function menuKeys(event: ReactKeyboardEvent, menu: HTMLElement | null, close: () => void) {
  if (event.key === "ArrowDown") focusItem(menu, 1);
  else if (event.key === "ArrowUp") focusItem(menu, -1);
  else if (event.key === "Home") focusItem(menu, "first");
  else if (event.key === "End") focusItem(menu, "last");
  else if (event.key === "Escape") close();
  else return;
  event.preventDefault();
  event.stopPropagation();
}

/** Top-level link with a hover/click/keyboard dropdown. Touch: the first tap opens the menu, the second follows the link. */
function NavDropdown({ entry }: { entry: NavEntry }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLAnchorElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const pointer = useRef("mouse");
  const leaveTimer = useRef<number | undefined>(undefined);
  const id = useId();
  const location = useLocation();
  const hasMenu = entry.menu.length > 0;
  const close = useCallback(() => setOpen(false), []);
  useDismiss(open, root, close);
  useEffect(() => () => window.clearTimeout(leaveTimer.current), []);

  function closeAndFocus() {
    setOpen(false);
    trigger.current?.focus();
  }
  return (
    <div
      ref={root}
      className="workspace-nav-item"
      onPointerEnter={(event) => {
        if (event.pointerType !== "mouse" || !hasMenu) return;
        window.clearTimeout(leaveTimer.current);
        setOpen(true);
      }}
      onPointerLeave={(event) => {
        if (event.pointerType !== "mouse") return;
        leaveTimer.current = window.setTimeout(() => setOpen(false), 140);
      }}
      onBlur={(event) => {
        if (!root.current?.contains(event.relatedTarget as Node | null)) setOpen(false);
      }}
    >
      <NavLink
        ref={trigger}
        to={entry.to}
        end={entry.to === "/"}
        className={({ isActive }) => cn("workspace-nav-link focus-ring", isActive && "is-active", open && "is-open")}
        aria-haspopup={hasMenu ? "menu" : undefined}
        aria-expanded={hasMenu ? open : undefined}
        aria-controls={hasMenu && open ? id : undefined}
        aria-label={entry.badge ? `${entry.label}，${entry.badge} 条未读` : undefined}
        onPointerDown={(event) => {
          pointer.current = event.pointerType;
        }}
        onClick={(event) => {
          if (hasMenu && (pointer.current === "touch" || pointer.current === "pen") && !open) {
            event.preventDefault();
            setOpen(true);
            return;
          }
          setOpen(false);
        }}
        onKeyDown={(event) => {
          pointer.current = "keyboard";
          if (!hasMenu) return;
          if (event.key === "ArrowDown" || ((event.key === " " || event.key === "Enter") && event.altKey)) {
            event.preventDefault();
            setOpen(true);
            requestAnimationFrame(() => focusItem(menu.current, "first"));
          } else if (event.key === "Escape" && open) {
            event.preventDefault();
            setOpen(false);
          }
        }}
      >
        <span>{entry.label}</span>
        {entry.badge ? <span className="nav-badge">{entry.badge > 99 ? "99+" : entry.badge}</span> : null}
        {hasMenu && <ChevronDown size={14} className="workspace-nav-caret" aria-hidden="true" />}
      </NavLink>
      {hasMenu && open && (
        <div
          ref={menu} id={id} role="menu" aria-label={entry.label} className="workspace-menu"
          onKeyDown={(event) => menuKeys(event, menu.current, closeAndFocus)}
        >
          {entry.menu.map((item) => (
            <MenuLink key={item.to} item={item} active={matchesLocation(item.to, location.pathname, location.search)} onSelect={() => setOpen(false)} />
          ))}
        </div>
      )}
    </div>
  );
}

function MenuLink({ item, active, onSelect }: { item: ModuleMenuItem; active: boolean; onSelect: () => void }) {
  const Icon = item.icon;
  return (
    <Link
      to={item.to} role="menuitem" tabIndex={-1} className={cn("workspace-menu-item focus-ring", active && "is-active")}
      aria-current={active ? "page" : undefined} onClick={onSelect}
    >
      {Icon && (
        <span className="workspace-menu-icon" aria-hidden="true">
          <Icon size={18} />
        </span>
      )}
      <span className="min-w-0">
        <span className="workspace-menu-label">{item.label}</span>
        {item.description && <span className="workspace-menu-desc">{item.description}</span>}
      </span>
    </Link>
  );
}

const themeChoices = [
  { id: "light", label: "浅色", icon: Sun },
  { id: "dark", label: "深色", icon: Moon },
  { id: "system", label: "跟随系统", icon: Monitor },
] as const;

function UserMenu({ theme, onTheme, disabled }: { theme: ThemeChoice; onTheme: (theme: ThemeChoice) => void; disabled: boolean }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const id = useId();
  const close = useCallback(() => setOpen(false), []);
  useDismiss(open, root, close);
  function closeAndFocus() {
    setOpen(false);
    button.current?.focus();
  }
  return (
    <div ref={root} className="workspace-user" onBlur={(event) => {
      if (!root.current?.contains(event.relatedTarget as Node | null)) setOpen(false);
    }}>
      <button
        ref={button}
        type="button"
        className="workspace-avatar focus-ring"
        aria-label="账户菜单"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => {
          setOpen((value) => !value);
          requestAnimationFrame(() => focusItem(menu.current, "first"));
        }}
        onKeyDown={(event) => {
          if (event.key !== "ArrowDown") return;
          event.preventDefault();
          setOpen(true);
          requestAnimationFrame(() => focusItem(menu.current, "first"));
        }}
      >
        <UserRound size={18} />
      </button>
      {open && (
        <div
          ref={menu} id={id} role="menu" aria-label="账户菜单" className="workspace-menu workspace-user-menu"
          onKeyDown={(event) => menuKeys(event, menu.current, closeAndFocus)}
        >
          <p className="workspace-menu-heading" id={`${id}-theme`}>主题</p>
          <div role="group" aria-labelledby={`${id}-theme`}>
            {themeChoices.map(({ id: value, label, icon: Icon }) => (
              <button
                key={value}
                type="button"
                role="menuitemradio"
                tabIndex={-1}
                aria-checked={theme === value}
                disabled={disabled}
                className="workspace-menu-item is-compact focus-ring"
                onClick={() => onTheme(value)}
              >
                <span className="workspace-menu-icon" aria-hidden="true">
                  <Icon size={17} />
                </span>
                <span className="workspace-menu-label flex-1">{label}</span>
                {theme === value && <Check size={16} className="text-brand" aria-hidden="true" />}
              </button>
            ))}
          </div>
          <div className="workspace-menu-sep" role="separator" />
          <MenuLink item={{ label: "外观", to: "/settings?tab=appearance", icon: Paintbrush }} active={false} onSelect={() => setOpen(false)} />
          <MenuLink item={{ label: "账户安全", to: "/settings?tab=security", icon: Fingerprint }} active={false} onSelect={() => setOpen(false)} />
        </div>
      )}
    </div>
  );
}

/** Narrow screens: a drawer listing every entry with its submenu expanded. */
function MobileNav({ entries }: { entries: NavEntry[] }) {
  const [open, setOpen] = useState(false);
  const location = useLocation();
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button type="button" className="workspace-burger focus-ring" aria-label="打开导航">
          <Menu size={20} />
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="ai-overlay z-40" />
        <Dialog.Content className="workspace-drawer z-50" aria-describedby={undefined}>
          <div className="workspace-drawer-head">
            <Dialog.Title className="font-semibold">Workbench</Dialog.Title>
            <Dialog.Close asChild>
              <button type="button" className="workspace-burger focus-ring" aria-label="关闭导航">
                <X size={20} />
              </button>
            </Dialog.Close>
          </div>
          <nav aria-label="主导航" className="workspace-drawer-body">
            {entries.map((entry) => (
              <section key={entry.key}>
                <NavLink
                  to={entry.to} end={entry.to === "/"}
                  className={({ isActive }) => cn("workspace-drawer-link focus-ring", isActive && "is-active")}
                  onClick={() => setOpen(false)}
                >
                  <entry.icon size={18} />
                  <span className="flex-1">{entry.label}</span>
                  {entry.badge ? <span className="nav-badge">{entry.badge}</span> : null}
                </NavLink>
                {entry.menu.length > 0 && (
                  <ul>
                    {entry.menu.map((item) => (
                      <li key={item.to}>
                        <Link
                          to={item.to}
                          className={cn("workspace-drawer-sublink focus-ring", matchesLocation(item.to, location.pathname, location.search) && "is-active")}
                          onClick={() => setOpen(false)}
                        >
                          {item.label}
                        </Link>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            ))}
          </nav>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
