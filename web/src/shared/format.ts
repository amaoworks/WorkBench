const pad = (value: number) => String(value).padStart(2, "0");

/** Local time as `YYYY-MM-DD HH:mm[:ss]` (24-hour), independent of the browser locale. */
export function formatDateTime(value: string | number | Date, { seconds = true }: { seconds?: boolean } = {}): string {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const day = `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
  const time = `${pad(date.getHours())}:${pad(date.getMinutes())}${seconds ? `:${pad(date.getSeconds())}` : ""}`;
  return `${day} ${time}`;
}
