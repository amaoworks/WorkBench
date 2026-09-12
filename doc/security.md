# 当前安全边界

本文根据 [auth](../internal/foundation/auth/auth.go)、[密码变更](../internal/foundation/auth/change_password.go)、[设置](../internal/app/settings.go)、[数据库](../internal/foundation/database/database.go) 和 [AI runtime](../internal/capabilities/ai/runtime.go) 描述实际行为。部署步骤见[部署与发布](deployment.md)，参数见[配置与运行](configuration.md)。

## 认证和浏览器请求

- local 模式仅允许 loopback 监听，免登录但保留 Host、Origin、CSRF 校验。
- 应用仅提供 HTTP。password 模式持久化密码哈希；反代部署显式配置 HTTPS 外部地址，非 loopback 密码监听必须提供该地址。配置外部地址时禁止 local 免登录模式。
- 密码使用 Argon2id；新密码至少 8 个 Unicode 字符，且大小写、数字、特殊符号四类中至少包含三类。
- Session 保存在 SQLite，Cookie 使用 HttpOnly、SameSite=Strict；配置 HTTPS 外部地址时使用 Secure。会话最长 7 天，空闲期限 24 小时，数据库保存哈希后的会话 token。
- 登录更新会话 token。密码变更检查当前密码，在事务中更新凭据版本并删除会话；受保护请求再次检查版本，旧登录失效。
- Host 必须匹配允许列表。写请求提供 Origin 时校验同源，配置 HTTPS 外部地址时要求 HTTPS Origin；写请求另由 CSRF token 保护。

应用以固定的 `WORKBENCH_PUBLIC_URL` 确定浏览器来源，Host 自动加入允许列表。CSRF 中间件按该配置判断外部 HTTPS，不依赖后端 TLS 状态或客户端可伪造的 `Forwarded` / `X-Forwarded-*`。应用的 HTTP 端口仅供本机代理或专用容器网络访问：二进制默认监听 loopback，Compose 默认只向宿主机 `127.0.0.1` 发布端口。代理保留原始 Host，并提供实际的 HTTPS 入口。

## 凭据和数据

AI、Wallos 与 Schwab 密钥保存在工作空间数据库中，读取设置只返回是否已有密钥。留空保留密钥的行为仅适用于相应地址或 App Key；更换地址、App Key 或 Schwab 回调须重新填写，避免把旧密钥自动发送给新服务。设置读取使用 `Cache-Control: no-store`。Schwab 访问令牌和刷新令牌同样只留在服务端，浏览器只调用本机代理。

数据库和备份文件权限设为 0600，新建父目录使用 0700。当前没有数据库内容加密，拥有这些文件读取权限的主体能够读取保存的配置密钥、对话和业务数据。

在线备份由应用生成一致性快照并检查完整性；运行中直接复制主数据库不能替代该流程。恢复使用停止后的工作空间或新的数据库路径。

## 外部服务和工具

AI 设置和 Wallos 设置接受用户配置的 HTTP(S) 地址，允许内网自托管服务。URL 校验拒绝内嵌凭据、查询参数和片段；请求不跟随重定向。当前没有目标网段隔离，配置者决定外部请求目的地。Schwab 回调必须是带 `/oauth/schwab` 路径的 HTTPS 地址，且与 Developer Portal 登记一致；OAuth 回调因跨站跳转无法携带 `SameSite=Strict` 会话 Cookie，故挂在登录保护之外，并用一次性 `state` 校验。成功后通过同站点文档自动返回投资，回调响应设置 `no-store` 和 `no-referrer`。TradingView 图表库反代会去掉工作台 Cookie 后再请求官方静态资源。

Wallos 通过表单发送密钥，不把密钥放入 URL；有请求超时、响应大小和订阅数上限，错误响应不会作为订阅内容批量写入。AI 配置测试及业务文本生成使用简化错误；HTTP 请求日志记录路径和状态，不记录请求体。聊天错误、Job 错误和工具结果仍应按各自实现审查，不能将这些局部处理视为全局脱敏保证。

AI Tool 只从已注册且启用的模块中选择，服务端校验 JSON schema、风险、幂等键和确认信息，并保存执行审计。文本生成接口不提供工具。当前没有通用高风险工具确认界面。

## 当前隔离范围

模块随可信应用代码一起编译，共享进程、数据库连接和服务依赖；模块边界不提供第三方代码沙箱。模块开关检查后续入口，不强制终止已运行的请求、任务或消费者。

应用当前是单用户工作空间，没有多租户权限模型，也没有内置登录限速。调整访问范围、接入外部代码或增加新的工具能力时，应围绕新的需求修改实现并同步本说明。

投资更换 Key、Secret、回调或重新完成 OAuth 时，会关闭已有 Streamer 与浏览器连接；连接建立过程校验配置版本，避免旧身份的 LOGIN 在配置变更后生效。停用模块及进程关闭也会清理长连接。浏览器 WebSocket 只接收事件，行情订阅使用 CSRF 保护的 HTTP ADD 命令，无法通过该接口发送 ADMIN/LOGIN。
