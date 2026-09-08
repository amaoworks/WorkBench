# 业务模块开发指南

本文描述当前可执行的接入流程。完整参考：后端 [Todo](../internal/modules/todo/module.go)、[Investment](../internal/modules/investment/module.go)，前端 [Todo 注册项](../web/src/modules/todo/todo.module.ts)、[Investment 注册项](../web/src/modules/investment/investment.module.ts)。功能状态见 [扩展验收记录](module-platform.md)。

## 1. 分工与依赖方向

业务负责自己的数据、用例、页面、Widget、领域事件与提醒条件；平台负责鉴权、数据库生命周期、可靠投递、调度、AI 服务配置、通知持久化以及 Shell 和总览布局。

| 业务需要什么 | 使用入口 | 业务向平台提供什么 |
|---|---|---|
| 数据持久化 | 注入 `*sql.DB`，自身 sqlc Queries | 模块 migration、表前缀与 SQL |
| 可靠事件 | `contracts.EventPublisher.PublishTx` | 版本化领域事件 |
| 接收领域事件 | `ModuleRegistrar.Consume` | `EventConsumer`，含 `Module`、`MaxAttempts`、幂等 Handler |
| 定时执行 | `ModuleRegistrar.Job` | `JobDefinition`，含超时、重叠、重试与 misfire 策略 |
| 生成 AI 文本 | 注入 `contracts.TextGenerator` | `TextRequest` 的指令和业务数据 |
| 让 AI 调用业务 | `ModuleRegistrar.Tool` | 参数 Schema、风险等级与业务 Handler |
| 站内推送 | `contracts.NotificationService.Create/CreateTx` | 纯文本、站内跳转、幂等键 |
| HTTP 接口 | `ModuleRegistrar.Handle` | 标准 `net/http.Handler` |
| 导航页面 | `Manifest.Navigation` + 前端 `pages` | `pageKey`、路由、懒加载组件 |
| 总览卡片 | `ModuleRegistrar.Widget` + 前端 `widgets` | 描述、数据端点、懒加载组件 |

业务之间不得直接 import，也不得查询或更新其他业务的表。跨业务协作通过事件或通用能力的公开契约进行。当前数据库连接共享，表隔离是架构约束，**不是数据库权限沙箱**。模块可使用 `foundation/httpapi` 和 `foundation/identity` 的公共工具，不得直接依赖 AI SDK、gocron 或其他业务实现。

`internal/modules/architecture_test.go` 检查业务间 import 和对能力实现的直接依赖；SQL 表归属仍需代码审查。不要把未经信任的第三方代码视为隔离插件。

## 2. 新业务的文件结构

以 `reading` 为例：

```text
internal/modules/reading/
  module.go                     # 构造、Manifest、Migrations、Register
  service.go                    # 业务用例（复杂后再拆分）
  migrations/00001_reading.sql
  query/books.sql
  sqlc/                         # sqlc 生成，不手改
  module_test.go
web/src/modules/reading/
  reading.module.ts             # 唯一前端注册入口
  ReadingPage.tsx
  ReadingWidget.tsx
  queries.ts                    # 业务 Zod schema、Query hooks
```

Module ID 使用小写 snake_case。表名使用 `reading_*`；事件使用 `reading.book.created`；Consumer、Job、Tool、Widget、pageKey、widgetKind 都使用 `reading.` 前缀。公开 API 使用 `/api/modules/reading/...`。

## 3. 声明后端模块，显式注入依赖

实现 `contracts.Module` 的三个方法：

```go
Manifest() contracts.ModuleManifest
Migrations() contracts.MigrationSet
Register(contracts.ModuleRegistrar) error
```

`Manifest` 的 `ContractVersion` 当前为 1，`Version` 是业务自己的版本。页面示例：

```go
Navigation: []contracts.NavigationItem{
    {Label: "阅读", Route: "/reading", PageKey: "reading.list", Order: 30},
},
```

像 Investment 一样用 `Dependencies` 声明所需能力，在构造函数拒绝缺失依赖。由 `internal/app/app.go` 创建能力实例、构造业务并加入 `modules.Initialize` 的清单。不要在业务内部创建另一个 SQLite、通知服务或 AI 客户端，也不使用全局 service locator。

新增业务需要修改应用装配，但不需要修改 Foundation 实现。业务代码及 UI 随构建进入程序；运行时开关只改变 enabled 状态，不加载未知二进制。

在 `Register` 内调用 `Handle/Consume/Job/Tool/Widget`，检查并返回所有注册错误。注册仅声明资源，不启动 goroutine、不执行外部请求、不写业务数据。资源会先暂存、校验，然后发布到注册目录；模块 migration 本身不与注册构成单一回滚事务，因此必须保持迁移可重复、向前兼容。

## 4. 数据库与事务事件

在模块目录嵌入 Goose migrations，用自己的前缀建表。应用统一负责打开数据库、执行 migration、备份和退出。禁用业务不回滚 migration、不删除表。

在 `sqlc.yaml` 加入本模块的 schema、query 和输出目录，运行：

```bash
sqlc generate
```

固定 SQL 必须进 `query/*.sql`，通过生成 Queries 使用。业务写入和可靠事件共享事务：

```go
tx, err := m.db.BeginTx(ctx, nil)
if err != nil { return err }
defer tx.Rollback()
// 使用 m.queries.WithTx(tx) 完成本业务写入。
_, err = m.events.PublishTx(ctx, tx, contracts.NewEvent{
    Topic: "reading.book.created", SchemaVersion: 1,
    SourceModule: "reading", AggregateID: bookID, Payload: payload,
})
if err != nil { return err }
return tx.Commit()
```

不要在数据库事务内等待 AI 或远程数据源。先执行外部读取，再开启短事务。事件是至少一次交付，Consumer 必须幂等。Investment 用 `(symbol, asOf)` 语义防止重复快照再次发布，用同一语义构造通知幂等键。

时间在数据库中保存 UTC Unix 毫秒，在 HTTP 中输出 UTC RFC3339；实体 ID 使用 `identity.New()`。

Todo 列表使用 `limit`（默认 50、最大 100）和 opaque `cursor`，返回 `items` 与可选 `nextCursor`。前端只能把服务端返回的游标原样传回。当前顺序为未完成优先、到期时间升序、创建时间降序、ID 升序。列表不是跨请求快照；任务完成状态或截止时间变更后应从第一页刷新。模块目录、Widget 目录和固定三品种行情属于有界配置/聚合响应，不采用分页。

## 5. AI：调用能力与提供工具是两个方向

业务主动生成文本，注入 `contracts.TextGenerator`：

```go
ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
defer cancel()
text, err := m.ai.GenerateText(ctx, contracts.TextRequest{
    Profile: "default",
    Instruction: "简要描述给定数据，只使用输入中的事实。",
    Input: string(dataJSON),
})
```

应用提供的 `ai.TextService` 每次请求读取当前 Provider，设置保存后新请求使用新配置；已经开始的请求使用原配置完成。AI 关闭时返回 `contracts.ErrAIUnavailable`。业务应明确返回 `ai_unavailable`，保持非 AI 功能可用。调用失败不要向浏览器暴露 Provider 原始错误或密钥。

该接口仅支持文本生成，不给模型传入业务 Tool，也不执行模型返回的工具调用。适合摘要、分类、解释等用例。业务不直接调用对话 `Gateway`，因为 Gateway 会装配当前所有已启用业务的工具。当前统一使用 `default` profile；新增 profile 要在应用的 Provider 配置处注册，不由业务硬编码供应商模型。

如果是让 AI 执行业务操作，则用 `r.Tool(contracts.AITool{...})`，参考 `todo.create_task`：声明严格 JSON Schema、`SchemaVersion` 和风险等级，写入使用幂等键，高风险工具由运行时验证确认。不要通过文本生成接口自行执行模型输出的命令。

Investment 的 `POST /api/modules/investment/summary` 是完整的主动调用示例：读模拟行情 → 调用共享 AI → 保存业务摘要 → 返回结果。用户主动点击生成才发送模拟数据，没有后台 AI 请求。AI 摘要存业务表，不作为对话记录或 Tool 审计。

## 6. 定时任务、消费事件与通知

周期工作必须 `r.Job(...)`，不要在业务里自行启动 ticker。Job 需稳定 ID、所属模块、时区、超时、重叠策略、离线错过执行策略和重试策略，Handler 尊重 context，操作可重试。

消费事件使用 `r.Consume(contracts.EventConsumer{ID, Module, Topics, Timeout, MaxAttempts, Handler})`。Consumer ID 发布后保持稳定；消费前检查 `SchemaVersion`。读取其他业务的事件 payload 不能变成对方表访问。

站内通知示例：

```go
_, err := m.notifications.Create(ctx, contracts.NewNotification{
    SourceModule: "reading", Severity: contracts.NotificationInfo,
    Title: "阅读提醒", Content: "有一项阅读计划到期。",
    ActionLabel: "查看阅读", ActionRoute: "/reading",
    IdempotencyKey: "reading:due:" + bookID + ":" + deadlineVersion,
})
```

需要与业务写入同时提交时使用 `CreateTx(ctx, tx, ...)`。通知服务自行持久化通知并发布通知事件；业务不用直接操作 SSE。SSE 是在线提示，重连后前端重新查询持久化状态。

当前推送范围是站内通知/SSE。`NotificationChannel` 是预留接口，尚未装配邮件、Web Push 或外部 IM；不能只实现 `Deliver` 就宣称渠道已接入。新渠道还需要独立的投递状态、重试、幂等与秘密配置设计。

## 7. 页面与 Widget 由业务提供

新建 `web/src/modules/reading/reading.module.ts`：

```ts
import { lazy } from "react";
import type { ModuleUI } from "../registry";

export default {
  id: "reading",
  pages: { "reading.list": lazy(() => import("./ReadingPage")) },
  widgets: { "reading.summary": lazy(() => import("./ReadingWidget")) }
} satisfies ModuleUI;
```

`modules/registry.ts` 通过 Vite glob 收集一级业务目录中的 `*.module.ts`，检查 key 前缀和重复项。注册描述随主包加载，页面和 Widget 通过 `React.lazy` 分包加载。新增模块不需要修改 `Layout.tsx` 或 `DashboardPage.tsx`。可选 `icons` 使用模块前缀命名，并与后端 Manifest.Icon 对应。

后端注册 Widget：

```go
r.Widget(contracts.WidgetDefinition{
    ID: "reading.summary", Module: "reading", SchemaVersion: 1,
    Title: "阅读概览", WidgetKind: "reading.summary",
    DataRoute: "/api/modules/reading/widget/summary",
    Size: contracts.WidgetSmall, Order: 30,
})
```

Widget 默认导出接收 `{widget: Widget}` 的 React 组件，通过 `widget.dataRoute` 请求业务数据。总览提供标题、网格、加载边界和单卡片错误边界；业务负责卡片内部内容与数据校验。未知 widgetKind 显示兼容性提示。

API 请求使用 `shared/api.ts`，写操作带 JSON body，该 client 自动处理 CSRF。响应通过业务 Zod schema 校验，状态用 TanStack Query。业务 queryKey 以模块 ID 开头，例如 `["reading", "books"]`；卡片用 `["widget", widget.id]`。业务写入后刷新相关业务与 Widget 查询。模块管理完成启停后刷新模块/总览/AI 清单，并清除对应业务与 Widget 缓存。

使用共享 PageHeader、Card、Button、ButtonLink 和语义颜色，提供加载、空态、失败态，尊重减少动态效果设置。列表分页使用 `useInfiniteQuery` 与“加载更多”，参考 Todo。新增 UI 必须重新构建前端及嵌入二进制。

## 8. 总览配置与业务启停

- 模块行的“设置”按钮打开紧凑弹窗，启用和停用状态均可打开。业务可以在 `ModuleUI` 注册可选的 `settings: lazy(() => import("./ReadingSettings"))`；组件默认导出并接收 `{ enabled: boolean }`。表单、校验与保存逻辑归业务所有，主设置页只提供弹窗容器。未注册设置组件时显示“此业务暂无可配置项”，不显示虚假的保存操作。待办已注册 Wallos 联动设置表单，详见 [Wallos 联动](todo-wallos.md)。
- 设置组件通过 `enabled` 区分模块状态；现有业务 HTTP 接口仍受启停 Gate 控制。后续若需停用期间保存配置，应先明确独立设置端点的访问规则，不能直接绕过 Gate。
- 设置 → 业务模块：启停业务；导航与总览过滤停用业务，后端 Gate 控制新请求、Consumer、Tool、Job。
- 总览 → 配置总览：独立显隐、上下移动、选择小/中/大尺寸，保存到工作空间数据库；移动端自适应单列。
- 隐藏卡片不会停用业务；停用业务不会清除该卡片的布局偏好。
- 用户偏好覆盖 Widget 默认 Size/Order。新增注册卡片没有偏好时默认显示，采用业务声明的尺寸和顺序。
- 已开始执行的 HTTP/Job/Consumer 不承诺被强制中断；禁用作用于后续入口。卸载代码和删除业务数据是独立操作。

接口细节见 [核心契约](mvp/contracts/core-contracts.md)。

## 9. 新增一种平台能力

1. 在 `internal/contracts` 声明最小业务接口和输入/输出，避免暴露供应商类型。
2. 在 `internal/capabilities/<name>` 实现，依赖 Foundation 公共能力。
3. 在 `internal/app` 管理实例、配置更新与生命周期，再注入需要它的业务。
4. 业务如需把资源注册给平台，显式扩展 Registrar 与目录校验，并定义 enabled Gate 语义；普通调用能力只需依赖注入，不必扩大 Registrar。
5. 提供不可用、超时、重试/幂等、设置更新的测试和文档。不要以通用 map 或全局容器绕过契约。

## 10. 开发与验收

```bash
SQLC_BIN=/path/to/sqlc ./scripts/test.sh
go test -race ./...
go vet ./...
git diff --check
```

测试脚本比较 sqlc 再生成前后的内容，覆盖 Foundation 和所有 `internal/modules/*/sqlc`，允许开发中的未提交改动。前端构建输出位于 `internal/webui/dist` 并随源码一并交付。

新增业务至少验证：事务失败回滚、事件重试幂等、禁用后入口关闭、重新启用及重启保留数据、AI 不可用时普通功能可用、旧数据库增加模块成功；新增卡片还需验证前端注册和布局保存。两个业务的隔离关系应由测试保护，而非只靠一个示例证明。

浏览器脚本 `scripts/module-platform.e2e.cjs` **仅对全新临时工作空间执行**，会创建 51 条待办并修改布局/模块状态。需本地 Playwright 与 Chromium，可通过 `PLAYWRIGHT_MODULE`、`CHROMIUM_PATH` 和 `WORKBENCH_TEST_URL` 指定环境。完整命令与验证记录见 [扩展验收记录](module-platform.md)。

### 通知跳转的统一呈现

通知生产者只提供 `ActionRoute` / `ActionLabel`，目标地址由通知服务校验，跳转和操作控件由通知中心统一渲染。查看入口使用共享 `ButtonLink`，已读/归档使用 `Button`，全部采用 `variant="ghost" size="sm"`。两类组件共用尺寸与交互样式；其他业务无需也不应为通知额外提供按钮 HTML 或 CSS。
