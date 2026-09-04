# MVP 配置与运行

Workbench MVP 使用命令行参数和环境变量，不读取配置文件。默认命令不需要任何参数，并且只监听 loopback。

## 命令行参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-listen` | `127.0.0.1:8080` | HTTP(S) 监听地址 |
| `-data` | `~/.workbench/data.db` | SQLite 工作空间路径 |
| `-auth` | `local` | `local` 或 `password` |
| `-tls-cert` | 空 | PEM TLS 证书；必须与 `-tls-key` 同时设置 |
| `-tls-key` | 空 | PEM TLS 私钥 |
| `-allowed-host` | 监听地址 | 允许的 HTTP Host，可重复传入 |

`local` 模式拒绝非 loopback 地址。`password` 模式首次启动必须提供至少 12 个 Unicode 字符的密码；非 loopback 时还必须启用 TLS，并用 `-allowed-host` 列出用户实际访问的 `host:port`。

## 环境变量

| 变量 | 用途 |
|---|---|
| `WORKBENCH_PASSWORD` | password 模式首次初始化密码；之后使用数据库中的 Argon2id 哈希 |
| `WORKBENCH_ALLOWED_HOSTS` | 逗号分隔 Host allowlist；仅在没有 `-allowed-host` 时采用 |
| `OPENAI_API_KEY` | 启用可选 AI Provider |
| `OPENAI_MODEL` | 覆盖默认模型 profile |
| `OPENAI_BASE_URL` | 受信任环境中的 OpenAI 兼容 API base URL |

## 示例

本机默认运行：

```bash
./workbench
```

本机密码模式首次初始化：

```bash
WORKBENCH_PASSWORD='请设置至少十二个字符的安全密码' \
  ./workbench -auth password
```

受信任局域网域名与 TLS：

```bash
WORKBENCH_PASSWORD='请设置至少十二个字符的安全密码' \
  ./workbench \
  -listen 0.0.0.0:8443 \
  -auth password \
  -tls-cert ./workbench.crt \
  -tls-key ./workbench.key \
  -allowed-host workbench.lan:8443
```

## 数据与备份

数据库、WAL/SHM 临时文件和进程锁位于数据文件同目录。不要把运行中的主数据库文件直接复制为一致性备份；登录后通过 `POST /api/system/backup` 创建在线备份，服务会执行 `integrity_check`，文件保存在 `<data-dir>/backups/`。
