# Todo 待办模块

模块 ID 为 `todo`，页面为 `/todo`。后端在 [internal/modules/todo](../../internal/modules/todo)，前端在 [web/src/modules/todo](../../web/src/modules/todo)。Wallos 是本模块内的订阅提醒来源，详见 [Wallos 联动](todo-wallos.md)。

## 功能和数据

待办支持创建、修改标题/说明/提醒时间/优先级/分类、完成与恢复、删除、分页，以及未完成/逾期/已完成概览。页面提供创建、完成切换、删除和加载更多；完整修改字段由后端 API 提供。页面按 已逾期/今天/本周/以后/无截止时间/已完成 分组，用 `?view=all|today|week|later|done` 切换视图，`?new=1` 会聚焦创建输入框。视图筛选和标签计数由服务端对完整数据集计算，优先级与分类筛选同样作用于服务端分页和计数。Wallos 生成的任务使用 `category = "renewal"`，页面据此显示「续费」标记。

标题去除首尾空白后须为 1–300 字节，说明最多 10000 字节，分类去除首尾空白后最多 40 个字符。优先级为 `none|low|medium|high`，默认 `none`。提醒时间、完成时间和分类可以为空，业务时间以 UTC 保存并通过 JSON 时间字符串返回。

列表先排未完成，再按优先级从高到低、提醒时间、创建时间倒序和 ID 排序；未设置提醒时间的项目位于有时间的项目之后。`limit` 默认 50、最大 100，返回 `nextCursor` 时继续请求下一页。游标版本 2 编码完整排序位置，前端不自行推算。

日期分组按 `tz` 指定的 IANA 时区计算。今天截止于下一个本地午夜；本周截止于下周一零点。边界使用日历日期运算，夏令时切换日仍按当地自然日和自然周划分。页面每 30 秒刷新时间敏感的列表与计数，使截止时间和午夜跨越及时反映到分组。

`todo_tasks` 是主表，Wallos 配置和账期映射另存本模块表。创建、更新和删除与相应 `todo.task.created/updated/completed/deleted` 事件在同一事务提交。

## HTTP 和注册资源

| 方法 | 路径 | 内容 |
|---|---|---|
| GET | `/api/modules/todo/tasks` | `{ items, nextCursor? }`；支持 `limit`、`cursor`、`view`、`tz`、`priority`、`category` |
| GET | `/api/modules/todo/tasks/counts` | 按 `tz` 返回 overdue、today、week、later、none、done 全量计数；支持 priority、category |
| POST | `/api/modules/todo/tasks` | 提交 title、description、dueAt、priority、category，返回创建的 Task |
| PATCH | `/api/modules/todo/tasks/{id}` | 可提交 title、description、dueAt、clearDueAt、completed、priority、category、clearCategory |
| DELETE | `/api/modules/todo/tasks/{id}` | 删除，成功为 204 |
| GET | `/api/modules/todo/widget/summary` | `{ open, overdue, completed }` |
| GET | `/api/modules/todo/widget/today` | 今日到期/逾期任务与续费；`completedToday` 按 `tz` 统计当日完成数 |

任务不存在时，修改和删除返回 404。所有入口受平台鉴权、请求保护和模块开关控制。

| 资源 | 标识和行为 |
|---|---|
| 页面 | `todo.list` |
| Widget | `todo.today`，默认 medium、顺序 20 |
| 到期扫描 | `todo.scan_due`，每分钟扫描；通知以任务 ID 和提醒时间去重 |
| Wallos 同步 | `todo.sync_wallos`，每小时执行，使用已保存联动设置 |
| AI Tool | `todo.create_task`，low_write，支持 dueAt、priority、category并复用普通创建操作 |
| AI Tool | `todo.list_tasks`，read_only，按视图、时区和数量查询任务 |
| AI Tool | `todo.complete_task`，low_write，按明确任务 ID 完成任务并复用更新事务与事件 |

到期扫描按 `(due_at, id)` 稳定分页（每批 100 条）遍历全部未完成到期任务。通知内容由平台统一呈现，点击前往 `/todo`。关闭 Wallos 联动后已生成的待办仍参与正常提醒；停用整个 Todo 模块会阻止后续模块任务与入口。

## 文件职责

| 文件 | 内容 |
|---|---|
| `module.go` | Module 构造、依赖、Manifest、迁移嵌入和资源注册 |
| `task.go` | Task 类型、创建/更新业务操作、数据库行与时间转换 |
| `http.go` | 创建、修改、删除和概览 HTTP 处理 |
| `pagination.go` | 列表接口、分页参数与游标 |
| `jobs.go` | 到期扫描和通知生成 |
| `tools.go` | AI 创建待办参数适配 |
| `wallos.go`、`wallos_amount.go` | 联动配置、远端读取、同步、描述和金额转换 |
| `migrations/`、`query/`、`sqlc/` | 本业务数据定义和查询 |
| 前端 `schema.ts`、`queries.ts` | Todo/Wallos 校验类型、接口调用、查询和缓存刷新 |
| 前端 `todo.module.ts` | 页面、Widget、模块设置与图标注册 |

行为测试随模块保存；分页、Wallos 同步和设置交互可通过 [浏览器回归脚本](../../scripts/README.md) 验证。
