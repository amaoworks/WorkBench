# 架构与目录

Workbench 是本地优先、单用户的个人工作台。当前采用 Go 模块化单体、React 前端和共享 SQLite 工作空间；前端生产资源嵌入 Go 程序。运行时还会产生 SQLite WAL、锁文件和备份，详见[数据设计](data-model.md)。

## 三层职责

| 层级 | 后端目录 | 职责 |
|---|---|---|
| 业务应用层 | `internal/modules/` | 待办、投资等具体业务，拥有各自数据、规则、接口、任务与界面 |
| 通用能力层 | `internal/capabilities/` | AI、对话、调度、通知、总览等可复用服务 |
| 基础设施层 | `internal/foundation/` | 数据库生命周期、鉴权、可靠事件、模块注册、HTTP 与 ID 工具 |

`internal/contracts` 存放跨层接口和资源描述。`internal/app` 负责装配、运行生命周期及平台配置接口；它不是业务模块目录。`internal/webui` 负责嵌入前端资源和 SPA 路由回退。

## 当前目录

```text
cmd/workbench/                 启动参数、环境变量、信号处理
internal/
  app/                         依赖装配、平台路由、设置、备份入口
  contracts/                   Module、Event、Job、AI、Notification、Widget
  foundation/
    auth/                      凭据、Session、Host/Origin/CSRF
    database/                  SQLite、迁移、备份及平台 SQL
    events/                    事务事件存储与投递
    modules/                   注册目录、持久化启停状态、入口门控
    httpapi/                   JSON 请求与响应
    identity/                  实体 ID
  capabilities/
    ai/                        Provider、文本生成、工具执行与审计
    conversation/              对话持久化、普通响应与流式响应
    dashboard/                 Widget 目录和总览布局
    notifications/             通知持久化、状态操作、SSE
    scheduler/                 调度、执行记录、重试与恢复
  modules/
    todo/                      待办及 Wallos 联动
    investment/                Schwab 代理、TradingView 投资终端
  webui/dist/                  已检入的前端生产资源
web/src/
  app/                         登录、应用外壳、导航、主题、命令面板
  features/                    AI 面板、总览、通知、平台设置
  modules/
    registry.ts                编译期业务界面注册表
    todo/                      Todo 页面、Widget、设置、查询、schema
    investment/                行情页面、Widget、Schwab 设置、查询、schema
  components/ui/               通用界面组件
  shared/                      HTTP 客户端、平台共享 schema、工具
  styles.css                   主题变量与共享样式
scripts/                       构建、测试与浏览器回归脚本
doc/                           当前设计和使用开发说明
sqlc.yaml                      平台和各模块的 SQL 生成配置
```

业务按模块优先组织，前后端使用同一个模块 ID 对应两个目录。模块专属的类型、查询、设置和第三方联动留在模块内；通用目录保留平台或明确共享的内容。

Go 模块内部先按职责拆文件、保留同一 package。`module.go` 聚焦构造、Manifest、迁移声明与注册，业务操作、HTTP 入口、Job、事件和 AI Tool 按实际需要分文件。SQL 和迁移随业务归档，不建立横跨全部业务的 handlers、services 或 models 目录。

## 依赖边界

```text
cmd/workbench → app（配置还使用 auth.Mode）
app → contracts + foundation + capabilities + modules + webui
modules/<id> → contracts + 自身代码/sqlc + foundation/httpapi、identity
capabilities → contracts + foundation；部分能力相互组合
foundation → contracts + 自身基础设施
contracts → 标准库
```

业务通过构造函数接收数据库连接、事件发布器、通知服务和文本生成等依赖，不直接依赖其他业务或通用能力实现。能力层可以组合能力，例如 conversation 使用 ai Gateway。底座不依赖业务，模块不读写其他业务的表。

前端业务目录使用共享组件、HTTP 客户端和平台描述类型，通过 `*.module.ts` 提供页面、Widget、图标和可选设置。Shell 根据平台返回的标识映射本地注册项，不包含具体业务页面实现。模块之间不直接导入彼此实现。

[架构测试](../internal/modules/architecture_test.go) 当前检查后端业务跨模块导入和直接依赖能力/app 实现。SQL 表归属及前端导入边界仍需审查；共享数据库连接没有提供模块级权限隔离。

## 装配与启停

[App.New](../internal/app/app.go) 打开并锁定数据库、执行平台迁移，创建通用服务与业务模块。Registry 校验模块声明、执行模块迁移、收集注册资源、同步模块元数据并加载启停状态；随后装配通知 SSE、事件投递、调度、鉴权、AI、对话和 HTTP 路由，加载持久化设置。

注册目录先在内存收集并校验，失败时不发布可用目录。模块迁移在注册目录构建前执行，不与全部注册操作组成一个可回滚事务。

`App.Run` 绑定监听地址，启动调度器、事件投递和 HTTP 服务。退出时停止接收请求、关闭投资 WebSocket 和调度，最后关闭数据库并释放锁。

模块代码随程序构建。运行时启停会持久化到数据库，并影响后续模块 HTTP、Job、事件消费者、AI Tool、导航和总览卡片。停用不会删除表、历史数据或布局偏好，也不强制取消已开始的处理。隐藏 Widget 只影响总览展示。

## 关键协作流程

- 业务写入与领域事件使用同一数据库事务。Dispatcher 为消费者生成投递记录、领取租约并执行，失败按策略重试，超过次数记为 `dead`；业务消费者承担幂等处理。
- Job 的定义来自代码，数据库保存调度状态和运行记录。支持 cron、interval、once，运行前检查模块状态；恢复时依据 misfire 配置跳过或补跑一次。
- 通知服务统一保存通知及变更事件。SSE 将变更告知浏览器，浏览器再查询通知数据；它不提供完整事件历史回放。
- 对话使用 AI Gateway，可调用启用模块注册的 Tool；业务主动生成文本使用 `contracts.TextGenerator`，该接口不执行业务工具。两者共享设置中的 Provider，保存设置后新请求使用新配置。
- Dashboard 合并模块 Widget 声明和持久化布局，业务 Widget 自己读取数据。平台设置和业务设置分别由 `features/settings` 与模块的设置组件负责。

这些边界描述当前实现。需求变化可以调整架构、契约和模块划分，调整时同时维护代码、迁移、测试和对应文档。
