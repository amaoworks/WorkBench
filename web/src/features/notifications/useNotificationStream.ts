import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

export function useNotificationStream() {
  const queryClient = useQueryClient();
  useEffect(() => {
    const source = new EventSource("/api/notifications/stream", { withCredentials: true });
    const refresh = () => {
      void queryClient.invalidateQueries({ queryKey: ["notifications"] });
    };
    source.addEventListener("core.notification.created", refresh);
    source.addEventListener("core.notification.updated", refresh);
    source.addEventListener("core.notification.archived", refresh);
    source.onerror = () => refresh();
    return () => source.close();
  }, [queryClient]);
}

