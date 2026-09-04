# MVP 核心 Go 契约

本文定义接口形状和语义，包名仅为建议。实现时允许按 Go 编译边界拆包，但不得改变 ADR 中的行为保证。

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
    Topics   []string
    Timeout  time.Duration
    Attempts int
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
- 列表分页使用 `limit + cursor`。`limit` 默认 50、最大 100；`cursor` 是服务端生成的 opaque Base64URL 字符串，客户端不得解析或构造。
- HTTP JSON 时间统一输出 UTC RFC3339/RFC3339Nano；SQLite 内部时间统一存 UTC Unix 毫秒。
- 应用生成的实体、事件、消息和运行 ID 是 32 位小写十六进制、128-bit、按毫秒近似可排序的 opaque 字符串；客户端不得依赖其内部布局。
- 修改状态的请求使用 JSON body，并经过 Host、Origin 和 CSRF 校验；不通过 query string 传递秘密或对话正文。
