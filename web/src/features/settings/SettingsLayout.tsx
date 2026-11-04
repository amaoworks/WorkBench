import { useEffect, useRef, useState, type ComponentType, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { useIsFetching, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../../components/ui/PageHeader";
import { cn } from "../../shared/cn";

type Section = { id: string; title: string; icon: ComponentType<{ size?: number }> };
const sectionId = (id: string) => `settings-${id}`;
const SPY_OFFSET = 140;

function scrollToSection(id: string, smooth: boolean) {
  const reduced = document.documentElement.dataset.motion === "reduced" || window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  document.getElementById(sectionId(id))?.scrollIntoView({ block: "start", behavior: smooth && !reduced ? "smooth" : "auto" });
}

export function SettingsLayout({ title, sections, children }: {
  title: string; sections: readonly [Section, ...Section[]]; children: ReactNode;
}) {
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab");
  const selected = sections.find((section) => section.id === requested)?.id ?? sections[0].id;
  const [current, setCurrent] = useState(selected);
  const nav = useRef<HTMLElement>(null);
  const firstScroll = useRef(true);
  const positioned = useRef<string | null>(null);
  const client = useQueryClient();
  const fetching = useIsFetching();

  useEffect(() => {
    // User lists and service panels change section heights while their first queries load.
    // Position each deep link once after loading; polling must not pull the user back.
    if (client.isFetching() || positioned.current === selected) return;
    const frame = requestAnimationFrame(() => {
      scrollToSection(selected, !firstScroll.current);
      positioned.current = selected;
      firstScroll.current = false;
    });
    return () => cancelAnimationFrame(frame);
  }, [client, selected, fetching]);

  useEffect(() => {
    let frame = 0;
    const update = () => {
      frame = 0;
      let next = sections[0].id;
      for (const section of sections) {
        const element = document.getElementById(sectionId(section.id));
        if (element && element.getBoundingClientRect().top <= SPY_OFFSET) next = section.id;
      }
      if (window.scrollY > 0 && window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2) {
        next = sections[sections.length - 1]!.id;
      }
      setCurrent(next);
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(update);
    };
    update();
    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
    };
  }, [sections]);

  useEffect(() => {
    const strip = nav.current;
    const chip = strip?.querySelector<HTMLElement>(`[data-section="${current}"]`);
    if (strip && chip && strip.scrollWidth > strip.clientWidth) strip.scrollTo({ left: chip.offsetLeft - 16 });
  }, [current]);

  function select(id: string) {
    setCurrent(id);
    scrollToSection(id, true);
    setParams((value) => {
      const next = new URLSearchParams(value);
      next.set("tab", id);
      if (id !== "modules") next.delete("module");
      return next;
    }, { replace: true, preventScrollReset: true });
  }

  return (
    <div className="page settings-page">
      <PageHeader title={title} />
      <div className="settings-layout">
        <nav ref={nav} className="settings-nav" aria-label={`${title}分类`}>
          {sections.map(({ id, title: label, icon: Icon }) => (
            <a
              key={id} href={`?tab=${id}`} data-section={id}
              className={cn("settings-nav-link focus-ring", current === id && "is-active")}
              aria-current={current === id ? "true" : undefined}
              onClick={(event) => { event.preventDefault(); select(id); }}
            >
              <Icon size={18} /><span>{label}</span>
            </a>
          ))}
        </nav>
        <div className="settings-sections">{children}</div>
      </div>
    </div>
  );
}

export function SettingsSection({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section id={sectionId(id)} className="settings-section" aria-labelledby={`${sectionId(id)}-title`}>
      <h2 id={`${sectionId(id)}-title`}>{title}</h2>
      {children}
    </section>
  );
}

export function SettingsRow({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="settings-row">
      <div className="settings-row-label">{label}</div>
      <div className="settings-row-control">{children}</div>
    </div>
  );
}
