/** http(s) only. Any other scheme, or an unparseable value, is not a navigable href. */
export function safeExternalURL(value: string): string | null {
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : null;
  } catch {
    return null;
  }
}
