import * as Dialog from "@radix-ui/react-dialog";
import { useId, useState } from "react";
import { CalendarClock, ChevronLeft, ChevronRight } from "lucide-react";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Field } from "../../components/ui/Field";

const pad = (value: number) => String(value).padStart(2, "0");
const dateKey = (date: Date) => `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;

export function TodoDeadline({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState(() => dateKey(new Date()));
  const [month, setMonth] = useState(() => new Date(new Date().getFullYear(), new Date().getMonth(), 1));
  const [hour, setHour] = useState("09");
  const [minute, setMinute] = useState("00");
  const gridId = useId();
  const valueId = `${gridId}-value`;
  const count = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate();
  const start = (month.getDay() + 6) % 7;
  const label = value
    ? new Date(value).toLocaleString("zh-CN", {
        year: "numeric",
        month: "long",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
    : "截止时间";

  function changeOpen(next: boolean) {
    if (next) {
      const date = value ? new Date(value) : new Date();
      setSelected(dateKey(date));
      setMonth(new Date(date.getFullYear(), date.getMonth(), 1));
      setHour(value ? pad(date.getHours()) : "09");
      setMinute(value ? pad(date.getMinutes()) : "00");
    }
    setOpen(next);
  }

  function moveDay(day: number, offset: number) {
    const next = new Date(month.getFullYear(), month.getMonth(), day + offset);
    setMonth(new Date(next.getFullYear(), next.getMonth(), 1));
    setSelected(dateKey(next));
    requestAnimationFrame(() => document.getElementById(`${gridId}-${dateKey(next)}`)?.focus());
  }

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger asChild>
        <Button
          type="button"
          variant="secondary"
          className="task-date"
          aria-label="截止时间"
          aria-describedby={value ? valueId : undefined}
          lang="zh-CN"
        >
          <CalendarClock size={17} aria-hidden="true" />
          <Badge id={valueId}>{label}</Badge>
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="command-overlay z-40" />
        <Dialog.Content
          className="module-settings-dialog z-50 p-4"
          style={{ maxWidth: 340 }}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            document.getElementById(`${gridId}-${selected}`)?.focus();
          }}
        >
          <Dialog.Title className="font-semibold">截止时间</Dialog.Title>
          <Dialog.Description className="sr-only">选择日期与时间</Dialog.Description>
          <div className="my-3 flex items-center justify-between">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="上个月"
              onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}
            >
              <ChevronLeft size={16} />
            </Button>
            <span aria-live="polite">
              {month.getFullYear()} 年 {month.getMonth() + 1} 月
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="下个月"
              onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}
            >
              <ChevronRight size={16} />
            </Button>
          </div>
          <div className="grid grid-cols-7 gap-1 text-center">
            {["一", "二", "三", "四", "五", "六", "日"].map((day) => (
              <span key={day} className="text-xs text-[var(--muted)]">
                {day}
              </span>
            ))}
            {Array.from({ length: start }, (_, index) => (
              <span key={`blank-${index}`} />
            ))}
            {Array.from({ length: count }, (_, index) => {
              const day = index + 1;
              const key = dateKey(new Date(month.getFullYear(), month.getMonth(), day));
              return (
                <Button
                  key={key}
                  id={`${gridId}-${key}`}
                  type="button"
                  size="icon"
                  variant={selected === key ? "primary" : "ghost"}
                  aria-label={`${month.getFullYear()} 年 ${month.getMonth() + 1} 月 ${day} 日`}
                  aria-pressed={selected === key}
                  onClick={() => setSelected(key)}
                  onKeyDown={(event) => {
                    const offset = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[event.key];
                    if (offset === undefined) return;
                    event.preventDefault();
                    moveDay(day, offset);
                  }}
                >
                  {day}
                </Button>
              );
            })}
          </div>
          <div className="mt-4 grid grid-cols-2 gap-3">
            <Field label="小时">
              <select className="ui-input" value={hour} onChange={(event) => setHour(event.target.value)}>
                {Array.from({ length: 24 }, (_, index) => (
                  <option key={index} value={pad(index)}>
                    {pad(index)}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="分钟">
              <select className="ui-input" value={minute} onChange={(event) => setMinute(event.target.value)}>
                {Array.from({ length: 60 }, (_, index) => (
                  <option key={index} value={pad(index)}>
                    {pad(index)}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                onChange("");
                setOpen(false);
              }}
            >
              清除
            </Button>
            <Dialog.Close asChild>
              <Button type="button" variant="secondary">
                取消
              </Button>
            </Dialog.Close>
            <Button
              type="button"
              onClick={() => {
                onChange(`${selected}T${hour}:${minute}`);
                setOpen(false);
              }}
            >
              确定
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
