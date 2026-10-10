import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiValidated } from "../../shared/api";

import { appearanceSchema, settingsSchema, telegramSettingsSchema, type Appearance, type Settings } from "./schema";
export type { Appearance, AISettings, LoggingSettings, Settings } from "./schema";

export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => apiValidated("/api/settings", settingsSchema) });
}
export function useTelegramSettings() {
  return useQuery({ queryKey: ["settings", "telegram"], queryFn: () => apiValidated("/api/settings/telegram", telegramSettingsSchema), refetchInterval: 30_000 });
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
