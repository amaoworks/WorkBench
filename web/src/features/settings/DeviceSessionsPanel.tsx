import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Monitor, Smartphone } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState } from "../../components/ui/EmptyState";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { apiValidated } from "../../shared/api";
import { formatDateTime, formatRelative } from "../../shared/format";
import {
  deviceSessionsSchema,
  logoutAllSchema,
  revokedSessionSchema,
  type DeviceSession,
} from "./schema";

type RevokeTarget = { kind: "session"; session: DeviceSession } | { kind: "all" };

const platformLabels: Record<DeviceSession["platform"], string> = {
  web: "网页",
  ios: "iOS",
  android: "Android",
  other: "其他",
};

export function DeviceSessionsPanel() {
  const client = useQueryClient();
  const heading = useRef<HTMLHeadingElement>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const [target, setTarget] = useState<RevokeTarget | null>(null);
  const sessions = useQuery({
    queryKey: ["auth", "sessions"],
    queryFn: () => apiValidated("/api/auth/sessions", deviceSessionsSchema),
    staleTime: 30_000,
  });
  const revoke = useMutation({
    mutationFn: async (value: RevokeTarget) => {
      if (value.kind === "all") {
        await apiValidated("/api/auth/logout-all", logoutAllSchema, { method: "POST", body: "{}" });
        return value;
      }
      await apiValidated(`/api/auth/sessions/${encodeURIComponent(value.session.id)}`, revokedSessionSchema, { method: "DELETE" });
      return value;
    },
    onSuccess: async (value) => {
      if (value.kind === "all" || value.session.current) {
        client.clear();
        window.location.assign("/");
        return;
      }
      setTarget(null);
      await client.invalidateQueries({ queryKey: ["auth", "sessions"] });
      toast.success("设备已注销");
    },
    onError: (error) => toast.error(error.message),
  });

  function request(next: RevokeTarget, element: HTMLButtonElement) {
    trigger.current = element;
    setTarget(next);
  }

  function close() {
    if (!revoke.isPending) setTarget(null);
  }

  const description =
    target?.kind === "all"
      ? "确定注销全部设备吗？"
      : target?.session.current
        ? "确定注销当前设备吗？"
        : `确定注销“${target?.session.deviceName ?? ""}”吗？`;

  return (
    <div className="studio-form">
      <div className="control-heading">
        <h3 ref={heading} tabIndex={-1}>登录设备</h3>
        <Button
          variant="danger"
          size="sm"
          disabled={sessions.isPending || sessions.isError || sessions.data.items.length === 0 || revoke.isPending}
          onClick={(event) => request({ kind: "all" }, event.currentTarget)}
        >
          全部注销
        </Button>
      </div>
      {sessions.isPending ? (
        <div className="space-y-3 py-4">
          <Skeleton className="h-14" />
          <Skeleton className="h-14" />
        </div>
      ) : sessions.isError ? (
        <Notice tone="danger" title="登录设备加载失败" action={<Button onClick={() => sessions.refetch()}>重试</Button>}>
          {sessions.error.message}
        </Notice>
      ) : sessions.data.items.length === 0 ? (
        <EmptyState title="暂无登录设备" />
      ) : (
        <div>
          {sessions.data.items.map((session) => (
            <div className="module-row" key={session.id}>
              <div className="flex min-w-0 items-center gap-3">
                <span className="icon-tile" aria-hidden="true">
                  {session.platform === "web" ? <Monitor size={18} /> : <Smartphone size={18} />}
                </span>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 className="break-words text-sm font-medium text-[var(--heading)]">{session.deviceName}</h3>
                    {session.current && <Badge tone="info">当前设备</Badge>}
                  </div>
                  <div className="mt-1 text-sm text-[var(--muted)]">
                    {platformLabels[session.platform]} · 最近活动{" "}
                    <time dateTime={session.lastSeenAt} title={formatDateTime(session.lastSeenAt)}>
                      {formatRelative(session.lastSeenAt)}
                    </time>
                  </div>
                </div>
              </div>
              <Button
                variant="danger"
                size="sm"
                disabled={revoke.isPending}
                onClick={(event) => request({ kind: "session", session }, event.currentTarget)}
              >
                注销
              </Button>
            </div>
          ))}
        </div>
      )}
      <ConfirmDialog
        open={target !== null}
        title={target?.kind === "all" ? "注销全部设备" : "注销设备"}
        description={description}
        busy={revoke.isPending}
        confirmLabel="确认注销"
        busyLabel="注销中…"
        onCancel={close}
        onConfirm={() => {
          if (target) revoke.mutate(target);
        }}
        onClosed={() => {
          const fallback = trigger.current?.isConnected ? trigger.current : heading.current;
          fallback?.focus();
        }}
      />
    </div>
  );
}
