# Telegram 通知

「设置 → 通知推送」（`/settings?tab=notifications`）管理唯一的 Telegram 接收目标。首版仅转发投资价格预警，其他站内通知不自动外发。

1. 在 Telegram 向 [@BotFather](https://t.me/BotFather) 创建机器人，获取 Bot Token。
2. 私聊请先向机器人发送 `/start`；群组请添加机器人，频道需要发布权限。填写目标的数字 Chat ID（群组可为负数）或公开频道 `@用户名`。数字 ID 可从自己机器人的 [getUpdates 响应](https://core.telegram.org/bots/api#getupdates) 中读取 `message.chat.id`。
3. 在设置中填写 Token、Chat ID，点击「发送测试消息」确认接收，再保存并启用。测试使用当前表单，不保存候选配置；测试消息会真实发送。

服务端通过 HTTPS 调用 [sendMessage](https://core.telegram.org/bots/api#sendmessage)，无需接收消息的 webhook。部署环境需能访问 `api.telegram.org`。Token 不在读取接口中回显，留空保留，可显式清除；它与其他凭据一样保存在私有工作空间数据库及备份中，数据库本身未加密。

## 投递行为

- 价格预警先写入站内通知，再由 `core.notification.telegram` 消费者独立投递。复用事件表和租约恢复，外部超时不会阻塞 SSE 消费者。
- 启用、更换机器人或更换接收目标后，只发送此后产生的新提醒。停用渠道或投资模块时跳过投递，历史收件箱不补发。
- 为避免陈旧行情提醒，超过 15 分钟的待投递预警终止为失败；站内通知和投资触发记录仍保留。
- 每个机器人串行发送，间隔至少 3.1 秒。网络/服务错误使用持久化指数退避，最多 12 次尝试；429 尊重 `retry_after`，无效凭据或权限等永久错误直接停止。不同机器人的候选配置测试相互隔离。
- 设置页显示最近尝试、最近成功、脱敏错误，以及自当前目标启用以来的终止失败数量。失败消息不提供手工重放，调整配置用于后续新预警。
- 采用至少一次投递：Telegram 已接收而本地事件尚未确认时若进程崩溃，恢复后仍可能重复。辅助状态保存失败不会主动重发已确认送达的消息。
- 纯文本消息含标的、阈值、价格、涨跌幅和行情时间；配置了工作台外部地址时附带监控页链接。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/settings/telegram` | `enabled`、`chatId`、`hasBotToken`、最近投递状态和失败数 |
| PUT | `/api/settings/telegram` | `{ enabled, botToken, chatId, clearBotToken }`，Token 留空保留 |
| POST | `/api/settings/telegram/test` | 测试当前表单并返回 `{ ok: true }`，不保存配置 |

接口使用平台鉴权、来源检查和 CSRF 保护。通道实现在 `internal/capabilities/notifications/telegram*.go`，前端在 `web/src/features/settings/TelegramPanel.tsx`。测试使用本地传输替身，不发送真实消息。
