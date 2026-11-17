const KEY = "workbench.password-notice";

export function rememberPasswordNotice(message: string) {
  sessionStorage.setItem(KEY, message);
}

export function takePasswordNotice(): string | null {
  const message = sessionStorage.getItem(KEY);
  if (message) sessionStorage.removeItem(KEY);
  return message;
}
