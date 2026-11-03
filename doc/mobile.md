# 移动端接入

移动客户端使用 Workbench 的同一 HTTPS 域名和平台 API。接口定义见 [OpenAPI](mobile.openapi.yaml)，认证边界见[安全](security.md)，代理部署见[部署与发布](deployment.md)。

## 基础约定

| 项目 | 约定 |
|---|---|
| Base URL | `https://workbench.example.com`，不使用子路径部署 |
| 请求格式 | JSON 请求使用 `Content-Type: application/json` |
| 原生认证 | `Authorization: Bearer <accessToken>` |
| 时间 | RFC 3339 UTC；令牌响应的 `expiresIn` 为秒 |
| 错误 | JSON `{ "code": string, "message": string }` |
| 请求标识 | 可发送 `X-Request-ID`；响应标识由代理和服务日志链路使用 |

原生登录与刷新请求必须是无 Cookie、无 `Authorization` 的 JSON 请求。其他 API 每次只提交一种凭据；Bearer 与 Cookie 同时出现会返回 `400 ambiguous_credentials`。浏览器仍使用 HttpOnly Session Cookie 和 CSRF token，不把网页认证流程复用于原生网络层。`POST /api/auth/login` 不接受 Authorization，误用时返回 `400 invalid_token_request`。

## 登录与令牌

### 登录

```http
POST /api/auth/token
Content-Type: application/json

{
  "password": "用户密码",
  "deviceName": "Alice 的 iPhone",
  "platform": "ios"
}
```

`platform` 取 `ios`、`android` 或 `other`。成功响应包含：

- `accessToken`：EdDSA 签名的短期访问令牌。
- `refreshToken`：一次性刷新令牌。
- `tokenType`：`Bearer`。
- `expiresIn`：访问令牌剩余秒数，通常为 900。
- `refreshExpiresAt`：设备会话的绝对到期时间。
- `sessionId`：设备会话标识。
- `mustChangePassword`：首次登录是否必须改密。

访问令牌最长 15 分钟，设备会话从登录起最长 30 天，不因刷新延长。刷新令牌每次成功使用后立即轮换；再次使用旧刷新令牌会撤销整个设备会话，已签发的访问令牌也随之失效。

access token 使用 EdDSA，header 包含 `typ=at+jwt` 与 `kid`；服务端校验 `iss=workbench`、`aud=workbench-api`、`sub=owner`，并用 `sid` 关联设备会话、用 `ver` 关联密码凭据版本。客户端把 token 当作 opaque credential，不依赖解码后的字段决定权限或有效期。

### 刷新

```http
POST /api/auth/token/refresh
Content-Type: application/json

{ "refreshToken": "保存的刷新令牌" }
```

客户端网络层只允许一个刷新请求在途：所有遇到 401 的请求等待同一刷新结果，成功后读取同一组新令牌。刷新令牌替换应原子写入安全存储；不能让两个并发刷新使用同一旧值。

刷新成功后，只自动重放 GET、HEAD 等幂等读取。创建、更新、删除、交易命令和其他非幂等请求不能无条件重放；由调用方依据业务幂等键或重新读取服务端状态决定。

### 本地保存

- iOS 使用 Keychain，Android 使用 Keystore 支持的加密存储；不写入普通偏好、日志、崩溃信息、剪贴板或 URL。
- access token 可仅保存在内存；refresh token 和 `sessionId` 存入平台安全存储。
- 注销时先调用 `POST /api/auth/token/revoke`，再清除本地令牌。401 时同样清除本地令牌并进入登录页。
- TLS 证书错误直接失败，不降级到 HTTP，也不关闭系统证书校验。

## 状态码

| 状态 | 客户端处理 |
|---|---|
| 400 | 请求格式、Host、CSRF 或凭据混用错误；修正请求，不刷新令牌 |
| 401 | access/refresh token 无效、过期或设备已撤销；access token 请求可尝试一次刷新，是否重放仍遵守幂等规则；刷新接口 401 直接重新登录 |
| 403 | `password_change_required` 时只开放改密流程；`invalid_origin` 表示部署来源不匹配 |
| 429 | 读取 `Retry-After`，到期后再试；不并发撞限 |
| 503 | 业务停用、启动中或上游能力不可用；保留用户操作并显示服务端消息 |

首次登录返回 `mustChangePassword=true` 时，调用 `PUT /api/settings/password`，也可调用 `POST /api/auth/token/revoke` 退出当前设备。其他业务接口仍不可访问。改密成功会撤销全部网页和设备会话，响应为 `{ "reauthenticate": true }`。

## AI 历史

- `GET /api/conversations?limit=100` 按更新时间倒序列出对话，使用响应中的 `nextCursor` 继续读取；网页选择器通过“更多对话”加载后续页。
- `GET /api/conversations/{id}/messages?limit=50` 先读取最近消息，每页按时间正序返回。将 `nextCursor` 传给下一次请求可读取更早一页，拼接时把该页放在已有消息前；网页通过“更早消息”逐页加载。
- 分页失败保留已显示的历史，重试同一 cursor。发送新消息后保留已有内容，不因加载旧消息丢失当前回复。

## 待办写入与重试

创建待办可发送 `Idempotency-Key`。键为 1–128 个可打印 ASCII 字符；一个逻辑创建动作生成一个键，并在超时、断网等结果不确定的重试中保持不变。

- 同一键与同一已解码请求重复提交，返回首次保存的 Task 和 201，不重复创建任务或事件。任务后来被修改或删除也返回首次结果。
- 同一键用于不同的已解码请求，返回 `409 idempotency_conflict`。
- 请求哈希基于 JSON 解码后的创建结构，不基于 HTTP 原始字节；JSON 空白和对象成员顺序不构成新请求。
- 不提供幂等键的 POST 不自动重放。

Task 的 `revision` 从 1 开始，每次 PATCH 递增。更新时提交最近一次读取的 `revision`；不匹配返回 `409 todo_conflict`，客户端重新读取列表、合并用户修改后再提交。`revision` 为可选字段，旧客户端可以省略；此时以服务端处理请求时读到的版本更新，不能发现客户端读取 Task 后已经完成的修改。PATCH 成功后若响应丢失，不能直接重复旧 revision 请求；先读取当前 Task 判断修改是否已经生效。

## 设备管理

- `GET /api/auth/sessions` 同时列出网页与原生设备会话。
- `DELETE /api/auth/sessions/{id}` 注销指定会话。
- `POST /api/auth/logout-all` 注销全部网页与原生会话。
- `POST /api/auth/signing-key/rotate` 轮换访问令牌签名密钥；旧密钥继续验签 15 分钟，使已签发的短期令牌自然到期。

设备撤销、全部注销、改密和 refresh token 重用都会使相应 Bearer 失效。SSE 与 WebSocket 在握手后每秒检查权威登录状态，发现失效后关闭连接。

## 长连接

原生 HTTP 客户端可在 SSE 请求和 WebSocket Upgrade 握手中发送 `Authorization: Bearer ...`。连接断开后：

1. 刷新令牌状态并重新建立连接。
2. 通知 SSE 重新查询通知列表和未读数；服务端不按 `Last-Event-ID` 补发历史。
3. 行情 WebSocket 重新读取当前业务状态，并通过对应 HTTP command 接口恢复订阅；不能假定旧订阅仍存在。

反向代理必须转发 `Authorization`、WebSocket Upgrade，并关闭 SSE 缓冲。Bearer 只用于握手，请求 URL 和 WebSocket 子协议不得携带令牌。

## 通知与推送

原生设备使用 Bearer 调用 `POST` 或 `PUT /api/mobile/push-token` 注册 APNs/FCM token，调用 `DELETE` 删除；成功均返回 204。推送 token 与当前设备会话绑定，网页 Cookie 不能注册移动推送。

APNs 和 FCM payload 均包含 `notificationId` 与 `actionRoute`。客户端把 `actionRoute` 当作 Workbench 站内路径映射到原生页面：

- 只接受以 `/` 开头的已知路由，不作为任意外部 URL 打开。
- 未识别或空路由进入通知列表，并用 `notificationId` 读取最新状态。
- 打开前重新查询业务数据；推送内容只用于展示，不作为最终业务状态。

服务端 APNs/FCM 配置见[配置与运行](configuration.md)。

## Schwab 授权

1. 携带 Bearer 调用 `POST /api/modules/investment/schwab/oauth/start`。
2. 使用系统浏览器打开响应中的 `authorizationUrl`。URL 只含 Schwab OAuth 参数和一次性 state，不含 App access token。
3. Schwab 返回公开的 `/oauth/schwab`，由 Workbench 服务端校验 state、交换并保存券商令牌；系统浏览器无需 App Bearer。
4. App 回到前台后，携带 Bearer 查询 `GET /api/modules/investment/schwab/oauth/status/{statusId}`，直到 `succeeded` 或 `failed`。

状态有效期为十分钟，新授权会使旧的 pending 请求失败。状态暂存在进程内；服务重启或状态不存在时重新发起。

## WebView

`/investment/terminal` 及其子资源使用网页 Cookie 与 CSRF。原生客户端通过一次性换票建立与当前设备绑定的 WebView Session：

1. 使用 Bearer 调用 `POST /api/auth/browser-grant`，正文为 `{}`。响应的 `urlPath` 固定指向同域 `/api/auth/browser-session?ticket=...`，有效期 60 秒。
2. 立即在使用独立 Cookie store 的 WebView 中打开 `baseURL + urlPath`，该 GET 不发送 Authorization。已有同域 Cookie 可以随请求发送。
3. 服务端消费 ticket、写入 HttpOnly Session Cookie，并以 303 固定跳转 `/investment/terminal`。终端后续资源、HTTP command 和 WebSocket 使用该 Cookie；写请求由终端现有逻辑读取 CSRF token。

ticket 是短时登录凭据，只保存在内存，不进入应用日志、分析事件或外部浏览器。它在数据库中仅保存 SHA-256 哈希，只能消费一次；access token 和 refresh token 不进入 URL。WebView 到达固定终端地址后，原生容器应清理含 ticket 的历史导航项，避免回退时重新暴露或提交已消费凭据。

换票前会检查父设备未撤销、未过期、凭据版本有效且无需强制改密。父设备注销、30 天期限到达、改密或全部注销会使绑定的 WebView Session 失效；已建立的 SSE/WebSocket 通过每秒登录检查关闭。`GET /api/auth/sessions` 只列出父设备仍有效的 WebView 会话。

## 验收

接入验收覆盖登录、单次刷新、并发 401 合并刷新、旧 refresh 重用后的设备撤销、改单设备/全部注销、改密、待办幂等创建与 revision 冲突、SSE 重连、WebSocket 重订阅、推送 token 更新与删除、Schwab 系统浏览器往返。
