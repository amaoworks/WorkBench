import { useEffect, useState } from "react";

export type NotificationDayGroup = "今天" | "昨天" | "更早";

export function notificationDayGroup(iso: string, now: Date): NotificationDayGroup {
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const startOfYesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  const time = new Date(iso).getTime();
  if (time >= startOfToday.getTime()) return "今天";
  if (time >= startOfYesterday.getTime()) return "昨天";
  return "更早";
}

export function millisecondsUntilNextLocalDay(now: Date): number {
  return new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime() - now.getTime();
}

export function useLocalNow(): Date {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    let timer = window.setTimeout(refreshAtMidnight, millisecondsUntilNextLocalDay(new Date()) + 50);

    function refreshAtMidnight() {
      setNow(new Date());
      timer = window.setTimeout(refreshAtMidnight, millisecondsUntilNextLocalDay(new Date()) + 50);
    }

    const refreshIfVisible = () => {
      if (document.visibilityState === "visible") setNow(new Date());
    };
    window.addEventListener("focus", refreshIfVisible);
    document.addEventListener("visibilitychange", refreshIfVisible);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("focus", refreshIfVisible);
      document.removeEventListener("visibilitychange", refreshIfVisible);
    };
  }, []);

  return now;
}
