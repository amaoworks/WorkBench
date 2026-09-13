import { useQuery } from "@tanstack/react-query";
import { api } from "../shared/api";
import { modulesResponseSchema } from "../shared/schema";

export function useModules() {
  return useQuery({
    queryKey: ["modules"],
    queryFn: async () => modulesResponseSchema.parse(await api<unknown>("/api/modules")),
    refetchInterval: (query) => {
      const items = query.state.data?.items ?? [];
      return items.some((item) => item.kind === "external" && item.pending) ? 2000 : 5000;
    }
  });
}

