# 配置与运行

配置入口是 [cmd/workbench/main.go](../cmd/workbench/main.go)、[app/config.go](../internal/app/config.go) 和 [app/settings.go](../internal/app/settings.go)。应用仅监听 HTTP，HTTPS 由反向代理提供。二进制安装、Compose、systemd、代理配置和 Actions 发布见[部署与发布](deployment.md)。部署参数在启动时读取；业务开关、AI、外观和总览布局保存到当前工作空间。

## 构建和本地运行

Go 版本以 [go.mod](../go.mod) 为准，当前为 1.27.1。前端使用 Node.js 24+、npm 11+，依赖版本由 `web/package-lock.json` 锁定。重新生成 SQL 查询使用 sqlc 1.31.x，生成代码已检入。

```bash
./scripts/build.sh
./workbench
```

构建脚本执行 `npm ci`、TypeScript/Vite 构建，再编译 Go。前端输出到 `internal/webui/dist/` 并嵌入程序。已有前端和 sqlc 生成产物时也可直接 `go build -o workbench ./cmd/workbench`；修改前端后须先重新构建前端。

默认访问 `http://127.0.0.1:8080`，并使用 SQLite 数据库 `~/.workbench/data.db`。修改端口：

```bash
./workbench -listen 127.0.0.1:9090
```

首次以密码模式创建工作空间时，程序使用 `crypto/rand` 生成初始密码，保存凭据后由 `auth` 日志器以 `warn` 等级输出一次，不受日志等级过滤。systemd 部署可用 `journalctl -u workbench` 查看该条日志。首次登录必须把初始密码更换为不同的新密码，随后所有会话失效并重新登录。新密码至少 8 个 Unicode 字符，包含大写字母、小写字母、数字、特殊符号中的至少三类。

`WORKBENCH_PASSWORD` 保留为兼容入口，可为新工作空间显式提供初始密码；无论自动生成还是显式提供，首次登录都必须改密。已有数据库迁移后沿用当前密码，不进入强制改密流程；已有凭据也不会被环境变量覆盖。非首次启动不会重新生成或打印初始密码。

## 启动参数和环境变量

优先级为命令行参数 → 非空环境变量 → 默认值。程序不自动读取 `.env` 或配置文件；Compose 读取 `.env` 后传入容器，systemd 通过 `EnvironmentFile` 注入环境，开发脚本通过 `./scripts/dev.sh --config FILE` 加载同格式文件（见[开发脚本 - 配置文件](../scripts/README.md#配置文件)）。直接运行二进制时可用 `set -a; . ./workbench.env; set +a` 导入简单的 `KEY=VALUE` 文件。

| 参数 | 环境变量 | 默认值 / 行为 |
|---|---|---|
| `-listen` | `WORKBENCH_LISTEN` | 原生运行默认 `127.0.0.1:8080`，始终为 HTTP；容器默认 `0.0.0.0:8080` |
| — | `WORKBENCH_HTTP_PORT` | 空；非空时替换 `WORKBENCH_LISTEN` 中的端口，显式 `-listen` 仍优先 |
| `-db-driver` | `WORKBENCH_DB_DRIVER` | `sqlite`；可选 `sqlite`、`postgres` |
| — | `WORKBENCH_DATABASE_URL` | PostgreSQL 连接串；选择 `postgres` 时必填，不提供命令行参数 |
| `-data` | `WORKBENCH_DATA` | 用户目录下的 `.workbench/data.db`；SQLite 为数据库路径，PostgreSQL 使用其父目录保存本地运行文件 |
| `-log-level` | `WORKBENCH_LOG_LEVEL` | `info`；可选 `debug`、`info`、`warn`、`error`，工作空间已保存的日志等级优先 |
| `-auth` | `WORKBENCH_AUTH` | `local`；可选 `password` |
| `-futu-opend-address` | `WORKBENCH_FUTU_OPEND_ADDRESS` | OpenD 的 TCP 主机与端口，默认 `127.0.0.1:11111`；仅在部署配置中管理 |
| `-futu-allow-non-local` | `WORKBENCH_FUTU_ALLOW_NON_LOCAL` | 是否允许非本机 OpenD 地址，默认 `false` |
| `-futu-config-dir` | `WORKBENCH_FUTU_CONFIG_DIR` | 与配套 OpenD 共享的登录配置目录；账号密码在投资设置中填写 |
| `-futu-runtime-dir` | `WORKBENCH_FUTU_RUNTIME_DIR` | 原生 OpenD 私有运行目录，默认数据库同级 `futu-opend`；首次启用自动下载安装 |
| `-futu-opend-binary` | `WORKBENCH_FUTU_OPEND_BINARY` | 已安装的兼容 OpenD 程序路径；留空使用自动安装的 10.9.6908 |
| `-public-url` | `WORKBENCH_PUBLIC_URL` | 空；反代部署填写完整 HTTPS 来源，例如 `https://workbench.example.com`，可带端口、不可带子路径 |
| `-allowed-host` | `WORKBENCH_ALLOWED_HOSTS` | 参数可重复，环境变量用逗号分隔；存在参数时替代环境列表 |
| `-allow-insecure-http` | `WORKBENCH_ALLOW_INSECURE_HTTP` | `false`；设为 `true` 时允许 password 模式通过非 loopback 的纯 HTTP 监听 |
| — | `WORKBENCH_CORS_ALLOW_ORIGIN` | 空，同源访问；逗号分隔完整 HTTP/HTTPS origin，不接受 `*` |
| — | `WORKBENCH_PASSWORD` | 兼容配置；仅在密码模式尚无凭据时显式提供初始密码，留空则自动生成 |
| — | `OPENAI_API_KEY` | 尚未保存 AI 页面配置时的初始密钥；非空时初始启用 AI |
| — | `OPENAI_BASE_URL` | 初始 AI 端点；未设置时为 `https://api.openai.com/v1` |
| — | `OPENAI_MODEL` | 初始模型；代码默认值为 `gpt-5.2` |
| `-version` | — | 输出版本、提交号和构建时间后退出 |
| `-healthcheck` | — | 按当前监听和 Host 配置检查运行中的 HTTP 服务；不打开数据库、不启动服务 |

AI 默认值描述本项目配置，不代表模型选型建议。AI 保存后，数据库中的整套设置优先于上述环境变量。启动配置不覆盖工作空间里已经保存的业务设置。

原生运行默认使用 SQLite。仓库根目录的 Compose 默认启动 PostgreSQL 17 Alpine 并自动创建数据库，数据库类型和 DSN 可通过环境变量调整。其他 PostgreSQL 部署仅用于新工作空间，数据库须在启动前创建。连接串只通过环境变量传入，避免出现在进程命令行中；远程服务通常启用 TLS，例如：

```bash
export WORKBENCH_DB_DRIVER=postgres
export WORKBENCH_DATABASE_URL='postgresql://workbench:替换密码@db.example.com:5432/workbench?sslmode=require'
export WORKBENCH_DATA='./data/data.db'
./workbench
```

开发时可执行 `./scripts/db-manage.sh init` 在 Docker 中创建只监听 `127.0.0.1` 的 PostgreSQL，并生成可直接用于 `./scripts/dev.sh --config .env.dev-pg` 的完整开发配置；`start`、`stop`、`status`、`uninit` 管理该容器，见[开发脚本 - PostgreSQL 开发库](../scripts/README.md#postgresql-开发库)。应用不会把现有 SQLite 数据自动迁移到 PostgreSQL。PostgreSQL 部署仍需持久化 `WORKBENCH_DATA` 的父目录，其中保存图表缓存、OpenD 运行内容等本地文件。

`local` 仅接受 loopback 监听，适用于本机试用。配置 `public-url` 必须使用 `password`，且外部地址必须是 HTTPS origin。password 模式默认拒绝非 loopback 的纯 HTTP 监听；仅在受控网络中可显式设置 `WORKBENCH_ALLOW_INSECURE_HTTP=true`。二进制服务器部署通常仍监听 loopback，容器内监听 `0.0.0.0:8080`。Compose 默认允许容器内 HTTP，同时只向宿主机 `127.0.0.1` 发布端口。

```bash
./workbench -auth password -listen 127.0.0.1:8080 \
  -public-url https://workbench.example.com
```

外部地址的 Host 自动加入允许列表，显式配置的 `allowed-host` 可添加其他 Host。未配置外部地址及允许列表时，默认允许监听地址；loopback 监听还允许同端口的 `localhost`、`127.0.0.1` 和 `[::1]`。Host 校验使用请求实际发送的 `host[:port]`，反代必须保留原始 Host。

`WORKBENCH_CORS_ALLOW_ORIGIN` 为浏览器跨来源访问设置白名单。每项必须是完整 HTTP/HTTPS origin；不支持通配符。启用 HTTPS 外部地址时，白名单也必须全部使用 HTTPS。中间件处理预检请求、凭据响应头和 CSRF 来源白名单。HTTPS 部署配置 CORS 白名单时，Session 和 CSRF Cookie 使用 `SameSite=None`；其他部署保持 `SameSite=Strict`。纯 HTTP 跨站请求仍可能受浏览器 Cookie 限制，可使用同站点的不同端口。Session/CSRF 安全属性依据 `public-url` 计算，应用不信任动态 `X-Forwarded-*` 请求头。反代 Nginx/Caddy 配置、WebSocket 与 SSE 缓冲要求详见[部署与发布 - 反向代理](deployment.md#反向代理)，请求保护边界见[安全边界](security.md)。

旧的 `-tls-cert` 和 `-tls-key` 参数已移除，TLS 统一由反向代理终止。可在设置的「数据与运行」查看当前生效的监听地址、数据库类型、数据库路径或本地数据路径、认证模式与外部 Host。

## 设置页面

`/settings?tab=modules|ai|notifications|security|appearance|data` 可定位六个标签：

- 业务模块：启停已编译业务；模块提供设置组件时可打开独立弹窗。
- AI 配置：开关、端点、模型、密钥，支持测试连接与保存。端点须兼容 Responses API；测试会发送简短请求，可能产生费用。保存后新请求使用新 Provider。
- 通知推送：Telegram Bot Token、Chat ID、启停、测试发送和投递状态，见 [Telegram 通知](notifications.md)。
- 账户安全：验证当前密码后修改密码，已有登录全部失效。
- 外观：风格可选 TradingView（默认）或暖陶（Claude），主题可选浅色、深色或跟随系统（默认），并可减少动态效果；自动保存。头像菜单中的主题切换写入同一设置。
- 数据与运行：切换日志等级，查看数据库类型和部署参数；SQLite 可创建在线备份，PostgreSQL 显示外部备份提示。

切换设置标签会保留当前页面内的表单草稿；刷新或离开页面不会持久化未保存输入。AI、Wallos、Schwab 和 Telegram 密钥只写入、不回显，留空通常保留已有值；更换端点、App Key 或回调须重新提供密钥。详细边界见[安全说明](security.md)。

## 前端开发和验证

推荐使用 `./scripts/dev.sh` 同时启动 Go 与 Vite，例如：

```bash
./scripts/dev.sh -auth password -listen 127.0.0.1:9090 -allowed-host w.vm2.de5.net
```

脚本支持现有程序参数和环境变量，自动配置 Vite 的后端代理及开发 Host。`--web-port` 设置前端端口（默认 5173），`--web-host` 设置前端监听地址（默认 127.0.0.1），`--dev-url` 指定经反代访问的外部来源，`--config` 加载 Compose `.env` 格式的环境变量文件（命令行 → Shell 环境 → 文件 → 默认值），开发参数对应 `WORKBENCH_DEV_WEB_PORT`、`WORKBENCH_DEV_WEB_HOST`、`WORKBENCH_DEV_URL`，因此可只用 `./scripts/dev.sh --config FILE` 启动。未设置 `-data`/`WORKBENCH_DATA` 时，开发脚本使用仓库内 `.workbench/dev/data.db`，与原生默认工作空间分开；程序本身的默认值不变。端口占用时报错退出，不自动更换端口。React/CSS 保存后热更新；开发构建从磁盘读取投资终端 HTML/JS，修改后自动刷新。Go 修改需要退出并重新执行脚本，脚本每次启动会重新编译后端。

域名访问时反代上游需改为 Vite 前端端口，并保留 Host、支持 WebSocket 和 SSE。HTTPS 入口同时配置 `-public-url` 以正确设置 Cookie 和来源校验；仅传 `--dev-url` 不会改变后端的安全配置。完整示例、参数与退出行为见[开发脚本](../scripts/README.md#开发模式)。

也可单独启动前端：

在根目录启动后端。另一个终端进入 `web/` 执行：

```bash
npm ci
npm run dev
```

Vite 将 `/api`、`/health`、`/modules`、`/oauth/schwab`、`/investment/terminal` 和 `/charting_library` 代理到 `http://127.0.0.1:8080`。浏览器通过 Vite 访问时，后端也会校验浏览器的 Host，因此启动后端时应允许 Vite 实际使用的地址，例如 `-allowed-host localhost:5173 -allowed-host 127.0.0.1:8080`；端口变化时相应调整。前后端联调保持 Origin 与代理保留的 Host 一致。

Schwab OAuth 回调必须使用实际可访问工作台的 HTTPS 入口，例如 `https://workbench.example.com/oauth/schwab`（替换为实际域名和端口），并在工作台和 Schwab Developer Portal 登记相同的完整 URL。默认 HTTP 后端或 Vite 开发地址不能直接用作此回调；先配置 HTTPS 反向代理和 `WORKBENCH_PUBLIC_URL`，并让 `/oauth/schwab` 转发到工作台回调处理器。建议从同一个 HTTPS 入口打开工作台并开始登录。部署在服务器时，`127.0.0.1` 指向用户浏览器所在机器，不能代替服务器地址。

在 Schwab 的账户关联确认页点击 Done 后，浏览器应进入工作台的 `/oauth/schwab`；成功后自动返回 `/investment`。如果点击后仍停留在 Schwab 域名，先核对上述两个回调配置和授权请求的 `redirect_uri`。如果进入工作台后出现 code/state 或换取令牌错误，按回调页面的具体错误排查。诊断时只分享域名和路径，不分享授权 code、state、密钥或令牌。

完整验证执行 `./scripts/test.sh`。`GO_BIN` 可以指定 Go 可执行文件，`SQLC_BIN` 可以指定 sqlc；后者未设置时依次查找 `.tools/bin/sqlc` 和 PATH。浏览器脚本和临时环境运行方法见 [scripts/README](../scripts/README.md)。

## 备份和恢复

SQLite 设置中的备份调用 `POST /api/system/backup`，在数据库同目录的 `backups/` 下生成文件，接口返回文件名。后端使用 `VACUUM INTO` 创建一致性快照并执行完整性检查，不提供浏览器下载或在线恢复入口。

SQLite 恢复时先停止使用该工作空间的进程，保留现有数据库及其 sidecar 文件用于回退。将备份复制到一个新的数据库路径，通过 `-data` 指向它启动，程序会执行所需迁移。不要把备份覆盖到仍在运行的数据库上，也不要把旧 WAL 与恢复的数据库混用。

PostgreSQL 设置页显示“由数据库服务管理”，`POST /api/system/backup` 返回 HTTP 501。使用数据库服务提供的备份机制或 `pg_dump`/`pg_restore` 执行备份与恢复，应用不调用或内置这些工具。

数据库及备份含凭据和业务内容。SQLite 创建数据库目录时使用 0700，数据库和完成的备份文件设为 0600；PostgreSQL 连接串所在环境文件及数据库服务备份应限制读取权限。文件布局、迁移表和维护行为见[数据设计](data-model.md)。

健康检查为 `GET /health/live` 和 `GET /health/ready`，后者检查数据库连接；无需登录，但仍校验 Host。`workbench -healthcheck` 自动使用配置的外部 Host 或允许列表，通过 HTTP 探测运行中的服务。

## 运行日志

设置 → 数据与运行 → 运行日志中选择最低记录等级并保存，无需重启。等级保存到当前数据库的 `workspace_settings.logging`，重启后优先于 `-log-level` 和 `WORKBENCH_LOG_LEVEL`；未保存过时使用启动默认值。日志等级不会改变程序行为或健康检查结果。

| 最低等级 | 记录内容 |
|---|---|
| `debug` | 全部日志，加上成功的健康检查、后台任务和事件投递细节 |
| `info`（默认） | 正常 HTTP 请求、启停、配置变更、备份，以及警告和错误 |
| `warn` | HTTP 4xx、任务重试、事件投递重试、行情连接异常，以及错误 |
| `error` | HTTP 5xx、请求 panic、后台任务最终失败和事件死信等错误 |

二进制和容器使用同一套 JSON 日志，输出到标准错误流（stderr），含时间、等级、消息和组件。HTTP 日志另含 `requestId`、方法、路由模板、状态、字节数、耗时；响应返回同一个 `X-Request-ID`。Host/Origin/CSRF 拒绝也会记录。SSE/WebSocket 请求日志在连接结束时输出，中间件保留流式刷新和连接升级能力。

请求日志不记录正文、查询参数、Cookie 或 Authorization。路径使用 `/api/modules/{id}/enabled` 等模板，未匹配或提前拒绝的请求标记为 `unmatched`，避免把路径内账户标识写入日志。Panic 记录调用栈但不记录 panic 值；后台业务失败记录任务/事件标识、重试状态和错误类型，不直接复制可能包含密钥的上游响应。自动生成的初始密码由 `auth` 组件在凭据成功保存后以独立 `warn` 日志输出一次，不属于 HTTP 请求日志。数据库内原有任务错误记录不是服务日志，参见[安全说明](security.md)。

```bash
# 二进制：捕获 JSON 日志，文件由运行用户管理
./workbench -log-level info 2>>workbench.log
# Docker Compose：自带每份 10 MB、最多 3 份的滚动日志配置
docker compose logs -f --tail 100 workbench
# systemd：由 journal 管理日志
journalctl -u workbench -f
```

程序不另外写日志文件；直接重定向部署时由管理员配置文件权限和轮转。临时排障可在页面切到 debug，排查完成后切回 info。
