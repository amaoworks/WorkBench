import { useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState } from "../../components/ui/EmptyState";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { deleteRule, saveRule, useRefreshNews, useRules, useSources } from "./queries";
import type { Rule, RuleInput } from "./schema";

const blank: RuleInput = { name: "", includeKeywords: [], excludeKeywords: [], sourceIds: [], topic: "", enabled: true };

export default function RulesTab() {
  const rules = useRules();
  const sources = useSources();
  const refresh = useRefreshNews();
  const [editing, setEditing] = useState<Rule | "new" | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const save = useMutation({
    mutationFn: saveRule,
    onSuccess: async () => {
      setEditing(null);
      await refresh();
      toast.success("关注规则已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  const remove = useMutation({
    mutationFn: deleteRule,
    onSuccess: async () => {
      await refresh();
      toast.success("关注规则已删除");
    },
    onError: (error) => toast.error(error.message),
  });
  const deletion = useConfirmDelete<Rule>({
    title: "删除关注规则",
    description: (target) => `确定删除“${target.name}”吗？`,
    onConfirm: (target) => remove.mutateAsync(target.id),
    fallbackFocus: () => addButton.current,
  });
  const items = rules.data?.items ?? [];

  return (
    <Card>
      <CardHeader
        title="关注规则"
        description={rules.data ? `${items.length} 条规则` : "正在读取"}
        action={
          <Button ref={addButton} size="sm" onClick={() => setEditing("new")}>
            <Plus size={15} />
            添加规则
          </Button>
        }
      />
      {editing && (
        <RuleForm
          key={editing === "new" ? "new" : editing.id}
          initial={editing === "new" ? blank : editing}
          id={editing === "new" ? undefined : editing.id}
          sources={sources.data?.items ?? []}
          busy={save.isPending}
          onCancel={() => setEditing(null)}
          onSave={(input) => save.mutate(input)}
        />
      )}
      {rules.isPending && (
        <div className="space-y-3 p-4">
          <Skeleton className="h-20" />
          <Skeleton className="h-20" />
        </div>
      )}
      {rules.isError && (
        <Notice className="m-4" tone="danger" title="关注规则读取失败">
          {rules.error.message}
        </Notice>
      )}
      {sources.isError && (
        <Notice className="m-4" tone="danger" title="订阅源读取失败">
          {sources.error.message}
        </Notice>
      )}
      {rules.data && items.length === 0 && !editing && <EmptyState title="暂无关注规则" description="按关键词、订阅源或主题筛选资讯。" />}
      <div className="divide-y divide-[var(--border)]">
        {items.map((rule) => (
          <div key={rule.id} className="flex min-w-0 flex-col gap-3 p-4 sm:flex-row sm:items-center">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-medium">{rule.name}</h3>
                {!rule.enabled && <Badge>已停用</Badge>}
              </div>
              <p className="mt-1 break-words text-xs text-[var(--muted)]">包含：{rule.includeKeywords.join("、") || "不限"}</p>
              {rule.excludeKeywords.length > 0 && (
                <p className="mt-1 break-words text-xs text-[var(--muted)]">排除：{rule.excludeKeywords.join("、")}</p>
              )}
              <p className="mt-1 text-xs text-[var(--muted)]">
                {rule.topic || "全部主题"} · {rule.sourceIds.length ? `${rule.sourceIds.length} 个订阅源` : "全部订阅源"}
              </p>
            </div>
            <div className="flex gap-1">
              <Button
                size="sm"
                variant="ghost"
                disabled={save.isPending}
                onClick={() => save.mutate({ ...pickInput(rule), enabled: !rule.enabled, id: rule.id })}
              >
                {rule.enabled ? "停用" : "启用"}
              </Button>
              <Button size="icon" variant="ghost" aria-label={`编辑 ${rule.name}`} onClick={() => setEditing(rule)}>
                <Pencil size={16} />
              </Button>
              <Button
                size="icon"
                variant="ghost"
                aria-label={`删除 ${rule.name}`}
                onClick={(event) => deletion.request(rule, event.currentTarget)}
              >
                <Trash2 size={16} />
              </Button>
            </div>
          </div>
        ))}
      </div>
      <ConfirmDialog {...deletion.dialogProps} />
    </Card>
  );
}

function RuleForm({
  initial,
  id,
  sources,
  busy,
  onCancel,
  onSave,
}: {
  initial: RuleInput;
  id?: string;
  sources: { id: string; name: string }[];
  busy: boolean;
  onCancel: () => void;
  onSave: (input: RuleInput & { id?: string }) => void;
}) {
  const [name, setName] = useState(initial.name);
  const [includes, setIncludes] = useState(initial.includeKeywords.join(", "));
  const [excludes, setExcludes] = useState(initial.excludeKeywords.join(", "));
  const [sourceIds, setSourceIds] = useState(initial.sourceIds);
  const [topic, setTopic] = useState(initial.topic);
  const [enabled, setEnabled] = useState(initial.enabled);
  function submit(event: FormEvent) {
    event.preventDefault();
    onSave({
      id,
      name: name.trim(),
      includeKeywords: splitKeywords(includes),
      excludeKeywords: splitKeywords(excludes),
      sourceIds,
      topic: topic.trim(),
      enabled,
    });
  }
  return (
    <form onSubmit={submit} className="m-4 space-y-3 rounded-lg border border-[var(--border)] p-4">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="名称">
          <input autoFocus required value={name} onChange={(event) => setName(event.target.value)} placeholder="规则名称" />
        </Field>
        <Field label="主题">
          <input value={topic} onChange={(event) => setTopic(event.target.value)} placeholder="全部主题" />
        </Field>
        <Field label="包含关键词">
          <textarea value={includes} onChange={(event) => setIncludes(event.target.value)} placeholder="AI, 芯片, 算力" rows={3} />
        </Field>
        <Field label="排除关键词">
          <textarea value={excludes} onChange={(event) => setExcludes(event.target.value)} placeholder="广告, 招聘" rows={3} />
        </Field>
      </div>
      <fieldset>
        <legend className="text-sm">订阅源</legend>
        <div className="mt-2 flex max-h-36 flex-wrap gap-x-4 gap-y-2 overflow-y-auto rounded-lg border border-[var(--border)] p-3">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={sourceIds.length === 0}
              onChange={(event) => {
                if (event.target.checked) setSourceIds([]);
              }}
            />
            全部订阅源
          </label>
          {sources.map((source) => (
            <label key={source.id} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={sourceIds.includes(source.id)}
                onChange={(event) =>
                  setSourceIds((current) =>
                    event.target.checked ? [...current, source.id] : current.filter((sourceId) => sourceId !== source.id),
                  )
                }
              />
              {source.name}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="flex items-center gap-2 text-sm">
        <Switch label="启用关注规则" checked={enabled} onChange={setEnabled} />
        启用
      </div>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="secondary" onClick={onCancel}>
          取消
        </Button>
        <Button disabled={busy}>{busy ? "保存中…" : "保存"}</Button>
      </div>
    </form>
  );
}

function splitKeywords(value: string) {
  return [
    ...new Set(
      value
        .split(/[，,\n]/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ];
}
function pickInput(rule: Rule): RuleInput {
  return {
    name: rule.name,
    includeKeywords: rule.includeKeywords,
    excludeKeywords: rule.excludeKeywords,
    sourceIds: rule.sourceIds,
    topic: rule.topic,
    enabled: rule.enabled,
  };
}
