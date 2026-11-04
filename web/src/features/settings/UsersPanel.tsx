import { useRef, useState, type FormEvent } from "react";
import { UserPlus } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { useCurrentUser } from "../../shared/auth";
import { useCreateUser, useManagedUsers, useResetUserPassword, useUpdateUser } from "./queries";
import type { ManagedUser } from "./schema";
import { TelegramRecipientMapping } from "./TelegramRecipientPanel";

type PasswordResult = { username: string; password: string };

export function UsersPanel() {
  const users = useManagedUsers();
  const current = useCurrentUser();
  const [creating, setCreating] = useState(false);
  const [password, setPassword] = useState<PasswordResult | null>(null);
  const createButton = useRef<HTMLButtonElement>(null);
  if (users.isPending) return <Skeleton className="h-48" />;
  if (users.isError) return (
    <Notice tone="danger" title="用户加载失败" action={<Button variant="secondary" onClick={() => users.refetch()}>重试</Button>}>
      {users.error.message}
    </Notice>
  );
  const activeAdmins = users.data.items.filter((user) => user.role === "admin" && user.status === "active").length;
  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button ref={createButton} variant="secondary" onClick={() => setCreating((value) => !value)}>
          <UserPlus size={16} />创建用户
        </Button>
      </div>
      {creating && <CreateUserForm onCreated={(result) => {
        setPassword(result);
        setCreating(false);
        createButton.current?.focus();
      }} />}
      {password && (
        <Notice title={`${password.username} · 初始密码`}>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <input aria-label="初始密码" className="min-w-0 flex-1" value={password.password} readOnly autoComplete="off" />
            <Button variant="secondary" size="sm" onClick={() => setPassword(null)}>关闭</Button>
          </div>
        </Notice>
      )}
      <div className="divide-y divide-[var(--hair)]">
        {users.data.items.map((user) => (
          <UserEditor
            key={`${user.id}:${user.updatedAt}`}
            user={user}
            self={user.id === current.id}
            lastAdmin={user.role === "admin" && user.status === "active" && activeAdmins === 1}
            onPassword={setPassword}
          />
        ))}
      </div>
    </div>
  );
}

function RolePicker({ value, onChange, disabled = false }: {
  value: ManagedUser["role"]; onChange: (value: ManagedUser["role"]) => void; disabled?: boolean;
}) {
  return (
    <div className="settings-segment" role="group" aria-label="用户角色">
      {([{ id: "user", label: "普通用户" }, { id: "admin", label: "管理员" }] as const).map((role) => (
        <button
          key={role.id} type="button" className="focus-ring" aria-pressed={role.id === value}
          disabled={disabled} onClick={() => onChange(role.id)}
        >{role.label}</button>
      ))}
    </div>
  );
}

function CreateUserForm({ onCreated }: { onCreated: (result: PasswordResult) => void }) {
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [role, setRole] = useState<ManagedUser["role"]>("user");
  const create = useCreateUser();
  function submit(event: FormEvent) {
    event.preventDefault();
    create.mutate({ username: username.trim(), displayName: displayName.trim(), role }, {
      onSuccess: (result) => {
        onCreated({ username: result.user.username, password: result.initialPassword });
        toast.success("用户已创建");
      },
    });
  }
  return (
    <form className="studio-form space-y-4" onSubmit={submit}>
      <fieldset className="form-fields" disabled={create.isPending}>
        <div className="field-pair">
          <Field label="用户名">
            <input autoComplete="off" value={username} onChange={(event) => setUsername(event.target.value)} maxLength={64} required />
          </Field>
          <Field label="显示名称">
            <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={100} />
          </Field>
        </div>
        <RolePicker value={role} onChange={setRole} />
      </fieldset>
      {create.isError && <Notice tone="danger" title="创建失败">{create.error.message}</Notice>}
      <div className="form-actions">
        <Button disabled={!username.trim() || create.isPending}>{create.isPending ? "创建中…" : "创建用户"}</Button>
      </div>
    </form>
  );
}

function UserEditor({ user, self, lastAdmin, onPassword }: {
  user: ManagedUser; self: boolean; lastAdmin: boolean; onPassword: (result: PasswordResult) => void;
}) {
  const [displayName, setDisplayName] = useState(user.displayName);
  const [role, setRole] = useState(user.role);
  const [confirmation, setConfirmation] = useState<"disable" | "password" | null>(null);
  const [mappingOpen, setMappingOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement | null>(null);
  const update = useUpdateUser();
  const reset = useResetUserPassword();
  const busy = update.isPending || reset.isPending;
  const dirty = displayName !== user.displayName || role !== user.role;
  function changeStatus(status: ManagedUser["status"]) {
    update.mutate({ id: user.id, status }, {
      onSuccess: () => { setConfirmation(null); toast.success(status === "active" ? "用户已启用" : "用户已禁用"); },
      onError: (error) => toast.error(error.message),
    });
  }
  return (
    <div className="space-y-4 py-5">
    <form className="studio-form space-y-4" onSubmit={(event) => {
      event.preventDefault();
      update.mutate({ id: user.id, displayName, role }, {
        onSuccess: () => toast.success("用户已保存"), onError: (error) => toast.error(error.message),
      });
    }}>
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="font-medium">{user.username}</h3>
        {self && <Badge>本人</Badge>}
        <Badge tone={user.status === "active" ? "info" : "neutral"}>{user.status === "active" ? "已启用" : "已禁用"}</Badge>
        {user.mustChangePassword && <Badge tone="warning">待改密</Badge>}
      </div>
      <fieldset className="form-fields" disabled={busy}>
        <Field label="显示名称">
          <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} maxLength={100} />
        </Field>
        <RolePicker value={role} onChange={setRole} disabled={busy || lastAdmin} />
      </fieldset>
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="secondary" disabled={busy || user.status !== "active"} onClick={() => setMappingOpen((value) => !value)}>
          通知目标
        </Button>
        <Button type="button" variant="secondary" disabled={busy || self} onClick={(event) => {
          trigger.current = event.currentTarget;
          setConfirmation("password");
        }}>重置密码</Button>
        <Button
          type="button" variant="secondary" disabled={busy || lastAdmin}
          title={lastAdmin ? "至少保留一位有效管理员" : undefined}
          onClick={(event) => {
            trigger.current = event.currentTarget;
            if (user.status === "active") setConfirmation("disable");
            else changeStatus("active");
          }}
        >{user.status === "active" ? "禁用" : "启用"}</Button>
        <Button disabled={!dirty || busy}>{update.isPending ? "保存中…" : "保存"}</Button>
      </div>
      <ConfirmDialog
        open={confirmation !== null}
        title={confirmation === "password" ? `重置 ${user.username} 的密码？` : `禁用 ${user.username}？`}
        description={confirmation === "password" ? "现有登录将失效，下次登录需修改密码。" : "该用户的登录和个人任务将停止。"}
        confirmLabel={confirmation === "password" ? "重置密码" : "禁用用户"}
        busyLabel={confirmation === "password" ? "重置中…" : "禁用中…"}
        busy={busy}
        onCancel={() => setConfirmation(null)}
        onClosed={() => trigger.current?.focus()}
        onConfirm={() => {
          if (confirmation === "disable") changeStatus("disabled");
          else reset.mutate(user.id, {
            onSuccess: (result) => {
              setConfirmation(null);
              onPassword({ username: user.username, password: result.initialPassword });
              toast.success("密码已重置");
            },
            onError: (error) => toast.error(error.message),
          });
        }}
      />
    </form>
    {mappingOpen && user.status === "active" && <TelegramRecipientMapping userId={user.id} />}
    </div>
  );
}
