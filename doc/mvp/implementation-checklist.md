# MVP 实施与验收清单

## 阶段 0：契约冻结

- [ ] 将核心契约拆成实际 Go package，并通过编译验证无循环依赖
- [ ] 固定 Module ID、事件 topic、Consumer ID、Job ID 命名规则
- [ ] 固定 API 错误、分页、时间和 ID 格式
- [ ] 为事件、AITool 参数和 Widget 描述添加 schema version
- [ ] 形成 threat model：监听地址、Session、CSRF、SSRF、Tool 越权和秘密存储

## 阶段 1：Foundation

- [ ] 初始化 Go module、chi server 和配置加载
- [ ] SQLite pragma、连接池、进程文件锁与 Goose migration
- [ ] 实现 Module Registry 暂存、校验和原子提交
- [ ] 实现 enabled gate，并覆盖 Route/Consumer/Tool/Job
- [ ] 实现 Transactional Outbox、claim lease、重试和 dead delivery
- [ ] 实现结构化日志、request ID、健康检查和优雅退出

验收场景：

- [ ] 业务写入与事件发布同时成功或同时回滚
- [ ] Consumer 在成功落库后崩溃，重复投递不会产生重复副作用
- [ ] Dispatcher 执行中进程退出，lease 到期后可恢复
- [ ] 禁用模块后各入口均不可继续执行，重新启用后历史数据仍在
- [ ] 两个进程不能同时写同一工作空间

## 阶段 2：通用能力

- [ ] gocron Job 注册、持久定义、运行历史和超时
- [ ] local/password 鉴权、Argon2id、SCS Store、CSRF/Origin/Host 校验
- [ ] NotificationService、清理任务和 SSE heartbeat/reconnect
- [ ] OpenAI Provider、Responses API、`store:false`、超时与取消
- [ ] AITool Schema 校验、风险分级、确认、幂等和审计脱敏
- [ ] Conversation、Dashboard 聚合和模块兼容性错误

验收场景：

- [ ] Job 超时、失败重试、禁止重叠和进程离线 misfire 行为符合定义
- [ ] 非 loopback 的不安全配置启动失败
- [ ] 重复通知 idempotency key 只产生一条记录
- [ ] SSE 断线不会丢失最终状态，重连查询可恢复
- [ ] AI Provider 不可用时普通业务和 Dashboard 仍可使用
- [ ] 禁用模块的 Tool 即使被模型点名也不能执行

## 阶段 3：Web Shell

- [ ] React Router、TanStack Query、Zod 和统一 API client
- [ ] 编译期 page/widget 注册表与 `React.lazy`
- [ ] 动态导航、Dashboard、通知中心、主题和命令面板
- [ ] `go:embed` SPA fallback；API 404 不得回退成 HTML
- [ ] 可访问性、键盘导航、加载态、空状态和错误态

## 阶段 4：Todo 验证模块

- [ ] 独立 `todo_*` migration 和 CRUD API
- [ ] 创建/完成任务领域事件
- [ ] 单个周期扫描 Job 处理到期事项
- [ ] 幂等通知、Dashboard Widget 和低风险 AI Tool
- [ ] 完成一次旧数据库升级、备份、恢复和完整性检查演练

## MVP 完成定义

- [ ] 全新环境可以构建一个可执行文件并启动
- [ ] 默认 local 模式无需登录且只监听 loopback
- [ ] password 模式安全基线测试通过
- [ ] 重启后模块、事件、任务、通知和对话状态可恢复
- [ ] 关键故障路径拥有集成测试，而不只有 happy path
- [ ] 数据库可以在线一致性备份，并在新目录恢复启动
- [ ] Todo 证明新增模块无需修改 Foundation 实现
- [ ] 文档与实际接口、migration 和配置样例一致

