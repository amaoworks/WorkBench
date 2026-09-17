import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiValidated } from "../../shared/api";

import { appearanceSchema, settingsSchema, type Appearance, type Settings } from "./schema";
export type { Appearance, AISettings, LoggingSettings, Settings } from "./schema";

export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => apiValidated("/api/settings", settingsSchema) });
}
export function useAppearance() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (value: Appearance) => apiValidated("/api/settings/appearance", appearanceSchema, { method: "PUT", body: JSON.stringify(value) }),
    onSuccess: (appearance) => client.setQueryData<Settings>(["settings"], (old) => old ? { ...old, appearance } : old)
  });
}
