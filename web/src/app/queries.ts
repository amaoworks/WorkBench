import { useQuery } from "@tanstack/react-query";
import { api } from "../shared/api";
import { modulesResponseSchema } from "../shared/schema";

export function useModules() {
  return useQuery({
    queryKey: ["modules"],
    queryFn: async () => modulesResponseSchema.parse(await api("/api/modules")),
    refetchInterval: (query) => {
      const items = query.state.data?.items ?? [];
      // Poll fast while any enabled module is still coming up (pending, or a builtin not yet observed running).
      const starting = items.some(
        (item) => item.enabled && !item.lastError && (item.pending || (item.kind === "builtin" && item.observedEnabled !== true)),
      );
      return starting || query.state.status === "error" ? 2000 : 5000;
    }
  });
}

