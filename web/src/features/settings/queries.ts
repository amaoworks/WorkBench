import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../shared/api";

export type Appearance = { theme: "light" | "dark" | "system"; motion: "full" | "reduced" };
export type AISettings = { enabled: boolean; model: string; baseUrl: string; hasApiKey: boolean };
export type Settings = {
  ai: AISettings;
  appearance: Appearance;
  deployment: { listenAddress: string; dataPath: string; authMode: "local" | "password"; tls: boolean; allowedHosts: string[] | null };
};
export function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => api<Settings>("/api/settings") });
}
export function useAppearance() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (value: Appearance) => api<Appearance>("/api/settings/appearance", { method: "PUT", body: JSON.stringify(value) }),
    onSuccess: (appearance) => client.setQueryData<Settings>(["settings"], (old) => old ? { ...old, appearance } : old)
  });
}
