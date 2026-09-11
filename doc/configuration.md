# 配置与运行

配置入口是 [cmd/workbench/main.go](../cmd/workbench/main.go)、[app/config.go](../internal/app/config.go) 和 [app/settings.go](../internal/app/settings.go)。部署参数在启动时读取；业务开关、AI、外观和总览布局保存到当前工作空间。

## 构建和本地运行

Go 版本以 [go.mod](../go.mod) 为准，当前为 1.27.1。前端使用 Node.js 24+、npm 11+，依赖版本由 `web/package-lock.json` 锁定。重新生成 SQL 查询使用 sqlc 1.31.x，生成代码已检入。

```bash
./scripts/build.sh
./workbench
```

构建脚本执行 `npm ci`、TypeScript/Vite 构建，再编译 Go。前端输出到 `internal/webui/dist/` 并嵌入程序。已有前端和 sqlc 生成产物时也可直接 `go build -o workbench ./cmd/workbench`；修改前端后须先重新构建前端。

默认访问 `http://127.0.0.1:8080`，数据库为 `~/.workbench/data.db`。修改端口：

```bash
./workbench -listen 127.0.0.1:9090
```

首次启用密码模式时，通过 `WORKBENCH_PASSWORD` 设置初始密码，再用 `./workbench -auth password` 启动。密码至少 8 个 Unicode 字符，包含大写字母、小写字母、数字、特殊符号中的至少三类。已有凭据时，环境变量不会覆盖密码；修改密码使用设置中的账户安全。

## 启动参数和环境变量

| 配置 | 默认值 / 行为 |
|---|---|
| `-listen` | `127.0.0.1:8080` |
| `-data` | 用户目录下的 `.workbench/data.db` |
| `-auth` | `local`；可选 `password` |
| `-tls-cert`、`-tls-key` | 成对提供证书和私钥文件 |
| `-allowed-host` | 可重复指定允许的 HTTP Host，例如 `workbench.lan:8443` |
| `WORKBENCH_ALLOWED_HOSTS` | 逗号分隔的 Host；存在 `-allowed-host` 时不使用此环境变量 |
| `WORKBENCH_PASSWORD` | 仅在密码模式尚无凭据时用于初始化 |
| `OPENAI_API_KEY` | 尚未保存 AI 页面配置时的初始密钥；非空时初始启用 AI |
| `OPENAI_BASE_URL` | 初始 AI 端点；未设置时为 `https://api.openai.com/v1` |
| `OPENAI_MODEL` | 初始模型；代码默认值为 `gpt-5.2` |

AI 默认值描述本项目配置，不代表模型选型建议。AI 保存后，数据库中的整套设置优先于上述环境变量。应用没有配置文件加载入口，也没有数据路径或监听端口的环境变量入口。

`local` 仅接受 loopback 监听地址。非 loopback 使用 `password` 并提供 TLS，例如初始化凭据后运行：

```bash
./workbench -auth password -listen 0.0.0.0:8443 \
  -tls-cert /path/to/cert.pem -tls-key /path/to/key.pem \
  -allowed-host workbench.lan:8443
```

Host 校验使用请求实际发送的 `host[:port]`。未明确配置时，默认允许监听地址；loopback 监听还允许同端口的 localhost、127.0.0.1 和 ::1。使用域名或通配监听时明确列出浏览器访问的 Host。

应用依据自身 TLS 配置判断 HTTPS，不通过代理转发头改变认证模式或 Cookie 安全属性。反向代理必须保留正确 Host；流式聊天和通知 SSE 需要关闭响应缓冲。

## 设置页面

`/settings?tab=modules|ai|security|appearance|data` 可定位五个标签：

- 业务模块：启停已编译业务；模块提供设置组件时可打开独立弹窗。
- AI 配置：开关、端点、模型、密钥，支持测试连接与保存。端点须兼容 Responses API；测试会发送简短请求，可能产生费用。保存后新请求使用新 Provider。
- 账户安全：验证当前密码后修改密码，已有登录全部失效。
- 外观：日光、夜幕、跟随系统，以及减少动态效果；自动保存。
- 数据与运行：创建在线备份，并查看当前部署参数。

切换设置标签会保留当前页面内的表单草稿；刷新或离开页面不会持久化未保存输入。AI、Wallos 和 Schwab 密钥只写入、不回显，留空通常保留已有值；更换端点、App Key 或回调须重新提供密钥。详细边界见[安全说明](security.md)。

## 前端开发和验证

在根目录启动后端。另一个终端进入 `web/` 执行：

```bash
npm ci
npm run dev
```

Vite 将 `/api`、`/health`、`/oauth/schwab`、`/investment/terminal` 和 `/charting_library` 代理到 `http://127.0.0.1:8080`。浏览器通过 Vite 访问时，后端也会校验浏览器的 Host，因此启动后端时应允许 Vite 实际使用的地址，例如 `-allowed-host localhost:5173 -allowed-host 127.0.0.1:8080`；端口变化时相应调整。前后端联调保持 Origin 与代理保留的 Host 一致。

Schwab OAuth 回调必须使用实际可访问工作台的 HTTPS 入口，例如 `https://workbench.example.com/oauth/schwab`（替换为实际域名和端口），并在工作台和 Schwab Developer Portal 登记相同的完整 URL。默认 HTTP 后端或 Vite 开发地址不能直接用作此回调；先配置 TLS 或 HTTPS 反向代理，并让 `/oauth/schwab` 转发到工作台回调处理器。建议从同一个 HTTPS 入口打开工作台并开始登录。部署在服务器时，`127.0.0.1` 指向用户浏览器所在机器，不能代替服务器地址。

在 Schwab 的账户关联确认页点击 Done 后，浏览器应进入工作台的 `/oauth/schwab`；成功后自动返回 `/investment`。如果点击后仍停留在 Schwab 域名，先核对上述两个回调配置和授权请求的 `redirect_uri`。如果进入工作台后出现 code/state 或换取令牌错误，按回调页面的具体错误排查。诊断时只分享域名和路径，不分享授权 code、state、密钥或令牌。

完整验证执行 `./scripts/test.sh`。`GO_BIN` 可以指定 Go 可执行文件，`SQLC_BIN` 可以指定 sqlc；后者未设置时依次查找 `.tools/bin/sqlc` 和 PATH。浏览器脚本和临时环境运行方法见 [scripts/README](../scripts/README.md)。

## 备份和恢复

设置中的备份调用 `POST /api/system/backup`，在数据库同目录的 `backups/` 下生成文件，接口返回文件名。后端使用 `VACUUM INTO` 创建一致性快照并执行完整性检查，不提供浏览器下载或在线恢复入口。

恢复时先停止使用该工作空间的进程，保留现有数据库及其 sidecar 文件用于回退。将备份复制到一个新的数据库路径，通过 `-data` 指向它启动，程序会执行所需迁移。不要把备份覆盖到仍在运行的数据库上，也不要把旧 WAL 与恢复的数据库混用。

数据库及备份含凭据和业务内容，应用创建数据库目录时使用 0700，数据库和完成的备份文件设为 0600。文件布局、迁移表和维护行为见[数据设计](data-model.md)。

健康检查为 `GET /health/live` 和 `GET /health/ready`，后者检查数据库连接。应用以 JSON 日志输出到 stderr，HTTP 日志包含请求 ID、方法、路径、状态、字节数和耗时。
