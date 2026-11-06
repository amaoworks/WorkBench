import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiValidated } from "../../shared/api";
import { savedSchema } from "../../shared/schema";

import {
  aiModelsSchema, aiTestSchema, appearanceSchema, createdUserSchema, managedUsersSchema, resetUserPasswordSchema, settingsSchema,
  telegramRecipientSchema, telegramSettingsSchema, telegramTestSchema, updatedUserSchema,
  type Appearance, type ManagedUser, type Settings,
} from "./schema";
export type { Appearance, AISettings, LoggingSettings, Settings } from "./schema";

export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => apiValidated("/api/settings", settingsSchema) });
}
export type AIConnectionInput = { baseUrl: string; apiKey: string; clearApiKey?: boolean };
export type AISettingsInput = AIConnectionInput & { enabled: boolean; model: string; models: string[] };
export function fetchAIModels(input: AIConnectionInput) {
  return apiValidated("/api/settings/ai/models", aiModelsSchema, { method: "POST", body: JSON.stringify(input) });
}
export function saveAISettings(input: AISettingsInput) {
  return apiValidated("/api/settings/ai", savedSchema, { method: "PUT", body: JSON.stringify(input) });
}
export function testAISettings(input: AISettingsInput) {
  return apiValidated("/api/settings/ai/test", aiTestSchema, { method: "POST", body: JSON.stringify(input) });
}
const usersKey = ["admin", "users"] as const;
export function useManagedUsers() {
  return useQuery({ queryKey: usersKey, queryFn: () => apiValidated("/api/admin/users", managedUsersSchema) });
}
export function useCreateUser() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: { username: string; displayName: string; role: ManagedUser["role"] }) =>
      apiValidated("/api/admin/users", createdUserSchema, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => client.invalidateQueries({ queryKey: usersKey }),
  });
}
export function useUpdateUser() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: Pick<ManagedUser, "id"> & Partial<Pick<ManagedUser, "displayName" | "role" | "status">>) =>
      apiValidated(`/api/admin/users/${encodeURIComponent(id)}`, updatedUserSchema, { method: "PATCH", body: JSON.stringify(input) }),
    onSuccess: async () => {
      window.dispatchEvent(new Event("workbench:auth-invalid"));
      await client.invalidateQueries({ queryKey: usersKey });
    },
  });
}
export function useResetUserPassword() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiValidated(`/api/admin/users/${encodeURIComponent(id)}/password`, resetUserPasswordSchema, {
      method: "POST", body: "{}",
    }),
    onSuccess: () => client.invalidateQueries({ queryKey: usersKey }),
  });
}

export function useTelegramSettings(enabled: boolean) {
  return useQuery({ queryKey: ["settings", "telegram", "bot"], enabled,
    queryFn: () => apiValidated("/api/settings/telegram", telegramSettingsSchema), refetchInterval: 30_000 });
}
export function useTelegramRecipient() {
  return useQuery({ queryKey: ["settings", "telegram", "recipient"],
    queryFn: () => apiValidated("/api/settings/telegram/recipient", telegramRecipientSchema), refetchInterval: 30_000 });
}
export function useTelegramRecipientMapping(userId: string) {
  return useQuery({ queryKey: ["settings", "telegram", "recipients", userId],
    queryFn: () => apiValidated(`/api/settings/telegram/recipients/${encodeURIComponent(userId)}`, telegramRecipientSchema) });
}
export function useSaveTelegramRecipient(userId?: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: { enabled: boolean; chatId?: string }) => apiValidated(
      userId ? `/api/settings/telegram/recipients/${encodeURIComponent(userId)}` : "/api/settings/telegram/recipient",
      savedSchema, { method: "PUT", body: JSON.stringify(input) },
    ),
    onSuccess: () => client.invalidateQueries({ queryKey: ["settings", "telegram"] }),
  });
}
export function useTestTelegramRecipient() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => apiValidated("/api/settings/telegram/recipient/test", telegramTestSchema, { method: "POST", body: "{}" }),
    onSettled: () => client.invalidateQueries({ queryKey: ["settings", "telegram", "recipient"] }),
  });
}
// Appearance applies optimistically so the theme flips on click; saves run one at a time and the
// last server answer wins. A failed save restores what was shown before that click.
export function useAppearance() {
  const client = useQueryClient();
  const apply = (appearance: Appearance) => client.setQueryData<Settings>(["settings"], (old) => old ? { ...old, appearance } : old);
  return useMutation({
    scope: { id: "appearance" },
    mutationFn: (value: Appearance) => apiValidated("/api/settings/appearance", appearanceSchema, { method: "PUT", body: JSON.stringify(value) }),
    onMutate: async (value) => {
      await client.cancelQueries({ queryKey: ["settings"], exact: true });
      const previous = client.getQueryData<Settings>(["settings"])?.appearance;
      apply(value);
      return { previous };
    },
    onError: (_error, _value, context) => {
      if (context?.previous) apply(context.previous);
    },
    onSuccess: (appearance) => apply(appearance)
  });
}
