# 个人工作台（Workbench）系统架构设计 v1.2

状态：MVP 基线  
日期：2026-09-04

## 1. 目标与基础假设

Workbench 面向单用户、本地优先场景。默认只监听 `127.0.0.1`，以一个 Go 可执行文件和一个 SQLite 文件运行；前端构建产物通过 `go:embed` 内嵌。

前端构建产物由构建流程复制到 `internal/webui/dist`，再由该 Go package 执行嵌入，避免使用 Go 不允许的跨目录 embed pattern。

后端统一使用 Go。Rust 仅在未来出现经过测量的独立高性能需求时，以子进程或 IPC 组件引入，不作为当前后端技术栈。

业务模块可以启停，但模块代码在编译期进入主程序。运行时不加载未知二进制插件。未来接入行情、券商或消息渠道时，通过 Provider/Adapter 扩展稳定边界。

## 2. 设计原则

1. 底座契约稳定，业务模块保持轻量。
2. 采用模块化单体约束依赖，不以微服务增加本地部署复杂度。
3. SQLite 是持久状态的唯一真相；内存结构只能用于缓存、唤醒和实时提示。
4. 跨模块不读写对方表，不直接调用对方实现。
5. 需要可靠副作用的行为必须可幂等、可重试、可审计。
6. 模块启停必须作用于路由、事件消费者、AI Tool 和 Job，而不是卸载代码。
7. 安全模式显式配置，不根据监听地址做含糊的自动推断。

## 3. 总体架构

```text
浏览器
  │ HTTP / POST SSE
  ▼
单一 Go 进程（chi + go:embed）
  ├─ 业务模块：todo / investment / ...
  ├─ 通用能力：AI / conversation / dashboard / notifications / scheduler
  └─ 底座：auth / database / transactional events / module registry
  │
  ▼
SQLite 单文件
```

依赖方向：

```text
cmd → app
app → foundation + capabilities + modules + webui
modules → 公开契约
capabilities → foundation 公开契约
foundation → Go 标准库与基础设施依赖
```

稳定接口和资源描述放入不包含实现的 `internal/contracts` 叶子包，各层都可以依赖它。`internal/modules/*` 之间禁止直接 import。模块需要协作时发布版本化事件，或调用通用能力接口。

## 4. 后端结构

### 4.1 模块注册

模块实现 `Module`，提供 Manifest、migration 集合并向 `ModuleRegistrar` 注册资源。主程序显式导入模块，因而注册表在编译期确定。

启动顺序：

1. 获取工作空间文件锁并打开 SQLite。
2. 执行应用级 migration。
3. 收集模块并校验唯一 ID、依赖与契约版本。
4. 按稳定顺序执行已知模块 migration；禁用模块也保留已有数据和 schema 可读性。
5. 在暂存 Registrar 中注册 Route、Consumer、AITool、Widget 和 Job。
6. 完成全局冲突校验后一次性提交注册结果。
7. 启动事件 dispatcher、Scheduler 和 HTTP server。

`modules.enabled` 是运行时 gate。禁用模块后，其 HTTP 路由返回不可用、Consumer 不领取新事件、AITool 不暴露、Job 不调度；历史数据不删除。启停无需重新编译，但新增模块需要重新构建程序。

禁用模块采用“暂停后继续”语义：系统仍为其已注册 Consumer 保留待投递记录；重新启用后按正常重试规则处理积压事件。对应 Job 的错过执行按各自 misfire 策略处理。事件保留与永久移除模块是独立的运维决策。

### 4.2 HTTP

HTTP 路由器采用 chi。业务模块的公开契约只使用 `net/http`，不依赖 chi 类型，防止框架渗透。

统一约定：

- API 前缀为 `/api`，模块资源位于 `/api/modules/{moduleID}`。
- JSON 字段使用 `camelCase`，时间使用 RFC 3339；数据库内部时间统一为 Unix 毫秒 UTC。
- 列表接口使用游标分页，不使用不稳定的大偏移分页。
- 错误响应包含稳定 `code`、可读 `message` 和可选 `details`/`requestId`。
- 修改状态的请求支持或要求幂等键时，使用 `Idempotency-Key`。

### 4.3 数据库

使用 Go 1.27.x、`modernc.org/sqlite v1.58.x`、`database/sql + sqlc` 和 Goose migration。

默认约束：

- 一个数据库文件代表一个工作空间，不设置 `owner_id`。
- WAL 模式，`synchronous=FULL`，启用 foreign keys 和 busy timeout。
- 连接池最多 4 个连接；写事务保持短小。
- 同一数据库文件只允许一个 Workbench 进程持有文件锁。
- 在线备份使用 `VACUUM INTO` 或 SQLite backup API，之后运行 `integrity_check`；不能把运行中 WAL 数据库的“直接复制主文件”宣称为一致性备份。
- 底座表由应用 migration 管理；模块表使用模块前缀，并由模块 migration 管理。

### 4.4 可靠事件

事件总线不是易失的 Go channel Pub/Sub，而是 SQLite Transactional Outbox。

- 业务状态和事件可通过同一事务提交。
- 交付语义为至少一次，不承诺恰好一次。
- Consumer 使用稳定 ID，并以 `event_deliveries(event_id, consumer_id)` 记录状态。
- Handler 必须幂等；失败采用有界指数退避，超过次数进入 dead 状态供人工重试。
- 事件 Envelope 包含 ID、topic、schema version、source module、发生时间和 JSON payload。
- 事件只追加；敏感数据不应无条件写入 payload。

进程内 channel 只用于唤醒 dispatcher，丢失唤醒不影响数据库轮询恢复。

### 4.5 Scheduler

Scheduler 使用 `go-co-op/gocron/v2`，统一承载 cron、固定间隔和一次性任务。

每个 Job 必须声明：稳定 ID、所属模块、schedule、时区、超时、重入策略、misfire 策略和重试策略。Job 定义与运行历史持久化到 SQLite。

默认行为：

- 默认禁止同一 Job 重叠执行。
- 默认时区为 UTC；面向用户的计划显式保存 IANA 时区。
- 进程离线期间错过的周期任务默认合并执行一次，而非逐次补跑。
- Handler 接收带 deadline 的 context，并需要可安全重试。
- 模块不能自行启动 ticker 或常驻 goroutine。

### 4.6 鉴权与网络安全

运行模式显式分为：

- `local`：仅允许 loopback 监听，不显示登录流程。
- `password`：使用密码登录和服务端 Session；允许非 loopback，但默认要求 HTTPS。

密码使用 Argon2id 哈希；Session 使用 SCS 语义并实现 modernc SQLite Store，不使用 JWT。所有修改状态的请求执行 CSRF、Origin 和 Host 校验；Cookie 使用 `HttpOnly`、合适的 `SameSite`，HTTPS 下启用 `Secure`。

不得以“用户通常只在局域网使用”为由关闭上述校验。

## 5. 通用能力

### 5.1 AI 与站内对话

AI 层采用 Provider 架构，MVP 只实现 OpenAI 官方 Go SDK 和 Responses API，默认 `store:false`。

核心能力为 `Generate` 和 `Stream`；Summarize、Classify 等属于其上的用例封装，不作为必须由每个 Provider 原生实现的基础接口。

AITool 要求：

- 使用严格 JSON Schema，并进行服务端二次校验。
- 声明只读、低风险写入或高风险写入等级。
- 写操作需要幂等键；高风险操作必须要求用户确认。
- 记录调用、确认、结果和错误审计，但不默认保存秘密或完整敏感 prompt。
- Provider 返回的工具名不能绕过当前 enabled module gate。

站内对话记录保存在本地 SQLite。流式响应使用 POST 后返回的 SSE 流，避免把用户输入放入 URL。未来外部 IM 只新增 Channel Adapter，不改对话核心。

### 5.2 通知中心

通知中心负责通知的持久记录、查询、未读计数、已读和归档。Producer 可以是 EventConsumer、ScheduledJob 或同步用户操作。

- 创建通知必须提供唯一 `idempotencyKey`。
- 内容只接受纯文本。
- 动作只允许校验后的站内相对 `actionRoute`。
- 使用 `readAt` 和 `archivedAt` 表达状态。
- 通知不是审计日志；业务事实仍记录在领域表和事件中。

站内实时更新使用 `GET /api/notifications/stream` SSE。SSE 可推送 `notification.created`、`notification.updated`、`notification.archived` 和 `unread.count`，每 15～30 秒发送 heartbeat。客户端重连后必须重新查询列表和未读数，SSE 本身不承担可靠消息队列职责。

MVP 不实现通用规则 DSL。重复规则在两个以上模块中稳定出现后再提炼。浏览器通知、Web Push、OS 通知与外部 IM 均留作后续 Channel Adapter。

### 5.3 Dashboard

`GET /api/dashboard` 根据 enabled module 的 Widget 描述汇总布局和数据端点。后端不返回任意可执行前端组件，而只返回稳定的 `widgetKind`、props 和 data source 描述。

每日简报是可选的异步产物；AI 不可用时不阻塞普通 Dashboard。

## 6. 前端架构

前端使用 React、TypeScript、Vite、Tailwind CSS、shadcn/ui、React Router、TanStack Query 和 Zod。

模块页面和 Widget 在编译期通过 Vite glob 或显式注册表纳入 bundle，再通过 `React.lazy` 按需加载。后端 `/api/modules` 返回 `pageKey`、`widgetKind`、导航和 enabled 状态；前端只渲染本地注册表认识的键。未知键显示兼容性错误，而不是执行动态代码。

全局状态只保存鉴权状态、模块清单、主题和未读数。服务端数据由 TanStack Query 管理，并由 Zod 校验边界响应。

视觉规范：

- 主色 `#6366F1`
- 成功 `#10B981`、警告 `#F59E0B`、危险 `#EF4444`、信息 `#3B82F6`
- 深色背景 `#0F172A`
- Inter + PingFang SC，基础字号 14px，卡片圆角 12px
- 可折叠左侧导航、顶部工具栏、命令面板和卡片化主区域
- Skeleton、Empty State、Toast 和清晰的危险操作确认

## 7. 业务模块

### 7.1 Todo（MVP 验证模块）

Todo 用于验证完整扩展链路：模块 migration、CRUD Route、Widget、到期扫描 Job、事件、通知和一个低风险 AITool。它不是为了在 MVP 阶段做成完整任务管理平台。

### 7.2 Investment（MVP 后）

投资模块拥有 `investment_*` 表。行情 Provider 由 Job 定期同步，业务数据与 `investment.price.updated` 事件同事务提交；事件消费者判断提醒阈值并通过 NotificationService 幂等创建通知。

MVP 后先使用 mock Provider，再选择真实行情/券商 API。真实交易下单属于高风险能力，不因已有 Function Calling 而自动开放。

## 8. 可观测性与生命周期

- 使用结构化日志，并贯穿 `requestId`、`eventId`、`jobRunId` 和 `toolCallId`。
- `/health/live` 只表示进程存活；`/health/ready` 检查数据库和 migration 状态。
- 优雅退出顺序：停止接收请求、停止领取新任务、等待有界时间、释放数据库和文件锁。
- 日志、事件、通知、Job 运行历史和对话分别制定保留策略；不可把 `events_log` 无限增长当作审计设计。

## 9. 部署形态

```text
workbench                 Go 可执行文件（含前端资源）
~/.workbench/data.db      SQLite 主文件
~/.workbench/config.toml  非秘密配置
```

API Key 优先从环境变量或操作系统秘密存储读取，不写入普通配置文件或日志。Docker 是未来部署适配器，不是本地 MVP 的依赖。

## 10. 迭代路线

1. Foundation：数据库、迁移、模块注册、事件。
2. Capabilities：Scheduler、鉴权、通知、AI、对话、Dashboard。
3. Web shell：模块注册表、导航、Widget、主题。
4. Todo：端到端验证和故障场景测试。
5. Investment：mock 数据源。
6. 真实数据 Provider、外部通知 Channel 和远程部署强化。
