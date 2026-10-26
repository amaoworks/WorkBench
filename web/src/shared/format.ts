const pad = (value: number) => String(value).padStart(2, "0");

/** Local time as `YYYY-MM-DD HH:mm[:ss]` (24-hour), independent of the browser locale. */
export function formatDateTime(value: string | number | Date, { seconds = true }: { seconds?: boolean } = {}): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const time = `${pad(date.getHours())}:${pad(date.getMinutes())}${seconds ? `:${pad(date.getSeconds())}` : ""}`;
  return `${day} ${time}`;
}

const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 2 });
const usdWhole = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 0 });

/** `$1,234.56`; `whole` drops cents. */
export function formatUSD(value: number, { whole = false }: { whole?: boolean } = {}): string {
  return (whole ? usdWhole : usd).format(value);
}

/** Signed amount such as `+$12.30` / `-$4.00`. */
export function formatSignedUSD(value: number, options: { whole?: boolean } = {}): string {
  return `${value >= 0 ? "+" : "-"}${formatUSD(Math.abs(value), options)}`;
}

/** Signed percentage such as `+1.25%`. */
export function formatSignedPct(value: number, digits = 2): string {
  return `${value >= 0 ? "+" : ""}${value.toFixed(digits)}%`;
}

/** Short Chinese relative time: 刚刚 / 5 分钟前 / 3 小时前 / 昨天 / MM-DD. */
export function formatRelative(value: string | number | Date, now = Date.now()): string {
  const date = value instanceof Date ? value : new Date(value);
  const diff = now - date.getTime();
  if (Number.isNaN(diff)) return String(value);
  if (diff < 60_000) return "刚刚";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  if (diff < 2 * 86_400_000) return "昨天";
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

/** Local `HH:mm`. */
export function formatClock(value: string | number | Date): string {
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? "" : `${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
