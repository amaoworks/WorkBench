import { createContext, useContext } from "react";
import type { AuthStatus } from "./schema";

export const AuthContext = createContext<AuthStatus | null>(null);

export function useAuth() {
  const auth = useContext(AuthContext);
  if (!auth) throw new Error("缺少登录身份");
  return auth;
}

export function useCurrentUser() {
  const user = useAuth().user;
  if (!user) throw new Error("缺少登录用户");
  return user;
}

export function useIsAdmin() {
  return useAuth().user?.role === "admin";
}

export function userStorageKey(userId: string, name: string) {
  return `workbench.user.${encodeURIComponent(userId)}:${name}`;
}
