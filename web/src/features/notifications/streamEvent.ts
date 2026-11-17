export function notificationStreamEvent(event: "open" | "error"): { refresh: boolean } {
  return { refresh: event === "open" };
}
