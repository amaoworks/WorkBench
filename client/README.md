# 移动客户端

iOS 采用 SwiftUI，Android 采用 Jetpack Compose。两端复用网页的 Go 后端、账号和业务数据，使用相同的 HTTPS 入口。总览、待办、通知等用原生界面实现，投资图表可通过系统 WebView 复用现有 TradingView 终端。

## 技术路线

| 路线 | 适用性 | 开发成本 |
|---|---|---|
| [iOS 原生](ios/README.md)和 [Android 原生](android/README.md) | 推荐。适合长列表、手势、系统可达性、通知和安全存储 | 两套界面和测试，共享 API 契约和视觉规范 |
| React Native 或 Flutter | 适合以共用界面代码为优先目标 | 现有 React DOM 页面仍需重写，推送、安全存储和图表容器仍需验证平台集成 |
| WebView 封装网页 | 可快速复用现有页面，适合保留图表终端 | 整体体验受网页导航和渲染路径约束 |

沿用网页的页面名称、卡片层级、待办分组、操作文案，以及「宝蓝」「陶橙」主题配色。首版将总览、待办、通知放在底部标签栏，个人设置从总览头像进入。手机总览使用单列卡片，支持系统字号和深色模式。视觉参考 [前端界面规范](../doc/frontend-guidelines.md)和 [主题变量](../web/src/styles.css)。

## 后端接入

若网页通过 `https://workbench.example.com` 访问，App 的服务器地址也填 `https://workbench.example.com`，登录请求发到同一域名的 `/api/auth/token`。服务器地址填写 `https://`、域名和必要的端口，接口路径由 App 拼接。

| 条件 | 核对方式 |
|---|---|
| 手机可以访问现有 HTTPS 入口，系统信任证书 | 在手机打开同域 `/health/ready`，应返回 `200` 和 `{"status":"ready"}`。`127.0.0.1` 在真机上指向手机自身。 |
| 实例启用 password 模式，版本包含设备登录接口 | 用同一个网页账号请求 `POST /api/auth/token`。默认 local 模式会返回 `password_mode_required`。 |
| 反向代理把同域 `/api/*` 转发给 Workbench | `WORKBENCH_PUBLIC_URL` 与实际 HTTPS 入口一致，代理保留原始 Host 并转发 Authorization。接入行情和前台通知流时再验证 WebSocket Upgrade 与 SSE 即时转发。 |

登录和刷新使用无 Cookie 的网络会话，凭据仅通过 JSON 正文提交。业务请求使用 `Authorization: Bearer <accessToken>`。首次登录要求改密时进入改密页，成功后重新登录。网络层合并并发刷新，原子替换一次性 refresh token，自动重发限于 GET 等幂等读取。缓存至少按服务器地址和用户隔离，切换账号或退出时清除当前页面的数据。详见 [移动端接入](../doc/mobile.md)、[移动端 OpenAPI](../doc/mobile.openapi.yaml)和 [移动端代理配置](../doc/deployment.md#移动端入口)。

## 实施顺序

| 阶段 | 移动端功能 | 现有接口 |
|---|---|---|
| 设备接入 | 服务器地址、登录、改密、刷新、退出 | `/health/ready`、`/api/auth/token`、`/api/auth/token/refresh`、`/api/auth/token/revoke`、`/api/settings/password` |
| 首版功能 | 总览、待办增删改查、通知列表与未读状态、个人外观设置 | `/api/modules`、`/api/dashboard?include=data`、`/api/modules/todo/tasks`、`/api/notifications`、`/api/settings`、`/api/settings/appearance` |
| 后续补齐 | 前台实时通知、系统推送、资讯、AI 对话、投资 | 通知 SSE、APNs/FCM 注册、资讯 API、流式聊天与历史、投资 API。后台推送需要配置 APNs 或 FCM。 |

首版验收使用同一实例和同一账号，在网页和 App 之间交叉创建、编辑、完成待办并刷新确认。并发请求遇到 401 时只发起一次刷新。创建待办的重试沿用 `Idempotency-Key`，编辑提交 `revision`，遇到冲突时先读回服务器数据。总览用聚合接口减少首屏请求，按已启用业务和客户端支持的卡片类型渲染。长列表使用分页和原生惰性渲染。用真机记录冷启动到总览可交互的耗时，以及滚动长待办列表时的掉帧。

投资先验证系统 WebView 的 TradingView 触屏交互和行情重连。通过 `POST /api/auth/browser-grant` 获取一次性同域换票地址，建立终端 Cookie 会话。Schwab 授权用系统浏览器，回到 App 后查询服务端结果。持仓、订单和价格监控可基于现有 API 逐步实现原生页面。详见 [移动端 WebView 接入](../doc/mobile.md#webview)和 [投资模块](../doc/modules/investment.md)。
