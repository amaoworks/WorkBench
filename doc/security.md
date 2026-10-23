# 当前安全边界

本文根据 [auth](../internal/foundation/auth/auth.go)、[密码变更](../internal/foundation/auth/change_password.go)、[设置](../internal/app/settings.go)、[数据库](../internal/foundation/database/database.go) 和 [AI runtime](../internal/capabilities/ai/runtime.go) 描述实际行为。部署步骤见[部署与发布](deployment.md)，参数见[配置与运行](configuration.md)。

## 认证和浏览器请求

- local 模式仅允许 loopback 监听，免登录但保留 Host、Origin、CSRF 校验。
- 应用仅提供 HTTP。password 模式持久化密码哈希；反代部署显式配置 HTTPS 外部地址。password 模式默认拒绝非 loopback 的纯 HTTP 监听，`WORKBENCH_ALLOW_INSECURE_HTTP=true` 可为受控网络显式放行；Compose 默认启用该开关，但宿主机端口仅绑定 `127.0.0.1`。配置外部地址时禁止 local 免登录模式。
- 密码使用 Argon2id；新密码至少 8 个 Unicode 字符，且大小写、数字、特殊符号四类中至少包含三类。
- 新建密码凭据使用 `crypto/rand` 自动生成初始密码，也兼容通过 `WORKBENCH_PASSWORD` 显式提供，并保存 `must_change_password=1`。自动生成的凭据成功保存后，`auth` 日志器以 `warn` 等级输出 `initialPassword` 一次，不受日志等级过滤；显式提供的密码不打印，非首次启动不生成、不打印。已有数据库迁移新增列默认为 `0`，沿用当前密码且不要求改密。
- 自动生成或显式提供初始密码后，首次登录只允许 `/api/auth/status`、`/api/auth/login`、`/api/auth/logout`、`/api/auth/csrf` 和 `PUT /api/settings/password`；其他业务接口返回 `403 password_change_required`。新密码必须与初始密码不同。改密在事务中清除强制改密状态并删除全部会话，用户随后重新登录。
- Session 保存在所选的 SQLite 或 PostgreSQL 数据库中，Cookie 使用 HttpOnly；默认 `SameSite=Strict`，配置 HTTPS 外部地址及 CORS 白名单时使用 `SameSite=None`，配置 HTTPS 外部地址时使用 Secure。会话最长 7 天，空闲期限 24 小时，数据库保存哈希后的会话 token。
- 登录更新会话 token。密码变更检查当前密码，在事务中更新凭据版本并删除会话；受保护请求再次检查版本，旧登录失效。
- 密码哈希计算全局上限为 2 并发，由登录、密码修改和 `Authenticate` 共享；登录另设突发 5 次、每 3 秒恢复 1 次的令牌限速（密码修改与 `Authenticate` 不消耗登录令牌）。计算并发或登录令牌超限均返回 HTTP 429 并附带 `Retry-After`。
- Host 必须匹配允许列表。写请求提供 Origin 时校验同源或 `WORKBENCH_CORS_ALLOW_ORIGIN` 白名单；白名单只接受完整 HTTP/HTTPS origin，不接受 `*`，HTTPS 部署只接受 HTTPS 白名单。CORS 中间件处理预检和凭据响应，允许的 origin 同时加入 nosurf 白名单。纯 HTTP 跨站点访问可能无法携带 Cookie；同站点不同端口不受该限制。写请求另由 CSRF token 保护。

应用以固定的 `WORKBENCH_PUBLIC_URL` 确定浏览器来源；该值必须是 HTTPS origin，Host 自动加入允许列表。CSRF 中间件按该配置判断外部 HTTPS，不依赖后端 TLS 状态或客户端可伪造的 `Forwarded` / `X-Forwarded-*`。应用的 HTTP 端口仅供本机代理或专用容器网络访问：二进制默认监听 loopback，Compose 默认只向宿主机 `127.0.0.1` 发布端口。代理保留原始 Host，并提供实际的 HTTPS 入口。认证使用服务端 Session，没有采用 JWT。

## 凭据和数据

AI、Wallos、Schwab 与外部模块服务凭据保存在工作空间数据库中，读取设置只返回是否已有密钥。留空保留密钥的行为仅适用于相应地址或 App Key；更换地址、App Key、Schwab 回调或外部模块 origin 须重新填写，避免把旧密钥自动发送给新服务。设置读取使用 `Cache-Control: no-store`。Schwab 访问令牌和刷新令牌同样只留在服务端，浏览器只调用本机代理。外部模块 `serviceToken` 不出现在模块列表、配置读回、日志或 iframe URL 中。

HTTP 请求日志不记录正文、查询参数、Cookie、Authorization 或密码。首次自动生成的初始密码只由 `auth` 组件在凭据成功持久化后写入一次独立日志；该日志是管理员取得一次性密码的入口。

富途账号和登录密码的 MD5 摘要保存在工作空间数据库中，原始密码不持久化。摘要可直接用于 OpenD 登录，按登录凭据保护，不在 API、日志或浏览器 URL 中回显。原生部署将摘要写入私有 OpenD 配置；共享目录部署通过 `login.json` 提供给配套进程，目录权限为 `0750`、文件为 `0640`。密码留空保留摘要，更换账号须重新填写，清除密码会停用富途。登录由 OpenD 完成。连接地址和非本机许可由 `WORKBENCH_FUTU_OPEND_ADDRESS`、`WORKBENCH_FUTU_ALLOW_NON_LOCAL` 或对应启动参数管理，不在设置页面修改；默认只允许 loopback，配套 Compose 显式允许服务地址。工作台只发送行情协议，拒绝 `2xxx` 交易协议，浏览器不直连 OpenD。

SQLite 数据库和备份文件权限设为 0600，新建父目录使用 0700。PostgreSQL 由数据库服务控制读取权限，连接串通过 `WORKBENCH_DATABASE_URL` 传入，不在设置接口中回显。应用没有对数据库内容另行加密，拥有数据库读取权限的主体能够读取保存的配置密钥、对话和业务数据。

SQLite 在线备份由应用生成一致性快照并检查完整性；运行中直接复制主数据库不能替代该流程。恢复使用停止后的工作空间或新的数据库路径。PostgreSQL 备份和恢复由数据库服务或外部工具管理。

## 外部服务和工具

AI 设置和 Wallos 设置接受用户配置的 HTTP(S) 地址，允许内网自托管服务。URL 校验拒绝内嵌凭据、查询参数和片段；请求不跟随重定向。当前没有目标网段隔离，配置者决定外部请求目的地。Schwab 回调必须是带 `/oauth/schwab` 路径的 HTTPS 地址，且与 Developer Portal 登记一致；OAuth 回调因跨站跳转无法携带 `SameSite=Strict` 会话 Cookie，故挂在登录保护之外，并用一次性 `state` 校验。成功后通过同站点文档自动返回投资，回调响应设置 `no-store` 和 `no-referrer`。TradingView 图表库反代会去掉工作台 Cookie 后再请求官方静态资源。

Wallos 通过表单发送密钥，不把密钥放入 URL；有请求超时、响应大小和订阅数上限，错误响应不会作为订阅内容批量写入。AI 配置测试及业务文本生成使用简化错误；HTTP 请求日志记录路由模板和状态，不记录原始路径参数、查询参数、请求体或认证头；认证拒绝也会记录。后台任务与事件投递的服务日志只记录错误类型和执行标识，不直接复制上游错误正文。聊天错误、Job 错误和工具结果仍应按各自实现审查，不能将这些局部处理视为全局脱敏保证。

AI Tool 只从已注册且启用的模块中选择，服务端校验 JSON schema、风险、幂等键和确认信息，并保存执行审计。文本生成接口不提供工具。当前没有通用高风险工具确认界面。

## 当前隔离范围

内置模块随可信应用代码一起编译，共享进程、数据库连接和服务依赖；模块边界不提供第三方代码沙箱。已保存的外部模块是用户管理的独立 HTTP 服务，当前不开放通过网址新增。兼容页面使用同源 iframe 嵌入，与宿主共享浏览器信任边界，不能当作不可信插件隔离。同源代理会去掉浏览器 Cookie 和客户端 Authorization，注入该模块专用服务凭据，屏蔽上游 `Set-Cookie`，并拒绝路径穿越、`/_workbench/*` 业务代理、不安全重定向和未许可的非本机拨号。默认只连接 loopback。模块开关检查后续入口；外部模块停用会立即关闭宿主业务门控并在约 1 秒内关闭本机代理 WebSocket，但不保证远端任务已经停止。

应用当前是单用户工作空间，没有多租户权限模型。

投资更换 Key、Secret、回调或重新完成 OAuth 时，会关闭已有 Streamer 与浏览器连接；连接建立过程校验配置版本，避免旧身份的 LOGIN 在配置变更后生效。停用模块及进程关闭也会清理长连接。浏览器 WebSocket 只接收事件，行情订阅使用 CSRF 保护的 HTTP ADD 命令，无法通过该接口发送 ADMIN/LOGIN。夜盘覆盖开关关闭时，OpenD WebSocket 返回 409 且不升级；OpenD 不可用时在已升级连接上发出 TryAgainLater，避免把失败当成覆盖已关闭。
