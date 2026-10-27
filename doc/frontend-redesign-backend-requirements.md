# 前端改版实现

顶部导航、今日概况、待办视图、通知分组、持仓与订单页使用以下接口。业务数据由服务端读取与计算，前端通过生产 Zod schema 校验响应。

## 待办

| 接口 | 参数与行为 |
|---|---|
| `GET /api/modules/todo/tasks` | `view=all|today|week|later|done|overdue`、`tz`、`priority`、`category`、`limit`、`cursor`；先在服务端筛选，再分页 |
| `GET /api/modules/todo/tasks/counts` | 按 `tz`、`priority`、`category` 返回 overdue、today、week、later、none、done 全量计数 |
| `GET /api/modules/todo/widget/today` | 按 `tz` 返回今天及逾期待办、续费和 `completedToday` |
| `POST /api/modules/todo/tasks` | 接受标题、说明、截止时间、`priority` 和 `category` |
| `PATCH /api/modules/todo/tasks/{id}` | 更新任务，支持 `priority`、`category` 和 `clearCategory` |

自然周从周一开始，日界线按所选时区计算。列表按未完成、优先级、截止时间、创建时间和 ID 排序，游标版本为 2。前端每 30 秒刷新分组与计数。

优先级为 none、low、medium、high；分类最长 40 字符。Wallos 同步写入 `category=renewal`，界面使用结构化分类显示续费标记。SQLite 与 PostgreSQL 均通过 `00003_priority_category.sql` 添加字段，并迁移已有续费任务分类。

## 持仓与订单

| 接口 | 参数与行为 |
|---|---|
| `GET /api/modules/investment/positions` | 返回账户汇总和持仓；现价读取行情报价，缺失值保留为 null |
| `GET /api/modules/investment/orders` | `days=1|7|30|90`、`status=all|working|filled|canceled`、`tz`；服务端展开条件子单、去重和筛选 |

响应状态为 ready、unconfigured、reauth、error。账户使用 Schwab 哈希标识，显示标签隐藏完整账号。前端每 30 秒刷新，并提供手动刷新、重试及最近更新时间。

订单每次最多请求 500 条；达到上限时二分时间区间继续读取，最终按账户哈希及订单 ID 去重。无法完整读取时返回错误。组合净成交价按买卖方向与腿比例计算；各腿部分成交比例不一致时保留单腿成交数量和均价，组合净价为空。止损限价单同时显示限价和触发价。

子页面为 `/investment/positions`、`/investment/orders`。后端占用 `/investment/terminal*`，前端子页面不得与其重名。

## 净值历史

`GET /api/modules/investment/networth?range=1m|3m|1y|all&account=` 返回 USD 日快照。前端提供时间范围、账户选择、分段曲线及每日明细。

- 日期按 America/New_York 计算；启动、重新启用及工作日 16:15 采集。
- `investment_networth_snapshots` 以账户哈希和日期为主键，同日覆盖更新；保存净值、现金、持仓市值、当日盈亏和 UTC 采集时间。
- 仅写入字段完整的账户；部分账户缺值时提交其他完整账户并报告缺失。
- `investment_networth_account_scopes` 限定当前授权可见账户，授权切换后保留历史存储。
- 缺少记录的日期保留为空；账户集合变化或缺记录处断开曲线。
- SQLite 与 PostgreSQL 使用成对 `00013_networth_snapshots.sql` 迁移。

## 通知

`GET /api/notifications` 支持 `source=<moduleId>` 与 `unread`、`limit`、`cursor` 组合筛选；`GET /api/notifications/unread-count` 同样接受 source。前端使用服务端分页，零结果时仍可清除来源条件。今天、昨天和更早按浏览器本地日历分组，跨午夜、重新聚焦及恢复可见时更新。

## AI

`/api/chat` 与 `/api/chat/stream` 接受可选 `context: { route, label }`。route 必须为站内相对路径；上下文仅用于本轮模型提示，用户原始消息独立持久化。用户可关闭附带上下文。

| 工具 | 风险 | 行为 |
|---|---|---|
| `todo.create_task` | low_write | 创建任务，支持优先级与分类 |
| `todo.list_tasks` | read_only | 按视图、时区与数量查询 |
| `todo.complete_task` | low_write | 按明确任务 ID 完成任务 |
| `notifications.list` | read_only | 查询通知 |
| `notifications.mark_read` | low_write | 按明确通知 ID 标记已读 |

写工具沿用运行时的严格参数校验、幂等和审计；high_write 工具仍需确认信息。面板与顶部导航按平台显示 Ctrl J 或 ⌘J。

## 总览与导航

通知概况独立于完整通知卡显示。完整通知卡默认隐藏，已保存的显隐偏好继续生效。仅当配置顺序中的第一张卡片是 large 时，将其放在概况旁边，其余卡片保持用户排序。

业务设置弹窗直接由 URL 的 tab、module 参数控制；关闭后清除 module，并保留未保存修改确认。模块加载、启停与失败状态由外壳统一处理。

## 验证

`./scripts/test.sh` 检查 SQL 生成漂移、Go 测试、前端及终端 lint、生产 schema 与真实后端响应契约、Node 逻辑测试及生产构建。

`scripts/frontend-redesign.e2e.cjs` 在临时工作空间运行浏览器回归，覆盖导航键盘与触屏、重复打开设置及草稿确认、通知组合筛选、投资子页面及状态、净值曲线和账户切换，以及桌面/360px、浅深主题和两套配色。券商数据使用虚构上游响应，测试不修改实际工作空间。
