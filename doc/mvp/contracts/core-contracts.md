# MVP 核心 Go 契约

本文定义接口形状和语义，实际稳定类型位于 `internal/contracts`。业务接入步骤见 [业务开发指南](../../business-development.md)。

## 基础类型

```go
type ModuleID string
type EventID string
type ConsumerID string
type JobID string

type ModuleManifest struct {
    ID              ModuleID
    Name            string
    Version         string
    ContractVersion int
    Icon            string
    Navigation      []NavigationItem
}

type NavigationItem struct {
    Label   string
    Route   string
    PageKey string
    Order   int
}
```

## Module 与原子注册

```go
type Module interface {
    Manifest() ModuleManifest
    Migrations() MigrationSet
    Register(r ModuleRegistrar) error
}

type MigrationSet struct {
    FS  fs.FS
    Dir string
}

type ModuleRegistrar interface {
    Handle(method, pattern string, handler http.Handler) error
    Consume(consumer EventConsumer) error
    Tool(tool AITool) error
    Widget(widget WidgetDefinition) error
    Job(job JobDefinition) error
}
```

Registrar 在暂存区收集声明，校验模块归属、ID、路由和依赖冲突后整体提交。`Register` 失败时不得留下部分注册结果。

## 事件

```go
type Event struct {
    ID            EventID
    Topic         string
    SchemaVersion int
    SourceModule  ModuleID
    AggregateID   string
    OccurredAt    time.Time
    Payload       json.RawMessage
}

type NewEvent struct {
    Topic         string
    SchemaVersion int
    SourceModule  ModuleID
    AggregateID   string
    Payload       json.RawMessage
}

type EventPublisher interface {
    Publish(ctx context.Context, event NewEvent) (EventID, error)
    PublishTx(ctx context.Context, tx *sql.Tx, event NewEvent) (EventID, error)
}

type EventConsumer struct {
    ID       ConsumerID
    Module   ModuleID
    Topics   []string
    Timeout  time.Duration
    MaxAttempts int
    Handler  func(context.Context, Event) error
}
```

Topic 使用 `<module>.<entity>.<action>`。Consumer ID 发布后必须稳定。Handler 按至少一次交付设计，成功返回前必须完成幂等写入。

模块、资源与前端键统一使用稳定命名：Module ID 为小写 snake_case；Consumer ID、Job ID、AITool 名、Widget ID、`pageKey` 和 `widgetKind` 均以 `<module>.` 开头；模块 HTTP API 位于 `/api/modules/<module>/`。事件 topic 的首段必须与 `SourceModule` 一致。

## Scheduler

```go
type ScheduleKind string

const (
    ScheduleCron     ScheduleKind = "cron"
    ScheduleInterval ScheduleKind = "interval"
    ScheduleOnce     ScheduleKind = "once"
)

type JobDefinition struct {
    ID             JobID
    Module         ModuleID
    Schedule       ScheduleSpec
    TimeZone       string
    Timeout        time.Duration
    OverlapPolicy  OverlapPolicy
    MisfirePolicy  MisfirePolicy
    Retry           RetryPolicy
    Handler         func(context.Context, JobRun) error
}

type ScheduleSpec struct {
    Kind       ScheduleKind
    Expression string
    Interval   time.Duration
    RunAt      time.Time
}

type JobRun struct {
    ID          string
    JobID       JobID
    ScheduledAt time.Time
    Attempt     int
}
```

持久表保存可配置状态与执行历史；函数 Handler 来自编译期注册，不序列化到数据库。

## AI Provider 与 Tool

```go
type AIProvider interface {
    Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
    Stream(ctx context.Context, req GenerateRequest) (AIStream, error)
}

type AITool struct {
    Name             string
    Module           ModuleID
    SchemaVersion    int
    Description      string
    ParametersSchema json.RawMessage
    Risk             ToolRisk
    Handler          ToolHandler
}

type ToolHandler func(context.Context, ToolCall) (ToolResult, error)

type ToolCall struct {
    ID             string
    Arguments      json.RawMessage
    IdempotencyKey string
    ConfirmedBy    string
}
```

`GenerateRequest` 不暴露供应商专属结构；模型、预算和超时通过应用定义的 profile 选择。高风险 Tool 必须在 Handler 执行前验证确认凭证。

## Widget

```go
type WidgetDefinition struct {
    ID            string
    Module        ModuleID
    SchemaVersion int
    Title         string
    WidgetKind    string
    DataRoute     string
    Size          WidgetSize
    Order         int
}
```

`WidgetKind` 必须存在于前端编译期注册表。`DataRoute` 必须是本站 API 相对路径。

## Notification

```go
type NotificationService interface {
    Create(ctx context.Context, n NewNotification) (Notification, error)
    CreateTx(ctx context.Context, tx *sql.Tx, n NewNotification) (Notification, error)
    List(ctx context.Context, q NotificationQuery) (NotificationPage, error)
    MarkRead(ctx context.Context, ids []string) error
    MarkAllRead(ctx context.Context, before time.Time) error
    Archive(ctx context.Context, ids []string) error
    UnreadCount(ctx context.Context) (int64, error)
}

type NewNotification struct {
    SourceModule   ModuleID
    Severity       NotificationSeverity
    Title          string
    Content        string
    ActionLabel    string
    ActionRoute    string
    IdempotencyKey string
    SourceEventID  EventID
    ExpiresAt      *time.Time
}
```

创建通知和发布 `core.notification.created` 应处于同一事务。未来 Channel 契约为：

```go
type NotificationChannel interface {
    ID() string
    Deliver(context.Context, Notification) error
}
```

外部 Channel 的 delivery 状态、重试和秘密配置必须独立建模，不复用站内 `readAt`。

## HTTP 错误

```go
type APIError struct {
    Code      string         `json:"code"`
    Message   string         `json:"message"`
    Details   map[string]any `json:"details,omitempty"`
    RequestID string         `json:"requestId,omitempty"`
}
```

错误码是客户端判断依据；`message` 用于展示，不作为程序分支条件。

## HTTP 数据约定

- API 路径统一位于 `/api`；业务模块路径位于 `/api/modules/<module>`。
- 成功响应和错误响应使用 JSON；错误结构固定为 `APIError`，`code` 是稳定的机器可读 snake_case 字符串，`requestId` 可选。
- 无界业务列表分页使用 `limit + cursor`；模块/Widget 目录及固定三品种模拟行情为有界配置或聚合响应。`limit` 默认 50、最大 100；`cursor` 是服务端生成的 opaque Base64URL 字符串，客户端不得解析或构造。
- HTTP JSON 时间统一输出 UTC RFC3339/RFC3339Nano；SQLite 内部时间统一存 UTC Unix 毫秒。
- 应用生成的实体、事件、消息和运行 ID 是 32 位小写十六进制、128-bit、按毫秒近似可排序的 opaque 字符串；客户端不得依赖其内部布局。
- 修改状态的请求使用 JSON body，并经过 Host、Origin 和 CSRF 校验；不通过 query string 传递秘密或对话正文。

## 业务主动调用 AI

```go
type TextRequest struct {
    Profile string
    Instruction string
    Input string
}
type TextGenerator interface {
    GenerateText(context.Context, TextRequest) (string, error)
}
```

通过应用装配的 TextService 注入业务，使用当前共享 AI 设置；未配置返回 `contracts.ErrAIUnavailable`。该入口不传递也不执行 AITool，供应商错误被脱敏。已有请求使用旧配置完成，保存后的新请求使用新配置。业务自行定义期限、读取数据、持久化生成结果，不在数据库事务中等待 AI。

## 总览布局 HTTP 契约

- `GET /api/dashboard` 返回 `{widgets: [...]}`，只含 enabled 业务中 visible 的卡片。
- `GET /api/dashboard/widgets` 返回全部编译期卡片，在 Widget 描述上增加 `visible`、`enabled`。`size/order` 已应用工作空间偏好。
- `PUT /api/dashboard/layout` 请求 `{items: [{id, visible, size, order}]}`，必须完整包含当前每张卡片一次；ID 不得未知或重复，visible 必填，size 为 small/medium/large，order 为 0～100000。无效请求返回 `400 invalid_layout`，不覆盖原配置。
- `DELETE /api/dashboard/layout` 恢复业务声明的默认值；写请求经过相同鉴权与 CSRF 校验。
- 保存与重置成功返回完整卡片目录。排序先按 order，再按 ID 保证稳定；相同 order 合法。
- 新卡片默认 visible，size/order 使用注册值。业务停用时保留偏好；不再编译的卡片不进入目录，下次保存完整布局会去掉其旧偏好。
- 总览标题、容器和错误边界由平台提供，数据与组件实现由业务提供。
