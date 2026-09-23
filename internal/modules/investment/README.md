# 投资模块代码导航

投资业务继续编入 Workbench 主程序。Go 代码保留在 `investment` package 内，按职责分文件；迁移、查询和 sqlc 生成代码由本模块拥有。

| 职责 | 文件 |
|---|---|
| 装配、注册、模块启停与退出 | `module.go` |
| Schwab 配置与持久化 | `schwab.go` |
| OAuth 页面与回调、令牌刷新、REST 代理 | `schwab_oauth.go`、`schwab_auth.go`、`schwab_proxy.go` |
| Schwab 实时连接、凭据读取、浏览器入口 | `streamer.go`、`streamer_credentials.go`、`streamer_http.go` |
| 后台价格采集、交易日历、规则与历史 API | `monitor.go`、`monitor_quotes.go`、`monitor_http.go` |
| Futu 配置与地址校验、行情 HTTP 入口 | `futu.go`、`futu_http.go` |
| Futu 连接与推送、订阅租约、历史查询与缓存 | `futu_gateway.go`、`futu_subscriptions.go`、`futu_history.go` |
| OpenD 传输协议、行情请求与解码 | `futu_opend.go`、`futu_quotes.go` |
| OpenD 安装、进程与登录配置 | `futu_install.go`、`futu_service.go`、`futu_process_*.go`、`futu_login.go` |
| 夜盘开关、终端资源与图表库代理 | `overnight.go`、`chart.go`、`chart_cache.go`、`chart_warm.go` |
| 工作台自选表持久化、编辑版本检查 | `watchlists.go` |
| 模块内共享的路径、时间及随机值辅助函数 | `helpers.go` |

`Module` 统一装配并持有连接资源。拆文件不改变锁的归属：Schwab 凭据仍由 `tokenMu` 串行保护，连接建立和配置替换仍使用各网关的 `connectMu`，连接集合与代数由网关自身的 `mu` 保护。修改这些路径时，应一起检查模块停用、令牌更新、迟到响应和退出清理。

价格扫描由 `scanMu` 串行化，行情 HTTP 不持有业务写锁；提交前取得 `monitorMu`、`tokenMu`，检查启停、规则版本及当前授权。规则 CRUD 与生命周期也通过 `monitorMu` 串行。每日触发记录与通知事件共用事务，使用平台 `NotificationService`，不直接发送 TG。

终端位于 `chart/`，与外层 React 投资页分开，由 Go embed 提供同源资源：

| 文件 | 职责 |
|---|---|
| `datafeed.js` | TradingView 回调、缓存、连接代数、订阅与清理 |
| `datafeed-symbols.js` | 标的类型识别和交易时段定义 |
| `datafeed-bars.js` | 历史请求、K 线规范化、时段合并和周期时间对齐 |
| `datafeed-realtime.js` | Schwab 字段转换与实时 K 线聚合 |
| `broker.js`、`broker-models.js` | 账户和交易适配、订单与持仓模型 |
| `schwab.js`、`stream.js`、`futu.js`、`websocket.js` | 请求客户端与行情连接 |
| `index.html`、`loading.js`、`theme.js` | 终端装配、加载反馈与主题 |
| `watchlists.js` | 自选表恢复、自动保存、待提交草稿与重试 |

行情转换函数接收明确的输入和缓存，连接代数校验由 `datafeed.js` 传入历史请求函数。新增终端 JS 放在 `chart/` 同层，可被当前嵌入规则、lint 和浏览器测试路由直接发现。

相关行为测试位于本目录的 `*_test.go` 和 `chart-tests/`。从仓库根目录运行 `go test -race ./internal/modules/investment`、`npm --prefix web run test:investment` 和 `npm --prefix web run lint:investment`；浏览器验证方式见 [scripts/README](../../../scripts/README.md#投资账户管理器)，功能与接口见 [Investment 文档](../../../doc/modules/investment.md)。
