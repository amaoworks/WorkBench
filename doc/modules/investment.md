# Investment 模拟行情模块

模块 ID 为 `investment`，显示名称为“模拟行情”，页面为 `/investment`。后端在 [internal/modules/investment](../../internal/modules/investment)，前端在 [web/src/modules/investment](../../web/src/modules/investment)。

## 当前行为

App 注入 `MockProvider`，提供 DEMO-A、DEMO-B、DEMO-C 三个虚构品种；价格和涨跌值为模拟数据，时间戳按五分钟窗口截断。当前没有真实行情数据源或交易执行入口。

`investment.sync` 每五分钟自动同步，也可从页面手动同步。读取 Provider 在事务外进行，随后在同一事务中保存更新的行情并发布 `investment.price.updated`。同一品种只有更新的 `asOf` 才会写入并发布，重复同步同一快照不会重复触发。

消费者 `investment.price_alert` 在涨跌达到 ±200 个基点（±2%）时创建站内通知。幂等键以品种和快照时间组成，通知跳转到 `/investment`。

页面支持按需生成 AI 摘要，将当前模拟行情发送给共享 `TextGenerator`，结果保存到 `investment_summary`。AI 不可用返回 503，行情浏览和同步仍可用；没有行情时摘要接口返回 409。请求限制为 45 秒，提示词要求描述给定模拟数值，文本生成接口不执行业务工具。

## 接口与资源

| 方法 | 路径 | 内容 |
|---|---|---|
| GET | `/api/modules/investment/overview` | source、items 和可为空的 summary |
| POST | `/api/modules/investment/sync` | 请求体 `{}`，同步后返回 overview |
| POST | `/api/modules/investment/summary` | 请求体 `{}`，返回 content、createdAt |

价格为整数分 `priceCents`，涨跌为基点 `changeBps`。页面和卡片每 60 秒刷新查询，手动操作成功时主动刷新相关缓存。

导航 pageKey 和 Widget kind 均为 `investment.overview`；卡片默认 medium、顺序 20。停用模块保留行情、摘要和总览偏好，重新启用后恢复展示。

## 文件和依赖

`module.go` 声明 `Dependencies`、构造、迁移、路由、Job、Consumer 和 Widget。`quotes.go` 管理同步、概览类型和读取，`events.go` 处理价格事件提醒，`http.go` 提供 HTTP 入口及摘要流程，`provider.go` 定义 QuoteProvider 与当前模拟实现。

依赖由 App 注入：共享数据库、EventPublisher、NotificationService、TextGenerator 和 QuoteProvider。模块不直接依赖 AI Provider、通知实现或其他业务。

业务表为 `investment_quotes` 和 `investment_summary`，SQL 与迁移留在模块内。前端 `schema.ts` 定义响应校验，`queries.ts` 提供接口函数和查询 hooks；页面及 Widget 通过 `investment.module.ts` 注册。

现有测试覆盖事务事件、快照幂等、通知去重和 AI 调用；App 集成测试覆盖实时 AI 设置、启停、重启后的数据保留。浏览器回归方法见 [scripts/README](../../scripts/README.md)。
