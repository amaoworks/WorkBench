import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bell, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "../../components/ui/Badge";
import { Button, ButtonLink } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { ConfirmDialog } from "../../components/ui/ConfirmDialog";
import { EmptyState } from "../../components/ui/EmptyState";
import { Field } from "../../components/ui/Field";
import { Notice } from "../../components/ui/Notice";
import { Skeleton } from "../../components/ui/States";
import { Switch } from "../../components/ui/Switch";
import { useConfirmDelete } from "../../components/ui/useConfirmDelete";
import { useTelegramSettings } from "../../features/settings/queries";
import { deletePriceRule, savePriceRule, usePriceHistory, usePriceMonitor } from "./queries";
import type { PriceRule, PriceRuleInput } from "./schema";

const states = {
  idle: "尚未启用规则",
  waiting: "等待下一次检查",
  monitoring: "监控运行中",
  closed: "常规交易时段外",
  error: "监控异常",
  stale: "检查已中断",
};
const time = (value: string | null) => (value ? new Date(value).toLocaleString() : "尚未更新");
const percent = (value: number | null) => (value === null ? "—" : `${value > 0 ? "+" : ""}${value.toFixed(2)}%`);
const price = (value: number | null) =>
  value === null ? "—" : value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 4 });

export function InvestmentMonitor() {
  const monitor = usePriceMonitor();
  const history = usePriceHistory();
  const telegram = useTelegramSettings();
  const client = useQueryClient();
  const [editing, setEditing] = useState<PriceRule | "new" | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const refresh = () => client.invalidateQueries({ queryKey: ["investment"] });
  const toggle = useMutation({ mutationFn: savePriceRule, onSuccess: refresh, onError: (error) => toast.error(error.message) });
  const remove = useMutation({
    mutationFn: deletePriceRule,
    onSuccess: async () => {
      await refresh();
      toast.success("监控规则已删除");
    },
    onError: (error) => toast.error(error.message),
  });
  const deletion = useConfirmDelete<PriceRule>({
    title: "删除监控规则",
    description: (target) => `删除 ${target.symbol} 的这条规则？已有触发记录会保留。`,
    onConfirm: (target) => remove.mutateAsync(target.id),
    fallbackFocus: () => addButton.current,
  });
  const data = monitor.data;
  return (
    <div className="space-y-4" aria-label="价格监控">
      <Card className="p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            {monitor.isPending ? (
              <Skeleton className="h-6 w-44" />
            ) : (
              <h2 className="flex items-center gap-2 font-medium">
                <Bell size={18} />
                价格监控
                <Badge
                  tone={
                    data?.status === "error" || data?.status === "stale" ? "danger" : data?.status === "monitoring" ? "success" : "neutral"
                  }
                >
                  {data ? states[data.status] : "状态未知"}
                </Badge>
              </h2>
            )}
            <p className="mt-1 text-xs text-[var(--muted)]">常规交易时段每分钟检查 · 最近检查：{time(data?.checkedAt ?? null)}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              variant="secondary"
              size="sm"
              disabled={monitor.isFetching}
              onClick={() => {
                void refresh();
              }}
            >
              <RefreshCw size={14} />
              刷新状态
            </Button>
            <Button ref={addButton} size="sm" disabled={editing !== null} onClick={() => setEditing("new")}>
              <Plus size={14} />
              添加规则
            </Button>
          </div>
        </div>
        {(monitor.isError || data?.lastError) && (
          <Notice className="mt-3" tone="danger" title="监控状态异常">
            {monitor.error?.message || data?.lastError}
          </Notice>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
          {telegram.isPending ? (
            <Skeleton className="h-5 w-28" />
          ) : (
            <Badge tone={telegram.data?.enabled ? "success" : "neutral"}>
              {telegram.data?.enabled ? "TG 推送已启用" : "TG 推送未启用"}
            </Badge>
          )}
          <ButtonLink variant="ghost" size="sm" to="/settings?tab=notifications">
            配置 Telegram
          </ButtonLink>
        </div>
        {telegram.isError && (
          <Notice className="mt-2" tone="danger" title="TG 状态读取失败">
            {telegram.error.message}
          </Notice>
        )}
        {telegram.data?.enabled && telegram.data.status.lastError && (
          <Notice className="mt-2" tone="danger" title="TG 推送异常">
            {telegram.data.status.lastError}
          </Notice>
        )}
      </Card>
      {editing !== null && (
        <RuleForm
          key={typeof editing === "string" ? editing : editing.id}
          rule={editing === "new" ? undefined : editing}
          onClose={() => {
            setEditing(null);
            addButton.current?.focus();
          }}
          onSaved={refresh}
        />
      )}
      <Card>
        <CardHeader title="监控规则" description={`${data?.items.length ?? 0} / 100 条`} />
        {monitor.isPending && (
          <div className="space-y-3 p-5">
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
          </div>
        )}
        {data?.items.length === 0 && <EmptyState title="尚无监控规则" description="添加股票或 ETF，例如 AAPL、SPY。" />}
        <div className="divide-y divide-[var(--border)]">
          {data?.items.map((rule) => (
            <div key={rule.id} className="p-5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="font-medium">
                    {rule.symbol}{" "}
                    <span className="ml-2 text-sm font-normal">
                      {rule.direction === "up" ? "上涨" : "下跌"}达到 {rule.thresholdPercent}%
                    </span>
                  </h3>
                  <div className="mt-1">
                    <Badge tone={rule.enabled ? "success" : "neutral"}>{rule.enabled ? "已启用" : "已暂停"}</Badge>
                  </div>
                </div>
                <div className="flex flex-wrap gap-1">
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={toggle.isPending}
                    onClick={() =>
                      toggle.mutate({
                        id: rule.id,
                        symbol: rule.symbol,
                        direction: rule.direction,
                        thresholdPercent: rule.thresholdPercent,
                        enabled: !rule.enabled,
                      })
                    }
                  >
                    {rule.enabled ? "暂停" : "启用"}
                  </Button>
                  <Button size="sm" variant="ghost" disabled={editing !== null} onClick={() => setEditing(rule)}>
                    编辑
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`删除 ${rule.symbol}`}
                    disabled={editing !== null}
                    onClick={(event) => deletion.request(rule, event.currentTarget)}
                  >
                    删除
                  </Button>
                </div>
              </div>
              <dl className="mt-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
                <div>
                  <dt className="text-xs text-[var(--muted)]">最新价 USD</dt>
                  <dd className="mt-1 tabular-nums">{price(rule.price)}</dd>
                </div>
                <div>
                  <dt className="text-xs text-[var(--muted)]">涨跌幅</dt>
                  <dd className="mt-1 tabular-nums">{percent(rule.changePercent)}</dd>
                </div>
                <div>
                  <dt className="text-xs text-[var(--muted)]">前收盘价 USD</dt>
                  <dd className="mt-1 tabular-nums">{price(rule.previousClose)}</dd>
                </div>
              </dl>
              <p className="mt-3 text-xs text-[var(--muted)]">行情时间：{time(rule.quoteAt)}</p>
              {rule.enabled && rule.lastError && (
                <Notice className="mt-2" tone="danger" title="规则检查失败">
                  {rule.lastError}
                </Notice>
              )}
            </div>
          ))}
        </div>
      </Card>
      <Card>
        <CardHeader title="触发记录" description="保留触发时的价格快照" />
        {history.isPending && (
          <div className="space-y-3 p-5">
            <Skeleton className="h-20" />
            <Skeleton className="h-20" />
          </div>
        )}
        {history.isError && (
          <Notice className="m-5" tone="danger" title="触发记录读取失败">
            {history.error.message}
          </Notice>
        )}
        {history.data?.pages[0]?.items.length === 0 && <EmptyState title="暂无触发记录" description="达到规则阈值后会记录在这里。" />}
        <div className="divide-y divide-[var(--border)]">
          {history.data?.pages
            .flatMap((page) => page.items)
            .map((item) => (
              <div key={item.id} className="p-5">
                <p className="text-sm">
                  <strong>{item.symbol}</strong> {item.direction === "up" ? "上涨" : "下跌"}达到 {item.thresholdPercent}% · 实际{" "}
                  {percent(item.changePercent)} · {price(item.price)} USD
                </p>
                <p className="mt-2 text-xs text-[var(--muted)]">
                  行情：{time(item.quoteAt)} · 提醒：{time(item.triggeredAt)}
                </p>
              </div>
            ))}
        </div>
        {history.hasNextPage && (
          <div className="p-4">
            <Button
              variant="secondary"
              disabled={history.isFetchingNextPage}
              onClick={() => {
                void history.fetchNextPage();
              }}
            >
              加载更多记录
            </Button>
          </div>
        )}
      </Card>
      <ConfirmDialog {...deletion.dialogProps} />
    </div>
  );
}

function RuleForm({ rule, onClose, onSaved }: { rule?: PriceRule; onClose: () => void; onSaved: () => Promise<unknown> }) {
  const [symbol, setSymbol] = useState(rule?.symbol ?? "");
  const [direction, setDirection] = useState<PriceRuleInput["direction"]>(rule?.direction ?? "up");
  const [threshold, setThreshold] = useState(String(rule?.thresholdPercent ?? 3));
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const save = useMutation({
    mutationFn: () => savePriceRule({ id: rule?.id, symbol, direction, thresholdPercent: Number(threshold), enabled }),
    onSuccess: async () => {
      await onSaved();
      onClose();
      toast.success("监控规则已保存");
    },
    onError: (error) => toast.error(error.message),
  });
  return (
    <form
      className="studio-form"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <h3>{rule ? "编辑监控规则" : "添加监控规则"}</h3>
      <fieldset className="form-fields mt-4" disabled={save.isPending}>
        <Field label="标的代码">
          <input
            autoFocus
            value={symbol}
            maxLength={15}
            required
            placeholder="例如 AAPL 或 SPY"
            onChange={(event) => setSymbol(event.target.value.toUpperCase())}
          />
        </Field>
        <div className="field-pair">
          <div className="space-y-2">
            <span className="block text-sm">涨跌方向</span>
            <div className="flex gap-2" role="group" aria-label="涨跌方向">
              <Button
                type="button"
                variant={direction === "up" ? "primary" : "secondary"}
                aria-pressed={direction === "up"}
                onClick={() => setDirection("up")}
              >
                上涨达到
              </Button>
              <Button
                type="button"
                variant={direction === "down" ? "primary" : "secondary"}
                aria-pressed={direction === "down"}
                onClick={() => setDirection("down")}
              >
                下跌达到
              </Button>
            </div>
          </div>
          <Field label="阈值 %">
            <input
              type="number"
              min="0.01"
              max={direction === "down" ? 100 : 1000}
              step="0.01"
              required
              value={threshold}
              onChange={(event) => setThreshold(event.target.value)}
            />
          </Field>
        </div>
        <div className="flex items-center gap-2 text-sm">
          <Switch label="启用监控规则" checked={enabled} onChange={setEnabled} />
          启用
        </div>
      </fieldset>
      <div className="form-actions">
        <Button type="button" variant="secondary" disabled={save.isPending} onClick={onClose}>
          取消
        </Button>
        <Button disabled={save.isPending}>{save.isPending ? "保存中…" : "保存"}</Button>
      </div>
    </form>
  );
}
