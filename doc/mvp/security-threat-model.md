# MVP 安全威胁模型

本文覆盖单用户、本地优先部署的 MVP。信任边界是浏览器、Workbench 进程、SQLite 工作空间和外部 OpenAI API；业务模块属于随二进制一起编译并受信任的代码，不把第三方动态插件视为可信输入。

## 资产与入口

- 资产：SQLite 数据、密码哈希、服务器端 Session、OpenAI API Key、对话内容、AITool 执行权限与审计记录。
- 网络入口：HTTP API、SSE、静态 Web UI；MVP 没有外部 IM、Webhook 或任意 URL 抓取入口。
- 本地入口：命令行参数、环境变量、数据库路径和备份目录。

## 威胁、控制与剩余风险

| 威胁 | MVP 控制 | 剩余风险/后续动作 |
|---|---|---|
| 意外暴露到局域网或公网 | `local` 模式只允许 loopback；非 loopback 的 `password` 模式必须配置 TLS；默认监听 `127.0.0.1` | 操作系统账户或主机已失陷不在应用层防护范围内 |
| 弱密码或密码泄露 | 首次密码来自环境变量；使用 Argon2id 保存哈希；登录错误不区分账户状态 | MVP 无登录限速；真正远程部署前增加速率限制和凭据轮换 |
| Session 窃取/固定 | 随机 opaque token、服务端 SQLite Store、登录时 renew、过期清理、`HttpOnly`、`SameSite=Strict`，HTTPS 时 `Secure` | 本机恶意软件仍可能读取进程或数据库 |
| CSRF、DNS rebinding 与跨源调用 | 修改请求校验 CSRF token；Host allowlist；Origin 同源校验；API 不使用宽松 CORS | 非浏览器客户端必须自行保护其本地执行环境 |
| SSRF | MVP 不提供通用 URL 抓取器；通知 action 只接受本站相对路由；Provider base URL 仅来自可信启动配置 | 未来行情连接器和 Channel Adapter 必须分别做协议、主机、端口及重定向 allowlist |
| AITool 越权或提示注入 | Tool 来自编译期注册表；模块 enabled gate；严格 JSON Schema；风险分级；高风险显式确认；写操作幂等；调用审计脱敏 | 模型输出仍不可信；新增 Tool 必须最小权限并在 Handler 内重复校验领域约束 |
| API Key/敏感提示泄露 | API Key 仅从环境变量读取，不写数据库或日志；OpenAI Responses 使用 `store:false`；Tool 参数按敏感字段名脱敏 | 用户主动放入普通文本的秘密无法可靠自动识别，应在 UI 和文档中提示 |
| SQLite 损坏、并发写入或不一致备份 | WAL、`synchronous=FULL`、busy timeout、单工作空间文件锁；在线一致性备份后执行 integrity check | 磁盘、系统账户和备份文件权限由部署环境负责；应定期把备份复制到独立介质 |
| 事件/任务重复副作用 | Transactional Outbox、稳定 Consumer/Job ID、claim lease、重试；通知与 Tool 使用 idempotency key | 至少一次投递仍要求每个 Consumer Handler 自身实现幂等 |
| XSS 与动态模块代码执行 | React 默认转义；前端 page/widget 为编译期注册表；后端只返回稳定键，不下发可执行组件 | 新增富文本前必须引入经过审核的 sanitizer 和 CSP |

## 秘密与日志规则

- 不把密码、Session token、API Key 或完整敏感 Tool 参数写入普通配置、事件 payload 或日志。
- 错误返回使用稳定错误码，不向客户端暴露数据库语句、文件系统细节或供应商凭据。
- 备份与数据库具有同等敏感等级；恢复前先做 `PRAGMA integrity_check`。

## 上线前复审触发条件

出现任一情况必须更新本模型：开放非 loopback 访问、接入真实券商/行情 API、增加任意 URL 获取、外部 IM/Webhook、第三方模块、文件上传、富文本渲染或多用户支持。
