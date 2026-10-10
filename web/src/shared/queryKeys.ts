import type { QueryClient } from "@tanstack/react-query";

export function moduleQueryKey(moduleId: string, ...parts: readonly unknown[]) {
  return [moduleId, ...parts] as const;
}

export async function clearModuleQueries(client: QueryClient, moduleId: string) {
  const queryKey = moduleQueryKey(moduleId);
  await client.cancelQueries({ queryKey });
  client.removeQueries({ queryKey });
}
