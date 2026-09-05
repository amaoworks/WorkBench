# MVP 配置与运行

Workbench 使用命令行参数管理部署配置，通过设置页面管理日常配置，不读取配置文件。默认命令不需要任何参数，并且只监听 loopback。

## 页面设置与生效方式

登录后访问 `/settings`：

| 设置 | 保存位置 | 生效方式 |
|---|---|---|
| AI 开关、接口地址、模型、API Key | SQLite `workspace_settings` | 保存后新请求立即生效；正在进行的请求继续使用原配置 |
| 外观主题、减少动态效果 | SQLite `workspace_settings` | 当前页面立即生效，其他设备重新加载后读取 |
| 登录密码 | SQLite `auth_credentials`，Argon2id 哈希 | 验证旧密码后修改，所有旧登录会话失效 |
| 在线备份 | 数据库同目录的 `backups/` | 点击后生成并校验，页面显示服务器上的文件名 |

AI 设置的优先级为：已保存的页面设置 > 启动环境变量 > 默认值。清除密钥或停用 AI 也会持久化，重启不会重新启用环境变量中的旧配置。环境变量只有在尚未保存 AI 页面设置时作为初始配置使用。

API Key 保存在权限为 `0600` 的工作空间数据库中，不做可逆加密，设置 API 不返回密钥。备份也包含密钥，须与数据库一样限制访问。更换接口地址时要求重新填写密钥，避免把原服务的密钥自动发送到新地址。连接测试会向当前表单配置的服务发送简短的 Responses API 请求，不保存设置，可能产生少量调用费用。

端口、数据库路径、认证模式、TLS 和 Host 白名单在页面只读展示，仍需修改启动配置并重启。首次升级到支持改密的版本时，旧登录会话需要重新登录。

## 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-listen` | `127.0.0.1:8080` | HTTP(S) 监听地址 |
| `-data` | `~/.workbench/data.db` | SQLite 工作空间路径 |
| `-auth` | `local` | `local` 或 `password` |
| `-tls-cert` | 空 | PEM TLS 证书；必须与 `-tls-key` 同时设置 |
| `-tls-key` | 空 | PEM TLS 私钥 |
| `-allowed-host` | 监听地址 | 允许的 HTTP Host，可重复传入 |

`local` 模式拒绝非 loopback 地址。`password` 模式首次启动必须提供至少 8 个 Unicode 字符的密码，且大写字母、小写字母、数字、特殊符号四类中至少包含三类。特殊符号包括标点和符号，空白不计入该类。非 loopback 时还必须启用 TLS，并用 `-allowed-host` 列出用户实际访问的 `host:port`。

## 环境变量

| 变量 | 用途 |
|---|---|
| `WORKBENCH_PASSWORD` | password 模式首次初始化密码；之后使用数据库中的 Argon2id 哈希 |
| `WORKBENCH_ALLOWED_HOSTS` | 逗号分隔 Host allowlist；仅在没有 `-allowed-host` 时采用 |
| `OPENAI_API_KEY` | 未保存页面 AI 设置时，用于启用 AI Provider |
| `OPENAI_MODEL` | 未保存页面 AI 设置时，覆盖默认模型 profile |
| `OPENAI_BASE_URL` | 未保存页面 AI 设置时，使用的受信任 Responses API 兼容地址 |

## 示例

本机默认运行：

```bash
./workbench
```

本机指定端口（例如 9090）：

```bash
./workbench -listen 127.0.0.1:9090
```

本机密码模式首次初始化（请替换示例密码）：

```bash
WORKBENCH_PASSWORD='MySecret1!' \
  ./workbench -auth password
```

受信任局域网域名与 TLS：

```bash
WORKBENCH_PASSWORD='MySecret1!' \
  ./workbench \
  -listen 0.0.0.0:8443 \
  -auth password \
  -tls-cert ./workbench.crt \
  -tls-key ./workbench.key \
  -allowed-host workbench.lan:8443
```

## 数据与备份

数据库、WAL/SHM 临时文件和进程锁位于数据文件同目录。不要把运行中的主数据库文件直接复制为一致性备份；登录后通过 `POST /api/system/backup` 创建在线备份，服务会执行 `integrity_check`，文件保存在 `<data-dir>/backups/`。
