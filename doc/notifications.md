# 外部通知

明确标记为外发的站内通知可通过 Telegram、iOS APNs 和 Android FCM 投递。各通道使用独立事件消费者；站内通知写入不等待外部网络。

## Telegram

管理员在「设置 → 通知推送」（`/settings?tab=notifications`）管理共享 Telegram 机器人，在「用户管理 → 通知目标」为用户分配接收目标。用户只能启停和测试自己的推送；不能读取 Bot Token 或修改其他用户的目标。待办到期、价格预警和资讯简报等外发通知只发送到收件人自己的目标。

1. 在 Telegram 向 [@BotFather](https://t.me/BotFather) 创建机器人，获取 Bot Token。
2. 私聊请先向机器人发送 `/start`；群组请添加机器人，频道需要发布权限。填写目标的数字 Chat ID（群组可为负数）或公开频道 `@用户名`。数字 ID 可从自己机器人的 [getUpdates 响应](https://core.telegram.org/bots/api#getupdates) 中读取 `message.chat.id`。
3. 管理员保存并启用 Bot Token，为用户保存 Chat ID。用户在个人推送中启用接收并点击「发送测试消息」确认。个人测试只使用已保存的本人目标，测试消息会真实发送。

服务端通过 HTTPS 调用 [sendMessage](https://core.telegram.org/bots/api#sendmessage)，无需接收消息的 webhook。部署环境需能访问 `api.telegram.org`。Token 不在读取接口中回显，留空保留，可显式清除；它与其他凭据一样保存在私有工作空间数据库及备份中，数据库本身未加密。

## Telegram 投递行为

- 外发通知先写入站内通知，再由 `core.notification.telegram` 消费者独立投递。复用事件表和租约恢复，外部超时不会阻塞 SSE 消费者。
- 启用或更换本人接收目标后，只发送此后产生的新通知。更换 Bot Token 会提升机器人代次，旧目标需要管理员重新分配，不会自动发送给新机器人。停用服务、个人接收、账号或来源模块时跳过投递，历史收件箱不补发。
- 通知可设置明确的投递截止时间；未设置时在创建后 15 分钟内有效。超过截止时间的投递终止为失败，站内通知仍保留。
- 每个机器人串行发送，间隔至少 3.1 秒。网络/服务错误使用持久化指数退避，最多 12 次尝试；429 尊重 `retry_after`，无效凭据或权限等永久错误直接停止。不同机器人的候选配置测试相互隔离。
- 设置页显示最近尝试、最近成功、脱敏错误，以及自当前目标启用以来的终止失败数量。失败消息不提供手工重放，调整配置用于后续新通知。
- 采用至少一次投递：Telegram 已接收而本地事件尚未确认时若进程崩溃，恢复后仍可能重复。辅助状态保存失败不会主动重发已确认送达的消息。
- 消息使用纯文本，最长 4096 个字符；内容过长时截断正文并保留工作台链接。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/settings/telegram` | 管理员读取服务开关、密钥存在状态及本人投递状态 |
| PUT | `/api/settings/telegram` | 管理员保存 `{ enabled, botToken, clearBotToken }`；兼容 `chatId` 只设置管理员本人的目标 |
| POST | `/api/settings/telegram/test` | 管理员测试候选配置，不保存 |
| GET / PUT | `/api/settings/telegram/recipient` | 读取本人目标和状态 / 保存 `{ enabled }`；普通用户不能提交 Chat ID |
| POST | `/api/settings/telegram/recipient/test` | 测试已保存的本人目标 |
| GET / PUT | `/api/settings/telegram/recipients/{userID}` | 管理员读取 / 分配 `{ enabled, chatId }` |

接口使用平台鉴权、来源检查和 CSRF 保护。通道实现在 `internal/capabilities/notifications/telegram*.go`，前端在 `web/src/features/settings/TelegramPanel.tsx`。测试使用本地传输替身，不发送真实消息。

## 移动推送

移动推送由部署环境启用，凭据配置见[配置与运行](configuration.md#启动参数和环境变量)。iOS 使用 APNs token authentication `.p8` 私钥；Android 使用 Firebase service-account JSON。服务端签发短期 APNs JWT 和 Google OAuth access token，凭据文件不写入数据库或备份。

移动端取得登录 access token 后，将操作系统返回的推送 token 注册到当前设备会话。`pushToken` 是 APNs/FCM 设备 token，不是 Workbench 登录 access token 或 refresh token。

```http
POST /api/mobile/push-token
Authorization: Bearer <Workbench access token>
Content-Type: application/json

{"platform":"ios","pushToken":"<APNs device token>"}
```

Android 使用 `platform: "android"`。系统仅从已验证的 Bearer token 取得设备会话 ID；请求体不接受会话 ID。浏览器 Cookie 会话不能注册或删除移动 token。

| 方法 | 路径 | 用途 |
|---|---|---|
| POST | `/api/mobile/push-token` | 注册当前设备的推送 token |
| PUT | `/api/mobile/push-token` | 更新当前设备的推送 token |
| DELETE | `/api/mobile/push-token` | 删除当前设备与请求中 token 的绑定 |

注册和删除请求体均为 `{ platform, pushToken }`。注册 token 即表示当前设备选择接收移动通知，删除 token 即表示退出；当前没有额外的设备偏好开关。同一设备会话每个平台保留一个 token，操作系统刷新 token 时使用 PUT 更新。注销、撤销或过期的设备会话及禁用账号不再参与投递；设备归属必须与通知收件人一致。

- 以数据库毫秒时间戳为准，通知创建时间须不早于服务启用时间和当前 token 的注册时间，历史事件回填不触发补发。首次注册、更换 token、将 token 转移到另一设备会话时，均使用本次注册时间；相同设备会话重复注册相同 token 保留原时间。
- 服务启用状态及启用时间保存在 `workspace_settings` 的 `mobile_push` 配置项。启动时首次启用，或从已记录的停用状态重新启用，会更新启用时间；保持启用的普通重启不改变时间边界，未过期的待重试通知继续投递。停用状态在以停用配置启动时记录。
- APNs 返回 `BadDeviceToken`、`DeviceTokenNotForTopic`、`Unregistered`，或 FCM 返回 `UNREGISTERED` 时，只删除与此次发送的记录 ID 和 token 同时匹配的绑定，不删除在途请求期间注册的替代 token。

移动消费者 `core.notification.mobile_push` 只发送 `External=true` 且来源业务仍启用的通知。通知未设置截止时间时投递窗口为创建后 15 分钟。网络错误、429 和 5xx 使用事件投递的持久重试，最多 12 次并尊重 `Retry-After`；凭据、权限或消息格式被服务商拒绝时终止重试。投递采用至少一次语义，服务商已接收而本地尚未确认时进程退出，恢复后可能重复。

APNs provider JWT 在 50 分钟内复用，过期被拒后立即重新签发；FCM OAuth access token 在到期前刷新，收到 401 后清除并在下一次重试重新获取。替换部署凭据文件后需重启 Workbench 载入新私钥。
