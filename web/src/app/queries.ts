import { useQuery } from "@tanstack/react-query";
import { api } from "../shared/api";
import { modulesResponseSchema } from "../shared/schema";

export function useModules() {
  return useQuery({
    queryKey: ["modules"],
    queryFn: async () => modulesResponseSchema.parse(await api<unknown>("/api/modules"))
  });
}

