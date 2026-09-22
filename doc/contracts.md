# 接口与契约

Go 契约的权威定义在 [internal/contracts](../internal/contracts)。HTTP 平台路由在 [app/app.go](../internal/app/app.go)，业务路由由各模块 `Register` 提供。本文列出当前接口入口和协作规则。

## 模块注册

[Module](../internal/contracts/module.go) 包含：

```go
Manifest() ModuleManifest
Migrations() MigrationSet
Register(ModuleRegistrar) error
```

Manifest 声明 ID、显示名称、业务版本、契约版本、图标和导航。当前契约版本为 1。`Migrations` 提供本模块的 `fs.FS` 和目录；`Register` 调用 `Handle`、`Consume`、`Job`、`Tool`、`Widget` 声明资源。

资源声明含模块归属，名称以模块 ID 为前缀，注册时检查有效性和重复项。App 负责构造模块与所需服务。内置注册表在构建程序时确定；外部模块通过 HTTP 协议在运行时接入，见下文。

可选契约：

- [ModuleLifecycle](../internal/contracts/lifecycle.go)：`OnEnabledChanged`、`Close`。无后台资源的模块可以不实现。Registry 在迁移后恢复每个模块的持久化意图，串行执行启停，并负责初始化失败及退出时的资源释放。回调应遵守 context，支持部分失败后重试同一意图；`Close` 必须能清理初始化未完成的资源。传入初始化函数后，资源所有权交给 Registry。
- [ModuleRouteProvider](../internal/contracts/lifecycle.go)：额外声明公共回调或受保护静态入口。公共回调必须显式标注，不会因为普通业务注册而匿名开放。

外部协议类型在 [external.go](../internal/contracts/external.go)，`protocolVersion` 当前为 1，与内置 `contractVersion` 分开。远端管理路径为 `/_workbench/manifest|status|state|config`。

## 事件、调度和通知

| 契约 | 当前语义 |
|---|---|
| [EventPublisher](../internal/contracts/event.go) | `Publish` 独立发布，`PublishTx` 加入调用方事务；事件含 topic、schemaVersion、sourceModule、aggregateId、payload |
| `EventConsumer` | ID、模块、主题集合、超时、最大尝试次数、Handler；消费者按至少一次交付设计 |
| [JobDefinition](../internal/contracts/job.go) | cron/interval/once、明确时区、超时、重试；OverlapPolicy 当前支持 skip，MisfirePolicy 支持 skip/run_once |
| [NotificationService](../internal/contracts/notification.go) | 创建、事务内创建、分页、标已读、全部已读、归档、未读数 |
| `NotificationChannel` | 外部投递接口，已装配 Telegram；首版转发投资价格预警 |

通知内容为纯文本，`actionRoute` 为站内路径，`actionLabel` 为跳转文案，幂等键由业务提供。SSE 发布 `core.notification.created`、`core.notification.updated` 和 `core.notification.archived`，浏览器收到后刷新查询。连接有心跳，不实现基于 Last-Event-ID 的历史补发。

Telegram 使用独立 dispatcher 和已有 `event_deliveries` 持久化投递。消费者可通过 `RetryAfterError` 请求更晚的重试时间，通过 `PermanentDeliveryError` 直接终止为 `dead`；详情及设置接口见 [Telegram 通知](notifications.md)。

## AI 和对话

[TextGenerator](../internal/contracts/text.go) 是业务主动调用的文本生成接口，参数为 profile、instruction、input。它不向 Provider 提供业务工具，返回不可用时使用 `ErrAIUnavailable`；不启用 AI 也能构造依赖该接口的业务。

[AIProvider 与 AITool](../internal/contracts/ai.go) 服务于平台 Gateway。Provider 提供普通与流式生成；Gateway 暴露当前启用模块的工具，并最多循环四轮模型请求。工具参数在服务端校验，执行审计持久化。

Tool 风险分为 read_only、low_write、high_write。写工具需要幂等键；high_write 还需要 `ConfirmedBy`。当前 Gateway 不提供用户确认交互，因此不能仅声明 high_write 就获得可用的确认流程。当前已注册业务工具是 `todo.create_task`。

对话请求包含 `conversationId`（可省略以新建）、`message` 和 `profile`（默认 default），响应包含对话 ID、消息 ID、文本和用量。流式 HTTP 事件为 `chat.started`、`chat.delta`、`chat.completed` 或 `chat.error`。对话数据由 conversation 能力保存。

## 页面和 Widget

[WidgetDefinition](../internal/contracts/widget.go) 声明 ID、模块、schemaVersion、标题、widgetKind、dataRoute、尺寸和默认顺序。尺寸为 small/medium/large。导航用 `pageKey` 关联页面。

前端 [ModuleUI](../web/src/modules/registry.ts) 提供 pages、widgets、可选 icons 和 settings。后端只能通过这些稳定标识选择本地编译的组件。Widget 接收 `{ widget }`，业务设置接收 `{ enabled }`。

Dashboard 保存完整的 Widget 配置集合，包含 ID、visible、size、order；目录查询保留停用业务以便编辑偏好，展示查询只返回启用且可见的卡片。

## HTTP 入口

JSON 错误使用 `code`、`message`，并可含 `details`、`requestId`，定义见 [APIError](../internal/contracts/http.go)。共享 [httpapi](../internal/foundation/httpapi/json.go) 校验 JSON、限制请求体并拒绝未知字段。前端 [api](../web/src/shared/api.ts) 负责凭据、CSRF 和错误映射，未校验的 JSON 返回 `unknown`。消费响应字段时使用 `apiValidated(path, schema, init)` 或显式 `schema.parse`，响应类型由 Zod 推导。关键 HTTP 契约通过真实后端响应与生产前端 schema 的跨端测试验证，见 [web/tests/contracts.test.mjs](../web/tests/contracts.test.mjs)。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/health/live`、`/health/ready` | 存活、数据库就绪 |
| GET | `/api/auth/status`、`/api/auth/csrf` | 登录状态、CSRF token |
| POST | `/api/auth/login`、`/api/auth/logout` | 登录、退出 |
| GET | `/api/modules` | 合并内置与外部目录（含 `kind`），只返回缓存状态 |
| POST | `/api/modules/external` | 校验连接并接入外部模块，成功 201 |
| PUT | `/api/modules/{id}/enabled` | 保存 `{ enabled }`；内置回调确认后 200，失败 503 并保留意图；外部确认后 200，等待确认时 202 |
| PUT | `/api/modules/{id}/connection` | 更新外部连接；要求已停用，更换 origin 须重新提供凭据 |
| POST | `/api/modules/{id}/refresh` | 有界刷新外部 manifest/状态 |
| GET | `/api/modules/{id}/status` | 已缓存状态 |
| GET / PUT | `/api/modules/{id}/config` | 外部业务配置，停用时仍可用 |
| DELETE | `/api/modules/external/{id}` | 解除外部接入（已停用且已确认，或不曾启用） |
| GET | `/modules/{id}/ui/*` | 外部业务页面代理，需已启用并确认 |
| GET / HEAD | `/modules/{id}/settings/*` | 外部设置页代理，停用时可用 |
| * | `/api/modules/{id}/proxy/*` | 外部业务 API/WebSocket 代理，需已启用并确认 |
| GET | `/api/dashboard`、`/api/dashboard/widgets` | 可见卡片、完整卡片目录 |
| PUT / DELETE | `/api/dashboard/layout` | 保存 `{ items }` / 恢复默认布局 |
| GET | `/api/notifications` | 支持 unread、limit、cursor 的列表 |
| GET | `/api/notifications/unread-count`、`/api/notifications/stream` | 未读数、SSE |
| PUT | `/api/notifications/read`、`/api/notifications/archive` | 提交 `{ ids }` |
| PUT | `/api/notifications/read-all` | 标记当前已有通知为已读 |
| GET | `/api/ai/status` | AI 是否可用 |
| POST | `/api/chat`、`/api/chat/stream` | 对话、流式对话 |
| GET | `/api/settings` | AI、外观、日志等级、部署信息，密钥不回显 |
| PUT | `/api/settings/ai`、`/api/settings/appearance` | 保存 AI 或外观 |
| PUT | `/api/settings/logging` | 保存 `{ "level": "info" }` 并立即生效；支持 debug/info/warn/error，返回规范化等级 |
| POST | `/api/settings/ai/test` | 测试候选 AI 配置，不保存 |
| GET / PUT | `/api/settings/telegram` | 读取脱敏配置和状态 / 保存 Telegram 配置 |
| POST | `/api/settings/telegram/test` | 发送一条测试消息，不保存候选配置 |
| PUT | `/api/settings/password` | 验证当前密码并修改 |
| POST | `/api/system/backup` | 创建服务端备份，返回文件名 |

除健康检查和鉴权入口外，上述平台 API 经过登录校验；local 模式免登录。Host、Origin 和 CSRF 保护由外层中间件处理，local 模式也保留请求保护。业务 API 另外经过模块启停门控，停用时返回 HTTP 503 和 `module_disabled`。外部业务代理还要求当前连接的当前代数已确认启用，且健康不是 `offline` 或 `incompatible`。

业务 HTTP、资源标识和字段说明见 [Todo](modules/todo.md)、[Wallos](modules/todo-wallos.md) 和 [Investment](modules/investment.md)。
