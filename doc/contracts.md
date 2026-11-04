# 接口与契约

Go 契约的权威定义在 [internal/contracts](../internal/contracts)。HTTP 平台路由在 [app/app.go](../internal/app/app.go)，业务路由由各模块 `Register` 提供。本文列出当前接口入口和协作规则。

## 模块注册

[Module](../internal/contracts/module.go) 包含：

```go
Manifest() ModuleManifest
Migrations() MigrationSet
Register(ModuleRegistrar) error
```

Manifest 声明 ID、显示名称、业务版本、契约版本、图标和导航。当前契约版本为 1。顶部导航的下拉菜单（`menu`）和子页面（`subpages`）属于前端 `ModuleUI`，不进入 Manifest，也不改变契约版本；子页面路径不能与后端已注册的页面或资源路径冲突。`Migrations` 提供本模块的 `fs.FS`、SQLite `Dir` 和 PostgreSQL `PostgresDir`，数据库层按当前驱动选择目录；`Register` 调用 `Handle`、`Consume`、`Job`、`Tool`、`Widget` 声明资源。

资源声明含模块归属，名称以模块 ID 为前缀，注册时检查有效性和重复项。认证后请求携带 `contracts.Principal`（用户、角色和会话标识）；私有服务调用 `RequireUserID`，缺失身份返回错误。后台业务通过注入的 `UserDirectory` 枚举有效用户，不能把无身份上下文当作全量权限。App 负责构造模块与所需服务。内置注册表在构建程序时确定；已保存的外部模块记录在运行时恢复，并通过 HTTP 协议管理。当前不开放通过网址新增外部模块。

可选契约：

- [ModuleLifecycle](../internal/contracts/lifecycle.go)：`OnEnabledChanged`、`Close`。无后台资源的模块可以不实现。Registry 在迁移后恢复每个模块的持久化意图，串行执行启停，并负责初始化失败及退出时的资源释放。回调应遵守 context，支持部分失败后重试同一意图；`Close` 必须能清理初始化未完成的资源。传入初始化函数后，资源所有权交给 Registry。
- [ModuleRouteProvider](../internal/contracts/lifecycle.go)：额外声明公共回调或受保护静态入口。公共回调必须显式标注，不会因为普通业务注册而匿名开放。
- [ModuleSessionPolicyProvider](../internal/contracts/lifecycle.go)：声明模块资源的会话策略。`ReadOnly` 只加载会话，不续期；`WrapResponse` 在该策略下调整响应头。请求仍经过鉴权和模块门控。Investment 用它声明内容哈希图表资源的缓存响应。
- [ModuleStartup](../internal/contracts/lifecycle.go)：`OnStarted(ctx)` 在 HTTP 服务启动后调用，回调应及时返回，后台工作使用随 `App.Run` 结束的 context。Investment 用它预热图表资源。

外部协议类型在 [external.go](../internal/contracts/external.go)，`protocolVersion` 当前为 1，与内置 `contractVersion` 分开。远端管理路径为 `/_workbench/manifest|status|state|config`。

## 事件、调度和通知

| 契约 | 当前语义 |
|---|---|
| [EventPublisher](../internal/contracts/event.go) | `Publish` 独立发布，`PublishTx` 加入调用方事务；事件含 userId、topic、schemaVersion、sourceModule、aggregateId、payload |
| `EventConsumer` | ID、模块、主题集合、超时、最大尝试次数、Handler；消费者按至少一次交付设计 |
| [JobDefinition](../internal/contracts/job.go) | cron/interval/once、明确时区、超时、重试；OverlapPolicy 当前支持 skip，MisfirePolicy 支持 skip/run_once |
| [NotificationService](../internal/contracts/notification.go) | 创建、事务内创建、分页、标已读、全部已读、归档、未读数 |
| `NotificationChannel` | 外部投递接口，已装配 Telegram；投递业务显式选择外发的通知 |

通知内容为纯文本，`actionRoute` 为站内路径，`actionLabel` 为跳转文案，幂等键由业务提供并在收件人范围内唯一。通知使用 `RecipientID`，个人调用的收件人必须与可信身份一致。列表、状态操作、SSE 和外部推送均按收件人隔离。`External` 默认关闭，业务显式设置后进入外部投递；该标志持久化且不在用户通知 API 中暴露。`ExpiresAt` 控制投递截止期，未指定时 Telegram 与移动推送使用 15 分钟。APNs/FCM payload 携带 `notificationId` 与 `actionRoute`，原生客户端只把已知站内路径映射为页面，未知路径回到通知列表。SSE 发布 `core.notification.created`、`core.notification.updated` 和 `core.notification.archived`，客户端收到后刷新查询。连接有心跳，不实现基于 Last-Event-ID 的历史补发。

Telegram 使用独立 dispatcher 和已有 `event_deliveries` 持久化投递。移动推送以 `core.notification.created` 事件消费者接入独立 dispatcher，并复用相同的投递重试记录。消费者可通过 `RetryAfterError` 请求更晚的重试时间，通过 `PermanentDeliveryError` 直接终止为 `dead`；详情及设置接口见 [Telegram 通知](notifications.md)。

## AI 和对话

[TextGenerator](../internal/contracts/text.go) 是业务主动调用的文本生成接口，参数为 profile、instruction、input。它不向 Provider 提供业务工具，返回不可用时使用 `ErrAIUnavailable`；不启用 AI 也能构造依赖该接口的业务。

[AIProvider 与 AITool](../internal/contracts/ai.go) 服务于平台 Gateway。Provider 提供普通与流式生成；Gateway 暴露当前启用模块的工具，最多执行四轮工具调用，再进行一次无工具的结果收尾。Responses 请求使用 `store: false`，每轮按顺序重传原始消息、模型输出（含加密推理项）和工具结果，不使用 `previous_response_id`。工具参数在服务端校验，执行审计持久化。会话与工具审计归调用用户；工具继承可信身份，模型参数不能指定执行用户，幂等记录按用户隔离。

Tool 风险分为 read_only、low_write、high_write。写工具需要幂等键；high_write 还需要 `ConfirmedBy`。当前注册工具只含查询和低风险写入，没有券商交易或删除工具。已注册待办创建、查询、更新和完成，通知查询与标记已读，投资持仓、订单、监控查询与监控创建，以及资讯查询、详情和简报生成；资讯简报工具不提交外部推送。Gateway 不提供通用高风险确认交互。聊天请求可附带 `context: { route, label }`，后端校验后仅用于本轮导航上下文；`timeZone` 用于提供当前时间和解析相对日期，默认 UTC。原始用户消息独立保存。

对话请求包含 `conversationId`（可省略以新建）、`message` 和 `profile`（默认 default），响应包含对话 ID、消息 ID、文本、各轮合计用量和工具执行状态。流式 HTTP 事件为 `chat.started`、`chat.delta`、`chat.tool`、`chat.completed` 或 `chat.error`；每次工具执行后立即发送状态，后续请求失败时仍保留已完成操作的反馈。`GET /api/conversations` 默认每页 20 条，`GET /api/conversations/{id}/messages` 默认每页 50 条，二者支持 `limit=1..100` 和 opaque `cursor`；返回 `nextCursor` 时继续读取。`PATCH /api/conversations/{id}` 重命名，`DELETE` 归档。消息页按时间正序返回，客户端恢复并展示历史后才允许续聊。

## 页面和 Widget

[WidgetDefinition](../internal/contracts/widget.go) 声明 ID、模块、schemaVersion、标题、widgetKind、dataRoute、尺寸和默认顺序。尺寸为 small/medium/large。导航用 `pageKey` 关联页面。

前端 [ModuleUI](../web/src/modules/registry.ts) 提供 pages、widgets，以及可选的 icons、settings（个人业务配置）、adminSettings（后台共享业务配置）、menu（顶部导航下拉项）和 subpages（子页面）。后端只能通过这些稳定标识选择本地编译的组件。Widget 接收 `{ widget }`，两类业务设置均接收 `{ enabled }`。`settings` 在 `/settings` 装配，`adminSettings` 仅在管理员路由 `/admin/settings` 装配；界面入口拆分不改变 API、配置归属或后端权限。

Dashboard 保存完整的 Widget 配置集合，包含 ID、visible、size、order；目录查询保留停用业务以便编辑偏好，展示查询只返回启用且可见的卡片。

## HTTP 入口

JSON 错误使用 `code`、`message`，并可含 `details`、`requestId`，定义见 [APIError](../internal/contracts/http.go)。共享 [httpapi](../internal/foundation/httpapi/json.go) 校验 JSON、限制请求体并拒绝未知字段。前端 [api](../web/src/shared/api.ts) 负责凭据、CSRF 和错误映射，未校验的 JSON 返回 `unknown`。消费响应字段时使用 `apiValidated(path, schema, init)` 或显式 `schema.parse`，响应类型由 Zod 推导。关键 HTTP 契约通过真实后端响应与生产前端 schema 的跨端测试验证，见 [web/tests/contracts.test.mjs](../web/tests/contracts.test.mjs)。原生接入流程见[移动端接入](mobile.md)，相关路径和字段见 [OpenAPI](mobile.openapi.yaml)。

`POST /api/modules/external` 已移除，当前返回 404。下表中的外部管理、页面和代理入口仅用于已保存的外部模块记录。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/health/live`、`/health/ready` | 存活、数据库就绪 |
| GET | `/api/auth/status`、`/api/auth/csrf` | 登录状态、CSRF token |
| POST | `/api/auth/login`、`/api/auth/logout` | 使用 `{ username, password }` 登录 / 退出 |
| POST | `/api/auth/token`、`/api/auth/token/refresh` | 无 Cookie 的设备登录、一次性 refresh token 轮换 |
| POST | `/api/auth/token/revoke`、`/api/auth/logout-all` | 注销当前设备 / 注销全部网页与设备 |
| POST | `/api/auth/browser-grant` | 用设备 Bearer 创建 60 秒一次性 WebView 换票 |
| GET | `/api/auth/browser-session?ticket=...` | 公开消费换票，建立绑定父设备的 Cookie Session 并 303 跳转终端 |
| GET | `/api/auth/sessions` | 登录设备列表 |
| DELETE | `/api/auth/sessions/{id}` | 注销指定设备 |
| POST | `/api/auth/signing-key/rotate` | 管理员轮换设备 access token 签名密钥 |
| GET / POST | `/api/admin/users` | 管理员列出 / 创建用户 |
| PATCH | `/api/admin/users/{id}` | 管理员修改 `{ displayName?, role?, status? }` |
| POST | `/api/admin/users/{id}/password` | 管理员重置密码；返回一次性初始密码 |
| GET | `/api/modules` | 合并内置与外部目录（含 `kind`），只返回缓存状态 |
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
| GET | `/api/notifications` | 支持 source、unread、limit、cursor 的列表 |
| GET | `/api/notifications/unread-count`、`/api/notifications/stream` | 未读数（可按 source 过滤）、SSE |
| PUT | `/api/notifications/read`、`/api/notifications/archive` | 提交 `{ ids }` |
| PUT | `/api/notifications/read-all` | 标记当前已有通知为已读 |
| POST / PUT / DELETE | `/api/mobile/push-token` | 注册、更新、删除当前设备的 APNs/FCM token；成功返回 204 |
| GET | `/api/ai/status` | AI 是否可用 |
| POST | `/api/chat`、`/api/chat/stream` | 对话、流式对话 |
| GET | `/api/conversations` | 分页列出未归档对话 |
| GET | `/api/conversations/{id}/messages` | 分页读取当前对话消息，不存在时返回 404 |
| PATCH / DELETE | `/api/conversations/{id}` | 重命名 / 归档对话 |
| GET | `/api/settings` | 本人外观；仅管理员额外获得 AI、日志等级和部署信息，密钥不回显 |
| PUT | `/api/settings/ai`、`/api/settings/appearance` | 保存 AI 或外观 |
| PUT | `/api/settings/logging` | 保存 `{ "level": "info" }` 并立即生效；支持 debug/info/warn/error，返回规范化等级 |
| POST | `/api/settings/ai/test` | 测试候选 AI 配置，不保存 |
| GET / PUT | `/api/settings/telegram` | 读取脱敏配置和状态 / 保存 Telegram 配置 |
| POST | `/api/settings/telegram/test` | 发送一条测试消息，不保存候选配置 |
| PUT | `/api/settings/password` | 验证当前密码并修改 |
| POST | `/api/modules/investment/schwab/oauth/start` | 创建移动 Schwab 授权 |
| GET | `/api/modules/investment/schwab/oauth/status/{id}` | 查询移动 Schwab 授权结果 |
| POST | `/api/system/backup` | SQLite 创建服务端备份并返回文件名；PostgreSQL 返回 HTTP 501，由数据库服务执行备份 |

除健康检查和鉴权入口外，上述平台 API 经过登录校验；local 模式只允许原始管理员单用户。用户管理、全局模块管理、AI 服务、日志、备份、签名密钥轮换及历史外部模块入口额外要求管理员。个人数据始终限定本人，管理员也不绕过归属，详见[用户与权限](multi-user.md)。Host、Origin 和 CSRF 保护由外层中间件处理，local 模式也保留请求保护。网页使用服务端 Cookie Session 与 CSRF；原生设备使用 `Authorization: Bearer`。设备登录和刷新必须是无 Cookie、无 Authorization 的 `application/json` 请求，其他请求混用 Cookie 与 Bearer 返回 400。业务 API 另外经过模块启停门控，停用时返回 HTTP 503 和 `module_disabled`。外部业务代理还要求当前连接的当前代数已确认启用，且健康不是 `offline` 或 `incompatible`。

网页登录与状态响应包含 `mustChangePassword` 和当前 `user`（ID、用户名、显示名、角色、状态和时间）；未登录时没有用户对象。新建密码凭据首次登录时该字段为 `true`，除 `PUT /api/settings/password` 外的受保护接口返回 `403 password_change_required`。改密提交 `{ currentPassword, newPassword }`，首次新密码必须与当前密码不同；成功返回 `{ reauthenticate: true }`，并注销本人的全部网页与设备会话。

WebView 换票创建接口只接受设备 Bearer，响应 `{ urlPath, expiresAt }`，目标固定为投资终端且不接受客户端 target。ticket 是 43 字符 base64url 短时登录凭据，数据库仅保存哈希，60 秒内单次消费。消费入口拒绝 Authorization，可携带 WebView 已有 Cookie；成功响应使用 `no-store`、`no-referrer`，写入网页 Session 后 303 到 `/investment/terminal`。Cookie Session 保留父设备关联，后续鉴权、状态查询和长连接检查同时验证父设备。

业务 HTTP、资源标识和字段说明见 [Todo](modules/todo.md)、[Wallos](modules/todo-wallos.md)、[Investment](modules/investment.md) 和 [资讯](modules/news.md)。
