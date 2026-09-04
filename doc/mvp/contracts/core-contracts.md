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
    ID         string
    Module     ModuleID
    Title      string
    WidgetKind string
    DataRoute  string
    Size       WidgetSize
    Order      int
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

