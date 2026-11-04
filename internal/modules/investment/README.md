# 投资模块代码导航

投资业务继续编入 Workbench 主程序。Go 代码保留在 `investment` package 内，按职责分文件；迁移、查询和 sqlc 生成代码由本模块拥有。

| 职责 | 文件 |
|---|---|
| 装配、注册、模块启停与退出 | `module.go` |
| 用户连接、权限、后台用户枚举与访问撤销 | `users.go` |
| Schwab 配置与持久化 | `schwab.go` |
| OAuth 页面与回调、令牌刷新、REST 代理 | `schwab_oauth.go`、`schwab_auth.go`、`schwab_proxy.go` |
| Schwab 实时连接、凭据读取、浏览器入口 | `streamer.go`、`streamer_credentials.go`、`streamer_http.go` |
| 后台价格采集、交易日历、规则与历史 API | `monitor.go`、`monitor_quotes.go`、`monitor_http.go` |
| Futu 配置与地址校验、行情 HTTP 入口 | `futu.go`、`futu_http.go` |
| Futu 连接与推送、订阅租约、历史查询与缓存 | `futu_gateway.go`、`futu_subscriptions.go`、`futu_history.go` |
| OpenD 传输协议、行情请求与解码 | `futu_opend.go`、`futu_quotes.go` |
| OpenD 安装、进程与登录配置 | `futu_install.go`、`futu_service.go`、`futu_process_*.go`、`futu_login.go` |
| 夜盘开关、终端资源与图表库代理 | `overnight.go`、`chart.go`、`chart_cache.go`、`chart_warm.go` |
| 当前 TV 详情面板夜盘字段兼容修复 | `chart_compat.go`、`chart/tv-quotes-compat.json` |
| 工作台自选表持久化、编辑版本检查 | `watchlists.go` |
| 模块内共享的路径、时间及随机值辅助函数 | `helpers.go` |

根 `Module` 持有共享富途网关、图表代理及用户连接映射；每个用户的子 `Module` 独立持有 Schwab `tokenMu`、OAuth 状态查询、Streamer、交易日历及总览缓存。HTTP 和 AI 工具入口从可信 `contracts.Principal` 选择连接；个人业务缺少身份时拒绝访问。后台任务通过 `UserDirectory` 枚举有效用户，最多同时处理四个用户。`RevokeUser` 关闭该用户的 Schwab 与富途下游连接并取消资产采集，不重置共享 OpenD。

## 数据与权限

| 数据 | 归属 |
|---|---|
| `investment_schwab_app` | 管理员维护共享 App Key、Secret、回调地址和配置版本 |
| `investment_schwab_connections` | `user_id` 独立令牌、授权时间、OAuth state、连接错误和 Streamer 信息 |
| 自选表、投资偏好、监控规则、触发历史、监控状态 | `user_id` 隔离；修改和分页同时限定归属 |
| 净值及授权账户范围 | 用户、账户及日期/授权时间联合主键 |
| `investment_trade_audit` | 记录用户、账户、接口、结果及时间，不保存订单正文或令牌 |
| 富途配置、上游行情池及 K 线缓存 | 全局共享；配置读写仅管理员可用 |

`GET /schwab` 返回本人连接状态及 `configured`。普通用户的 `appKey`、`callbackUrl` 为空，`hasAppSecret` 为 false；管理员可读应用配置。`PUT /schwab` 只接受管理员，配置变化递增版本，使所有旧连接和未完成 OAuth state 失效。断开、授权和刷新只操作本人连接。公开 OAuth 回调由持久化的一次性 state 确定用户，与回调 Cookie 无关；状态查询只对发起用户开放。

交易代理按前端实际接口及 HTTP 方法放行；账户路径必须属于调用者令牌返回的账户集合。写操作先记录审计，发送后记录 HTTP 结果或不确定状态，未经重放。管理员也不能借用其他用户的令牌。

`GET /futu`、富途配置、服务断开及 `PUT /overnight` 仅管理员可用。普通用户通过 `GET /overnight` 和总览读取安全运行状态。富途租约同时绑定用户、登录会话、WebSocket owner 和连接代数；推送只发送给实际订阅对应标的和周期的连接。订阅上限为每连接 64、每用户 128、共享上游租约 1024。富途继续只补夜盘，不替代个人 Schwab 主行情。

迁移 `00014_users.sql` 将旧个人数据和 Schwab 授权归入 `owner`，清除旧 OAuth state；新用户无个人数据。升级前备份；该迁移拒绝降级，回到单用户版本需恢复升级前备份。

价格扫描由 `scanMu` 串行化，行情 HTTP 不持有业务写锁；提交前取得 `monitorMu`、`tokenMu`，检查启停、规则版本及当前授权。规则 CRUD 与生命周期也通过 `monitorMu` 串行。每日触发记录与通知事件共用事务，使用平台 `NotificationService`，不直接发送 TG。

终端位于 `chart/`，与外层 React 投资页分开，由 Go embed 提供同源资源：

| 文件 | 职责 |
|---|---|
| `datafeed.js` | TradingView 回调、缓存、连接代数、订阅与清理 |
| `datafeed-symbols.js` | 标的类型识别和交易时段定义 |
| `datafeed-bars.js` | 历史请求、K 线规范化、时段合并和周期时间对齐 |
| `datafeed-realtime.js` | Schwab 字段转换与实时 K 线聚合 |
| `datafeed-quotes.js` | TV 报价字段规范化、Schwab 优先与富途夜盘补充、时段报价口径 |
| `broker.js`、`broker-models.js` | 账户和交易适配、订单与持仓模型 |
| `schwab.js`、`stream.js`、`futu.js`、`websocket.js` | 请求客户端与行情连接 |
| `index.html`、`loading.js`、`theme.js` | 终端装配、加载反馈与主题 |
| `watchlists.js` | 自选表恢复、自动保存、待提交草稿与重试 |

行情转换函数接收明确的输入和缓存，连接代数校验由 `datafeed.js` 传入历史请求函数。新增终端 JS 放在 `chart/` 同层，可被当前嵌入规则、lint 和浏览器测试路由直接发现。

相关行为测试位于本目录的 `*_test.go` 和 `chart-tests/`。从仓库根目录运行 `go test -race ./internal/modules/investment`、`npm --prefix web run test:investment` 和 `npm --prefix web run lint:investment`；浏览器验证方式见 [scripts/README](../../../scripts/README.md#投资账户管理器)，功能与接口见 [Investment 文档](../../../doc/modules/investment.md)。
