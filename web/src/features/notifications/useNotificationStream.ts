import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useCurrentUser } from "../../shared/auth";

export function useNotificationStream() {
  const queryClient = useQueryClient();
  const user = useCurrentUser();
  useEffect(() => {
    const source = new EventSource(`/api/notifications/stream?workbenchUser=${encodeURIComponent(user.id)}`, { withCredentials: true });
    const refresh = () => {
      void queryClient.invalidateQueries({ queryKey: ["notifications"] });
    };
    source.addEventListener("core.notification.created", refresh);
    source.addEventListener("core.notification.updated", refresh);
    source.addEventListener("core.notification.archived", refresh);
    source.onerror = () => refresh();
    return () => source.close();
  }, [queryClient, user.id]);
}

