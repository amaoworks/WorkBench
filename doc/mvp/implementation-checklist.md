# MVP 实施与验收清单

## 阶段 0：契约冻结

- [x] 将核心契约拆成实际 Go package，并通过编译验证无循环依赖
- [x] 固定 Module ID、事件 topic、Consumer ID、Job ID 命名规则
- [x] 固定 API 错误、分页、时间和 ID 格式
- [x] 为事件、AITool 参数和 Widget 描述添加 schema version
- [x] 形成 threat model：监听地址、Session、CSRF、SSRF、Tool 越权和秘密存储

## 阶段 1：Foundation

- [x] 初始化 Go module、chi server 和配置加载
- [x] SQLite pragma、连接池、进程文件锁与 Goose migration
- [x] 实现 Module Registry 暂存、校验和原子提交
- [x] 实现 enabled gate，并覆盖 Route/Consumer/Tool/Job
- [x] 实现 Transactional Outbox、claim lease、重试和 dead delivery
- [x] 实现结构化日志、request ID、健康检查和优雅退出

验收场景：

- [x] 业务写入与事件发布同时成功或同时回滚
- [x] Consumer 在成功落库后崩溃，重复投递不会产生重复副作用
- [x] Dispatcher 执行中进程退出，lease 到期后可恢复
- [x] 禁用模块后各入口均不可继续执行，重新启用后历史数据仍在
- [x] 两个进程不能同时写同一工作空间

## 阶段 2：通用能力

- [x] gocron Job 注册、持久定义、运行历史和超时
- [x] local/password 鉴权、Argon2id、SCS Store、CSRF/Origin/Host 校验
- [x] NotificationService、清理任务和 SSE heartbeat/reconnect
- [x] OpenAI Provider、Responses API、`store:false`、超时与取消
- [x] AITool Schema 校验、风险分级、确认、幂等和审计脱敏
- [x] Conversation、Dashboard 聚合和模块兼容性错误

验收场景：

- [x] Job 超时、失败重试、禁止重叠和进程离线 misfire 行为符合定义
- [x] 非 loopback 的不安全配置启动失败
- [x] 重复通知 idempotency key 只产生一条记录
- [x] SSE 断线不会丢失最终状态，重连查询可恢复
- [x] AI Provider 不可用时普通业务和 Dashboard 仍可使用
- [x] 禁用模块的 Tool 即使被模型点名也不能执行

## 阶段 3：Web Shell

- [x] React Router、TanStack Query、Zod 和统一 API client
- [x] 编译期 page/widget 注册表与 `React.lazy`
- [x] 动态导航、Dashboard、通知中心、主题和命令面板
- [x] `go:embed` SPA fallback；API 404 不得回退成 HTML
- [x] 可访问性、键盘导航、加载态、空状态和错误态

## 阶段 4：Todo 验证模块

- [x] 独立 `todo_*` migration 和 CRUD API
- [x] 创建/完成任务领域事件
- [x] 单个周期扫描 Job 处理到期事项
- [x] 幂等通知、Dashboard Widget 和低风险 AI Tool
- [x] 完成一次旧数据库升级、备份、恢复和完整性检查演练

## MVP 完成定义

- [x] 全新环境可以构建一个可执行文件并启动
- [x] 默认 local 模式无需登录且只监听 loopback
- [x] password 模式安全基线测试通过
- [x] 重启后模块、事件、任务、通知和对话状态可恢复
- [x] 关键故障路径拥有集成测试，而不只有 happy path
- [x] 数据库可以在线一致性备份，并在新目录恢复启动
- [x] Todo 证明新增模块无需修改 Foundation 实现
- [x] 文档与实际接口、migration 和配置样例一致

## 最终验收记录

验收日期：2026-09-04。

以下命令均通过：

```bash
GO_BIN=./.tools/go/bin/go GOCACHE=/tmp/workbench-gocache ./scripts/test.sh
GOCACHE=/tmp/workbench-race-cache ./.tools/go/bin/go test -race ./...
GOCACHE=/tmp/workbench-gocache ./.tools/go/bin/go vet ./...
npm --prefix web run lint
npm --prefix web run build
git diff --check
```

验收使用 Go 1.27.1、sqlc 1.31.1、Node.js 24.19.0 和 npm 11.17.0。`CGO_ENABLED=0` 构建所得二进制为静态链接文件；真实进程演练覆盖健康检查、动态模块 API、SPA 与 API 404 边界、CSRF 下 Todo 写入、在线备份及从备份恢复、password 登录与 Session 持久化、SIGTERM 优雅退出。

自动化测试分别覆盖事务与 Outbox 原子性、Consumer 幂等、过期 lease 恢复、模块 Gate、工作空间文件锁、旧 core schema 升级、Scheduler 重试/超时/重叠/misfire、鉴权安全基线、通知幂等与 SSE、AI Tool 权限/确认/审计，以及 SQLite 重开后的对话恢复。
