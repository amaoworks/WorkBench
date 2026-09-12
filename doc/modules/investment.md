# Investment 投资

模块 ID 为 `investment`，显示名称为“投资”，页面为 `/investment`。后端在 [internal/modules/investment](../../internal/modules/investment)，前端在 [web/src/modules/investment](../../web/src/modules/investment)。

模块在工作台进程内接入 Charles Schwab Trader API：Go 负责 OAuth、REST 和 Streamer WebSocket，TradingView 终端负责行情、持仓与订单界面。模拟行情、模拟波动提醒及模拟行情 AI 摘要已移除，不再注册对应接口、任务和事件消费者。

## Schwab 连接

在「设置 → 业务模块 → 投资」填写 Developer Portal 的 App Key、App Secret，以及与登记完全一致的 HTTPS 回调地址；路径必须为 `/oauth/schwab`。该 URL 必须能从用户浏览器到达工作台进程，反向代理需实际支持 HTTPS，应用以 HTTP 提供后端并配置对应的 `WORKBENCH_PUBLIC_URL`。部署示例见[部署与发布](../deployment.md)。HTTP 页面不会自动生成可用回调；旧 HTTP 回调也会在发起授权前被拒绝。Secret 读取时不回显。更换 App Key 或回调必须重新填写 Secret；Key、Secret 或回调变化均清除旧令牌和长连接。

保存后点击「登录 Schwab」，并在账户关联确认页点击 Done。公开回调 `/oauth/schwab` 使用有效期十分钟的一次性 state 校验，再由服务端换取并保存令牌。回调独立于工作台会话，因为跨站跳转不携带 `SameSite=Strict` 会话 Cookie。成功页先建立工作台同站点文档，再自动返回 `/investment`，保留手动返回链接；`no-referrer` 避免授权查询参数进入下一跳 Referer。重新授权成功也会关闭旧 Streamer，终端重连后重新读取账户列表。

`investment.schwab_refresh` 每二十分钟检查访问令牌；REST 请求和新连接也会按需刷新。令牌刷新后关闭旧流，浏览器自动重新登录连接。断开操作保留应用配置，清除券商令牌。所有券商访问令牌仅在服务端使用，代理不转发浏览器 Cookie。

## 行情与交易

连接后用 iframe 打开 `/investment/terminal`。终端从 Schwab 获取历史与实时行情，读取所选账户的真实多空持仓；订单查询覆盖最近一年。Schwab 没有订单游标，达到单次 3000 条上限时递归拆分时间范围并按订单 ID 去重；无法完整读取时明确报错，不展示被截断的完整列表。

投资页工具栏提供「页面铺满」「全屏」「新窗口」。页面铺满使用 `/investment?view=terminal`，隐藏工作台导航、页头和外围留白，保留终端工具栏；「返回工作台」恢复普通布局。切换页面铺满或浏览器全屏只改变布局，不重建 iframe，保留当前图表及下单面板状态。「新窗口」在独立标签页打开同一投资页并默认铺满，适合放到副屏；浏览器全屏可以通过工具栏或浏览器退出操作恢复。

账户初始化在 `broker_factory` 返回后的下一个任务中启动，等待 TradingView 完成适配器注册后再通知连接状态。持仓与订单的标的列使用数据中的 `symbol` 字段，分别传给券商标的和图表标的参数，避免渲染异常或将订单 ID 当作图表标的。

账户管理器显示末四位账号、净值、现金和购买力，不把完整账号当成金额。持仓方向用「持有 / 卖空」，避免 TradingView 把普通多头译成「做多」。下单保留两个独立字段：TradingView 原生「有效期」提供DAY(当天有效)、GTC(取消前有效)、FOK(立即全部成交，否则取消) 和 IOC(立即成交，否则取消)，FOK/IOC 仅在限价单中显示；「交易时段」提供常规(9:30-16:00 ET)、盘前(7:00-9:25 ET)、盘后(16:05-20:00 ET)、延长时段(7:00-20:00 ET)，分别映射 `NORMAL`、`AM`、`PM`、`SEAMLESS`。新订单默认当天有效、常规时段。延长时段包含盘前、常规和盘后，不含隔夜；时间范围内仍有常规开盘前、收盘后各五分钟的暂停，参见[嘉信时段说明](https://www.schwab.com/stocks/extended-hours-trading)。

按 Schwab `OrderRequest` 的 session 枚举，不提供或提交 `EXTO`；查询可以保留外部平台已有的隔夜订单，修改此类订单提示回 Schwab 操作。`END_OF_WEEK`、`END_OF_MONTH`、`NEXT_END_OF_MONTH` 暂未确认适用范围，不开放下单；`UNKNOWN` 不作为用户选项。适配器校验数量、价格及有效期/时段组合，盘前、盘后和延长时段仅支持 DAY/GTC 股票限价单。改单先读取原订单，并将原时段回填界面，保留原有效期和买卖指令。当前支持股票和股票期权单腿订单，复杂策略及未支持的订单类型（如跟踪止损）提示回 Schwab 修改，避免转换时丢失原订单语义。未实现的反转持仓与附加止盈止损能力不在界面声明为可用。

下单成功使用响应 Location 中的真实订单 ID；成功写入后的状态读取失败只提示刷新错误，不把已成功的交易改报为下单失败。撤单检查上游 HTTP 状态，并通过 REST 查询确认最终状态，不把“接受撤单”直接当成“已取消”。

下单、改单或撤单遇到网络异常、HTTP 408 或 5xx 时，提示结果未确认并要求核对原账户，不自动重试交易。已经提交的交易继续使用提交时的账户，切换账户不取消该写请求，也不将其回读结果注入新账户。

切换账户会清空缓存、取消旧请求并通知 TradingView 重载。异步响应带账户版本校验，旧账户响应不会写入新账户界面。ACCT_ACTIVITY 仅作为刷新信号，不直接解析事件中的订单和数值：收到事件后重新读取当前账户的订单、成交数量/均价和持仓，并每十五秒补充刷新。刷新用逐行更新，不清空账户表。当前订单只含未完成单，已完成、已撤销和已过期进入历史页，按更新时间新到旧排列。

每次启动、切换账户或重连后的第一份订单数据仅建立快照，通过 `orders()` / `ordersHistory()` 提供给 TradingView，不逐条触发 `orderUpdate`。后续刷新只推送新增或内容变化的订单，避免左下角重复弹出历史成交、撤单通知；新订单、成交及撤单仍正常更新。下单/改单/撤单后的 REST 回读同步更新比较基准，下一次轮询不会重复推送同一结果。

历史 K 线按时间排序、去重并排除查询结束边界；日、周、月线时间与 UTC 周期起点对齐。历史回补不会覆盖更新的实时 K 线，同一品种周期的多个订阅者只计算一次成交量增量，交给图表的数据使用副本，避免图表修改内部缓存。

## 长连接恢复

Go 对一条共享 Streamer 串行登录，收到 LOGIN 与账户订阅确认后才通知浏览器 ready。行情服务首次使用 SUBS，之后使用 ADD；HTTP 成功仅在 Schwab 确认订阅后返回。

上游断开时同时关闭浏览器连接，浏览器三秒后重连。重新 ready 后恢复所有活动品种订阅、清空行情快照，并调用 TradingView 的 `resetCache` 与各图表的 `resetData` 重载历史，同时重新读取账户状态。历史回补即使不是首次请求也会恢复实时 K 线基准；断线前的历史响应和已关闭 WebSocket 的迟到事件会被丢弃。终端显示连接恢复中的状态提示。慢客户端不静默丢弃订单事件，而是断开后重新获取状态。参见 [TradingView 数据恢复约定](https://www.tradingview.com/charting-library-docs/latest/connecting_data/Datafeed-Issues/#internet-connection-issues)。

配置变更使正在登录中的旧连接失效。最后一个浏览器离开、模块停用或进程关闭时清理连接；停用不删除 Schwab 配置、凭据或总览布局。

## TradingView 资源

当前 `/charting_library/` 从 `https://trading-terminal.tradingview-widget.com/charting_library/` 同源反代，保存图纸和新闻等依赖外部服务的功能关闭。行情与交易数据来自 Schwab。

这个演示资源接入不等同于取得库的部署授权。TradingView FAQ 区分了 Widgets、Advanced Charts 和 Trading Platform；自托管交易功能需使用获得授权的 Trading Platform 包。参见 [官方 FAQ](https://www.tradingview.com/charting-library-docs/latest/resources/Frequently-Asked-Questions/)。

## 接口与资源

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/modules/investment/schwab` | 应用配置、是否已保存 Secret/令牌及最近错误 |
| PUT | `/api/modules/investment/schwab` | 保存 `appKey`、`appSecret`、`callbackUrl` |
| POST | `/api/modules/investment/schwab/disconnect` | 清除令牌和连接 |
| GET | `/api/modules/investment/schwab/oauth/login` | 跳转到 Schwab 授权页 |
| GET | `/oauth/schwab` | 公开 OAuth 回调 |
| POST | `/api/modules/investment/schwab/oauth/refresh` | 检查并刷新访问令牌 |
| GET/POST/PUT/DELETE/PATCH | `/api/modules/investment/schwab/trader/v1/*` | Trader REST 代理 |
| GET/POST/PUT/DELETE/PATCH | `/api/modules/investment/schwab/marketdata/v1/*` | Market Data REST 代理 |
| GET | `/api/modules/investment/schwab/trader/ws`、`marketdata/ws` | 浏览器事件连接 |
| POST | `/api/modules/investment/schwab/marketdata/ws/command` | LEVELONE 服务的 ADD 订阅命令 |
| GET | `/investment/terminal`、`/investment/terminal/*` | 终端 HTML 与适配器 |
| GET/HEAD | `/charting_library/*` | 图表库静态文件代理 |

导航 pageKey 和 Widget kind 保留 `investment.overview`，使已有布局兼容。Widget 改为展示 Schwab 连接状态及终端入口，数据路由使用配置状态接口。Vite 同时代理终端资源和 `/api` WebSocket。

## 数据、验证与参考

模块只依赖注入的数据库；配置表为 `investment_schwab`。升级迁移移除历史演示表 `investment_quotes` 和 `investment_summary`，旧迁移文件保留。SQL 和 sqlc 生成代码均在模块目录。

Go 测试覆盖配置、OAuth、代理、连接确认、断线恢复和配置变更竞态；Node 测试直接导入终端 JS，覆盖交易语义、未确认的交易结果、真实持仓、订单时间分段、跨账户异步响应、订阅重放及历史/实时 K 线衔接。`./scripts/test.sh` 执行 sqlc 漂移检查、Go 测试、React/终端 JS lint、Node 测试与生产构建。模拟服务验证不替代真实券商端到端验收。

实际 TradingView 库的浏览器回归使用虚构的 Schwab 响应，覆盖初始化时序、持仓与订单列渲染、标的跳转、账户切换和重连，以及下单面板的有效期/交易时段名称、可选值和已有订单回填；运行方式见[脚本说明](../../scripts/README.md#投资账户管理器)。终端资源嵌入 Go 程序，修改后需重新编译并启动调试进程才能生效。

参考：[原始适配器 demo](https://github.com/invmy/schwab_API-tradingview_adapter)、[Cloudflare 代理](https://github.com/invmy/CF_schwab-API)、[Schwabdev](https://github.com/tylerebowers/Schwabdev)、[TradingView 账户切换契约](https://www.tradingview.com/charting-library-docs/latest/api/interfaces/Charting_Library.IBrokerConnectionAdapterHost/)。
