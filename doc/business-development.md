# 业务模块开发指南

先阅读[架构与目录](architecture.md)，再按本次需求定位模块。现有模块是实现参考，按实际职责选择文件；不要求复制所有文件或预先创建空目录。

## 代码归属

一个业务对应 `internal/modules/<id>/` 和 `web/src/modules/<id>/`。模型、查询、迁移、业务用例、HTTP、定时任务、事件、工具、页面和模块设置都归该业务管理。第三方集成如果只服务一个业务，先留在该模块内，例如 Todo 的 Wallos 联动。

```text
internal/modules/reading/       示例：按需求添加的业务
  module.go                    构造、Manifest、Migrations、Register
  book.go                      模型与业务操作
  http.go                      HTTP 请求处理
  jobs.go                      有定时任务时添加
  events.go                    有事件消费时添加
  tools.go                     有 AI Tool 时添加
  migrations/00001_reading.sql
  query/books.sql
  sqlc/                        生成查询
  *_test.go                    对应行为的测试
web/src/modules/reading/
  reading.module.ts            前端注册入口
  ReadingPage.tsx
  ReadingWidget.tsx            提供总览卡片时添加
  ReadingSettings.tsx          提供业务设置时添加
  queries.ts                   接口函数、查询 hooks、缓存刷新
  schema.ts                    业务响应校验和类型
```

Go 同一模块先按文件分工，出现独立职责和实际复用需求后再拆子包。公共契约放 `internal/contracts`，前端业务类型放模块的 `schema.ts`；`web/src/shared/schema.ts` 用于模块目录、Widget、通知等平台共享数据。

## 后端接入

1. 选择稳定的模块 ID：小写字母开头，其余使用小写字母、数字和下划线，最多 63 字符。业务表使用 `<id>_` 前缀，资源标识使用 `<id>.` 前缀，HTTP 使用 `/api/modules/<id>/...`。
2. 实现 [contracts.Module](../internal/contracts/module.go)：`Manifest()`、`Migrations()`、`Register()`。当前 Registry 支持 `ContractVersion: 1`；业务版本独立维护。
3. 在构造函数显式接收所需依赖并检查必需项。Todo 使用位置参数，Investment 使用 `Dependencies`；根据依赖数量选择清楚的签名。
4. 在 [internal/app/app.go](../internal/app/app.go) 创建模块并加入 `modules.Initialize` 清单。模块注册阶段只声明资源，检查并返回每个注册错误，不执行远程请求或业务写入，也不启动后台循环。
5. 模块迁移通过本包 `go:embed migrations/*.sql` 提供，固定 SQL 放 `query/`，在 [sqlc.yaml](../sqlc.yaml) 增加 schema、query 和生成目标。运行 `sqlc generate` 并提交生成文件。

业务不直接 import 其他业务、`internal/app` 或 `internal/capabilities` 实现。使用 `contracts` 接口注入，底层 HTTP/ID 工具可使用 `foundation/httpapi` 和 `foundation/identity`。共享数据库连接用于本业务表；跨业务读取应先设计公开契约或事件。

## 使用通用能力

| 需求 | 接入方式 | 业务需要负责 |
|---|---|---|
| 保存数据 | 注入 `*sql.DB`，使用自身 sqlc Queries | 表和迁移、校验、事务 |
| 发布可靠事件 | `EventPublisher.PublishTx` | 主题、版本、载荷和聚合 ID |
| 消费事件 | `ModuleRegistrar.Consume` | 消费者 ID、主题、超时、重试次数、幂等 Handler |
| 定时任务 | `ModuleRegistrar.Job` | 时间规则、时区、超时、重试、misfire、Handler |
| 站内通知 | `NotificationService.Create/CreateTx` | 提醒条件、纯文本、站内跳转、幂等键 |
| 生成文本 | `TextGenerator.GenerateText` | 指令、必要的业务输入、不可用和失败反馈 |
| AI 调用业务 | `ModuleRegistrar.Tool` | 严格参数 schema、风险、Handler |
| HTTP 接口 | `ModuleRegistrar.Handle` | `net/http.Handler`、请求校验和响应 |
| 导航和卡片 | Manifest Navigation、`ModuleRegistrar.Widget` | 稳定标识、路由、数据接口和前端组件 |

业务写入与对应事件放在同一事务里，使用 `queries.WithTx(tx)` 和 `PublishTx(ctx, tx, event)`，成功后统一提交。远程读取、AI 请求放在写事务外。事件按至少一次交付设计，Job 也可能重试，幂等应使用业务语义，例如行情的 `(symbol, asOf)`、Wallos 的 `(source, subscriptionID, paymentDate)`。

新建提醒统一调用通知服务，不直接插入通知表。`actionRoute` 使用站内路径，组件统一由通知中心渲染。模块停用会阻止后续入口，隐藏卡片不会停止业务；功能需要自行关闭时可增加模块设置。

业务主动生成文本时处理 `contracts.ErrAIUnavailable`，保留非 AI 功能可用。AI Tool 的对象 schema 要求 `additionalProperties: false`，属性进入 `required`，可选值通过可空类型表达。写工具需要幂等键；高风险工具还需要运行时确认信息，当前聊天界面没有通用高风险确认流程。具体契约见[接口与契约](contracts.md)。

## 前端注册和查询

在 `<id>.module.ts` 导出满足 [ModuleUI](../web/src/modules/registry.ts) 的声明：

```ts
import { lazy } from "react";
import type { ModuleUI } from "../registry";

export default {
  id: "reading",
  pages: { "reading.list": lazy(() => import("./ReadingPage")) },
  widgets: { "reading.summary": lazy(() => import("./ReadingWidget")) }
} satisfies ModuleUI;
```

Registry 通过 `./*/*.module.ts` 收集声明，校验模块和资源标识。页面 key、Widget kind 必须与后端对应；组件通过 `React.lazy` 加载。提供模块设置时增加 `settings` 懒加载组件，接收 `{ enabled: boolean }`；关闭状态下暂停业务查询并提示启用模块。

接口函数与查询 hooks 放 `queries.ts`，响应的 Zod schema 和业务类型放 `schema.ts`。页面保留表单状态、交互反馈和渲染。参考 [Todo queries](../web/src/modules/todo/queries.ts) 与 [Investment queries](../web/src/modules/investment/queries.ts)。

业务查询 key 以模块 ID 开头，例如 `["todo", "tasks"]`；卡片使用 `["widget", widget.id]`。写入后刷新受影响的业务和卡片缓存，涉及总览时刷新 `["dashboard"]`。列表读取 `nextCursor`，沿用后端分页语义，不能只请求第一页后在客户端当作全量数据。

共享 `api` 客户端处理同源凭据、CSRF 和标准错误。复用 `components/ui` 中的页面标题、卡片、按钮和确认弹窗，配色使用 `styles.css` 语义变量，图标使用 Lucide；其余交互约定见 [web/README](../web/README.md)。

## 验证和文档维护

已有行为测试位于模块和平台对应包，后端架构测试覆盖模块 import 边界。修改 SQL 时检查迁移与生成查询，修改页面、缓存或模块设置时验证对应交互。完整检查入口：

```bash
./scripts/test.sh
```

该脚本检查 sqlc 生成漂移、Go 测试、前端 lint 和生产构建。浏览器回归方法见 [scripts/README](../scripts/README.md)，使用临时数据库运行。

新增模块后，在 [internal/modules/README](../internal/modules/README.md) 和 [文档入口](README.md) 登记，并在 `doc/modules/` 写当前功能、数据归属和接入点。调整公共能力、配置或数据库结构时同步修改对应设计说明。文档描述实现后的状态；未来需求以当次任务为准。
