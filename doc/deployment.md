# 部署与发布

Workbench 发布 Linux amd64、arm64 二进制包，以及相同架构的 Docker 镜像。两种方式运行同一个 Go 程序：前端、数据库迁移和时区数据嵌入程序，SQLite 随工作空间保存。运行机器无需安装 Go、Node.js 或单独的数据库服务；Linux 主机需有 CA 根证书，镜像已包含根证书。

应用始终提供 HTTP 服务，HTTPS 由已有 Nginx、Caddy 等反向代理负责。浏览器访问工作台的 HTTPS 域名，代理转发到本机 HTTP 端口。应用使用 `WORKBENCH_PUBLIC_URL` 确定外部来源和安全 Cookie 属性；该地址不是监听地址，也不会让应用加载证书。

## 选择部署方式

| 方式 | 获取程序 | HTTP 后端 | 数据位置 |
|---|---|---|---|
| 本机试用 | 解压二进制包 | `127.0.0.1:8080`，默认 local 模式 | `~/.workbench/data.db` |
| 二进制 + systemd | 解压后安装 `workbench` | `127.0.0.1:8080`，密码模式 | `/var/lib/workbench/` |
| Docker Compose | 从 GHCR 拉取指定版本镜像 | 容器内 `0.0.0.0:8080`，宿主机仅发布到 `127.0.0.1:8080` | Compose 命名卷 `/data` |

同一工作空间只运行一个实例。SQLite 使用文件锁和 WAL，应放在本地持久化磁盘；容器挂载整个 `/data`，包含主数据库、WAL、SHM、锁文件和 `backups/`。不要只挂载 `data.db` 或让多个实例共享同一数据库。

投资模块的 TradingView 库仍由应用从外部静态服务代理加载；行情、券商、AI 和 Wallos 联动也需要访问各自服务。单文件部署不等于全部功能离线运行。

## 二进制部署

从 [GitHub Releases](https://github.com/amaoworks/WorkBench/releases) 下载相同版本的 `SHA256SUMS` 和对应架构压缩包。`uname -m` 为 `x86_64` 时选择 amd64，为 `aarch64` 时选择 arm64。下文 `v0.1.1` 仅为示例，请替换为已发布版本。

```bash
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf workbench_v0.1.1_linux_amd64.tar.gz
cd workbench_v0.1.1_linux_amd64
./workbench -version
./workbench
```

本机试用访问 `http://127.0.0.1:8080`。服务器部署使用密码和外部访问地址，例如：

```bash
export WORKBENCH_PASSWORD='替换为独立的强密码'
./workbench -listen 127.0.0.1:8080 -data ./data/data.db \
  -auth password -public-url https://workbench.example.com
```

随后按下文配置反向代理，从 `https://workbench.example.com` 登录。首次密码至少 8 个 Unicode 字符，且包含大写、小写、数字、特殊符号四类中的至少三类。密码哈希保存到数据库后可以移除启动环境中的初始密码；已有凭据时不会被该环境变量覆盖。

### systemd 托管

压缩包中的 `deploy/workbench.service` 使用固定系统用户 `workbench`。首次安装时创建用户；已有该用户则跳过创建步骤：

```bash
sudo useradd --system --user-group --home-dir /var/lib/workbench --shell /usr/sbin/nologin workbench
sudo install -m 0755 workbench /usr/local/bin/workbench
sudo install -d -m 0750 /etc/workbench
sudo install -m 0600 deploy/workbench.env.example /etc/workbench/workbench.env
sudo install -m 0644 deploy/workbench.service /etc/systemd/system/workbench.service
```

编辑 `/etc/workbench/workbench.env`，填写实际的外部 HTTPS 地址和初始密码，再启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now workbench
sudo systemctl status workbench
sudo journalctl -u workbench -f
```

systemd 负责创建并赋予 `/var/lib/workbench` 正确的所有权和 0700 权限。环境文件使用 systemd `EnvironmentFile` 语法，不会执行 shell 变量展开。修改环境文件后执行 `sudo systemctl restart workbench`。

## Docker Compose 部署

从 Release 下载 `workbench_<版本>_deployment.tar.gz` 并解压，或在仓库根目录使用 `compose.yaml`。部署文件包包含 Compose、环境示例、代理配置和文档，无需拉取源码或在服务器编译。

```bash
cp .env.example .env
chmod 600 .env
```

编辑 `.env`：

```dotenv
WORKBENCH_IMAGE=ghcr.io/amaoworks/workbench
WORKBENCH_VERSION=v0.1.1
WORKBENCH_PORT=8080
WORKBENCH_PUBLIC_URL=https://workbench.example.com
WORKBENCH_PASSWORD='填写独立的强密码'
```

`WORKBENCH_VERSION` 必须为已发布的版本标签。密码包含 `$`、`#` 等字符时使用单引号，按 Compose 的 `.env` 规则保留字面值；`.env` 已被 Git 和 Docker 构建上下文排除。AI、Wallos、Schwab、富途 OpenD 等业务配置在工作台设置页面保存。夜盘覆盖需要单独的 OpenD 进程，不要打进 workbench 镜像。可叠加仓库内 [futu-opend](../futu-opend/)（该目录可整体移到独立仓库）：

```bash
# 使用上面已经配置工作台版本与 HTTPS 地址的根目录 .env
docker compose -f compose.yaml -f futu-opend/compose.workbench.yaml --env-file .env up -d --build
```

在「设置 → 业务模块 → 投资设置」填写 Futu 账号和登录密码，保存并启用 Futu，然后单独启用夜盘。主机、端口和非本机许可已由配套 Compose 配置；独立部署使用 `WORKBENCH_FUTU_OPEND_ADDRESS`、`WORKBENCH_FUTU_ALLOW_NON_LOCAL`。需要使用支持共享登录配置的当前工作台镜像和配套 OpenD 镜像。首次登录若要求验证码，telnet `127.0.0.1:22222`。独立 OpenD 与账号保留规则见 [OpenD 部署说明](../futu-opend/README.md)。

```bash
docker compose config --quiet
docker compose pull
docker compose up -d --wait
docker compose ps
docker compose logs -f --tail 100 workbench
```

配置宿主机反向代理后，通过 `.env` 中的 HTTPS 地址访问。容器始终以 UID/GID `65532:65532` 运行，根文件系统只读，`/data` 可写，`/tmp` 为临时内存文件系统。日志输出到 stderr，并由 Compose 配置滚动保存。命名卷首次创建时继承镜像中 `/data` 的权限。

已有凭据后，可以把 `.env` 中的 `WORKBENCH_PASSWORD` 清空，再执行 `docker compose up -d`；原密码继续生效。日常使用 `docker compose stop` 或 `docker compose down` 停止服务并保留数据卷。`docker compose down --volumes` 会删除数据，不用于升级或常规停机。

GHCR 公共镜像允许匿名拉取；私有镜像需要先用有读取该包权限的凭据登录 `ghcr.io`。仓库公开并不自动使新建的 GHCR 包公开，首次发布后需核对 Package 的可见性和 Actions 访问权限。[GHCR 文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)

### 反向代理也运行在 Docker 中

将代理与 Workbench 接入同一个专用 Docker 网络，代理的上游使用 `http://workbench:8080`。代理容器里的 `127.0.0.1` 指向它自身，不能用于访问 Workbench。

仓库提供 `deploy/compose.proxy.yaml`，删除宿主机端口映射并加入已有网络。示例网络名为 `proxy`；可通过 `WORKBENCH_PROXY_NETWORK` 指定已有网络名：

```bash
docker compose -f compose.yaml -f deploy/compose.proxy.yaml up -d --wait
```

先确认网络已存在且代理已加入。此后更新、停止和查看日志均使用相同的两个 `-f` 参数。网络应允许 Workbench 访问外部服务，仅连接受信任的容器。覆盖文件的 `!reset` 语法需要 Docker Compose 2.24.4 或更新版本。

### 使用宿主机目录

需要直接查看备份文件时，可将 `workbench_data:/data` 改成 `./data:/data`。启动前创建目录并设置权限：

```bash
mkdir -p data
sudo chown 65532:65532 data
sudo chmod 700 data
```

迁入已有工作空间时，先停止旧进程，复制完整目录，再让其中的数据库、运行文件和备份归属 UID/GID `65532:65532`。原生二进制和 Docker 使用同一数据库格式，但不可同时打开同一工作空间。Rootless Docker 或用户命名空间映射环境需使用实际映射到宿主机的 UID/GID。

## 反向代理

代理保留浏览器的原始 Host，转发全部路径，包括 `/api`、`/oauth/schwab`、`/investment/terminal`、`/charting_library` 和 WebSocket。支持根路径部署，不能配置成 `/workbench/` 子路径。

`WORKBENCH_PUBLIC_URL` 的 Host（包括非标准端口）自动加入允许列表。应用不根据 `Forwarded` 或 `X-Forwarded-*` 判断安全来源，也不读取这些头来改变 Cookie 属性；无需配置可信代理 CIDR。后端的访问隔离由 loopback 监听、Compose 端口映射或专用容器网络保证，不应把 HTTP 后端直接发布到公网。

- **Nginx**：参考 `deploy/nginx.conf.example`，替换域名、证书路径和后端端口。示例保留 Host，配置 WebSocket Upgrade，并关闭 SSE 响应缓冲。`map` 应放在 `http` 上下文。先执行 `nginx -t`，通过后重新加载配置。[WebSocket 代理说明](https://nginx.org/en/docs/http/websocket.html)
- **Caddy**：参考 `deploy/Caddyfile.example`。宿主机 Caddy 代理到 `127.0.0.1:8080`；容器 Caddy 代理到 `workbench:8080`。域名与 DNS、80/443 入口配置正确时，由 Caddy 管理外部 HTTPS。[reverse_proxy 说明](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)

Schwab 的回调地址填写 `https://实际域名/oauth/schwab`，与 Developer Portal 登记完全一致；从同一个 HTTPS 入口开始授权。回调流程及排查见[配置与运行](configuration.md)。

## 健康检查和运行信息

`GET /health/live` 表示 HTTP 服务响应，`GET /health/ready` 检查数据库。健康路由无需登录，但仍受 Host 校验保护。

容器内置探针会读取当前环境配置，直接请求 HTTP 后端并携带正确的 Host，不依赖 shell 或 curl，也不重新打开数据库：

```bash
docker compose exec -T workbench /workbench -healthcheck
docker compose exec -T workbench /workbench -version
```

二进制也支持 `-healthcheck`，需使用与服务一致的监听地址、外部 URL 或允许 Host。成功退出码为 0，失败为非 0。此探针验证应用及数据库；外部证书、DNS、代理可用性需从实际 HTTPS 入口检查。

## 升级、备份与恢复

1. 在设置的「数据与运行」创建在线备份，并把备份保存到工作空间以外的位置。备份包含业务数据和凭据，应限制读取权限。
2. 停止服务。二进制部署更换可执行文件；Compose 修改 `.env` 的版本后执行 `docker compose pull` 和 `docker compose up -d --wait`，保留原数据卷及项目名。
3. 检查版本、健康状态和日志，登录确认数据。首次启动会执行所需数据库迁移。

命名卷中的备份可复制到宿主机，例如将返回的文件名代入：

```bash
docker compose cp workbench:/data/backups/备份文件名.db ./workbench-backup.db
```

回退前先停止服务并保留当前完整工作空间。旧版本未必兼容升级后的数据库，必要时同时恢复升级前备份：将备份复制到新的数据目录，使用该目录和原版本启动。不要把旧 WAL 与恢复的数据库混用，也不要向运行中的数据库覆盖文件。详细恢复语义见[配置与运行](configuration.md#备份和恢复)。

## 从源码构建

```bash
./scripts/build.sh
./workbench -version
docker build -t workbench:local .
```

使用本地镜像运行 Compose 时，在 `.env` 中设置 `WORKBENCH_IMAGE=workbench`、`WORKBENCH_VERSION=local`，保留外部地址和密码配置，再执行 `docker compose up -d --wait`。

`scripts/build.sh` 执行 `npm ci`、前端生产构建和 `scripts/compile.sh`。`compile.sh` 强制 `CGO_ENABLED=0`，支持 `GO_BIN`、`GOOS`、`GOARCH`、`OUTPUT`、`VERSION`、`COMMIT`、`BUILD_DATE`；直接调用前应构建最新前端。

Dockerfile 从源码多阶段构建，忽略本地 `internal/webui/dist` 并重新生成前端。Go 版本默认为 `go.mod` 当前版本，Actions 也从该文件读取；升级 Go 时同步 Dockerfile 默认值。多架构通过 Go 交叉编译构建，编译阶段无需 QEMU。最终为 scratch 镜像，仅包含程序、根证书及运行目录。

本地生成发布包：

```bash
VERSION=v0.1.1 ./scripts/release.sh
```

产物位于 `dist/release/v0.1.1/`，包括两个 Linux 压缩包、部署文件包和 `SHA256SUMS`。本地打包需要 Go、Node.js/npm、tar 及 sha256sum；Go、前端依赖和 sqlc 要求见[配置与运行](configuration.md)。

## GitHub Actions 编译与发布

`.github/workflows/ci.yml`（界面名称 **CI and Build**）支持 PR、主分支推送和 `workflow_dispatch`。手动编译时，在 Actions 中选择 **CI and Build → Run workflow** 并选择目标分支。

流程先通过 `scripts/check-workflows.sh` 执行 actionlint 和 ShellCheck，缺少 ShellCheck 会明确失败，避免本地悄悄跳过而 CI 报错。随后检查 SQL 生成漂移、Go 测试、前端 lint、投资 JS 测试和生产构建，以及部署相关并发行为，最后在 Linux amd64/arm64 runner 上分别构建镜像并验证 Compose 生命周期、登录、备份和数据保留。

主分支推送及手动分支构建还通过 `scripts/export-build.sh` 从已经验证的镜像中提取相同的静态二进制，导出 Docker 镜像和部署文件包，并验证导出的二进制。构建版本为 `v0.0.0-dev.<运行序号>-<短提交号>`，产物上传到该次运行的 **Artifacts**，按架构分为 `workbench-linux-amd64-<提交号>`、`workbench-linux-arm64-<提交号>`，保留 14 天。PR 执行检查和镜像验证，正式标签的产物由 Release 工作流发布。

下载 artifact ZIP 并解压后，先运行 `sha256sum --check SHA256SUMS`。二进制包解压即可运行；`*_docker.tar.gz` 使用 `docker load --input 文件名` 导入，镜像名为 `workbench:sha-<完整提交号>`。Compose 设置 `WORKBENCH_IMAGE=workbench`、`WORKBENCH_VERSION=sha-<完整提交号>`，其余配置与正常部署一致。普通构建不推送 GHCR，也不创建正式 Release。


`.github/workflows/release.yml` 在推送 `v*` 标签时触发。接受 `vMAJOR.MINOR.PATCH` 及预发布后缀，例如 `v0.1.1-rc.1`，不使用带 `+` 的构建元数据。正式包只能从 `main` 产出：标签指向的提交必须已经包含在 `origin/main` 中。先推送 `main`，再在该提交上打标签。

```bash
git checkout main
git pull origin main
git tag v0.1.1
git push origin v0.1.1
```

**Require commit on main** 用 `git merge-base --is-ancestor` 检查该提交。尚未合并进 `main` 的 `dev` 提交会在这一步失败，后续的检查、打包、GHCR 推送和 GitHub Release 都不会执行。通过后复用 CI，构建二进制包，分别在两个架构的 runner 上运行压缩包中的程序，再推送多架构 GHCR 镜像并创建 GitHub Release。Release 说明包含 `Released from: main` 和对应提交号。

二进制和镜像注入相同的版本、提交号和提交时间。镜像标签包括版本原文（`v0.1.1`）、完整提交号（`sha-...`），稳定版本另更新 `latest`；预发布不会更新 `latest`，GitHub Release 标记为 prerelease。部署推荐固定版本。不要移动已发布标签；需要修正时发布新版本。这条分支限制写在被标记提交自己的工作流里，合并到 `main` 之后才会约束新的标签。

Actions 通过内置 `GITHUB_TOKEN` 发布：镜像任务有 `packages: write`，Release 任务有 `contents: write`，验证任务仅请求读权限。仓库或组织需允许相应权限。Actions 依赖固定到提交 SHA，Dependabot 每月检查工作流依赖更新。Fork 发布时镜像名自动取该仓库的小写路径，部署 `.env` 的 `WORKBENCH_IMAGE` 需对应修改。

推送镜像与创建 Release 是两个外部步骤，不具备跨服务事务；若最后一步失败，查看 Actions 日志后处理已产生的产物。工作流发布产物后，由服务器执行拉取与重启完成升级。

## 日志与排障

在设置 → 数据与运行中保存日志等级，支持 debug/info/warn/error，立即生效且重启保留。Compose 的 `WORKBENCH_LOG_LEVEL` 和二进制的 `-log-level` 只提供未保存设置时的初始值。服务日志为 JSON，输出到 stderr，容器通过 `docker compose logs -f --tail 100 workbench` 查看，systemd 使用 `journalctl -u workbench -f`。Compose 已配置每份 10 MB、最多 3 份的日志轮转；二进制文件重定向需自行轮转。详细分级和字段见[运行日志](configuration.md#运行日志)。
