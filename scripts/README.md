# 构建和验证脚本

## 常规检查

在仓库根目录执行：

```bash
./scripts/test.sh
```

脚本按顺序检查 sqlc 重新生成前后的平台/全部业务生成目录、运行 Go 测试、前端及终端 JS lint、投资适配器 Node 测试和生产构建。它不要求工作区无未提交改动，也不自动运行浏览器测试。

`SQLC_BIN` 可指定 sqlc 1.31.x 路径；未设置时查找 `.tools/bin/sqlc`，再使用 PATH。`GO_BIN` 可指定 Go 可执行文件。首次运行前先在 `web/` 执行 `npm ci`。部分 Go 测试使用本机临时 HTTP 服务，测试环境须允许监听 loopback 端口。

```bash
SQLC_BIN=/path/to/sqlc ./scripts/test.sh
```

生成结果漂移时脚本会保留新结果并失败，核对 SQL 和生成差异后再验证。生产前端产物也会更新到 `internal/webui/dist/`。

需要检查 Go 并发访问时，在允许监听本机临时端口的环境执行 `go test -race ./...`。这会运行全项目测试及 race detector，包括投资连接重建、配置替换和 WebSocket 关闭场景。

## 构建

`./scripts/build.sh` 从锁文件安装前端依赖，构建前端，再通过 `compile.sh` 输出根目录 `workbench` 静态可执行文件。`compile.sh` 支持 `OUTPUT`、`GOOS`、`GOARCH`、`VERSION`、`COMMIT`、`BUILD_DATE`，并强制禁用 CGO；时区数据由程序内嵌。仅重新编译已有产物时可使用 `go build -o workbench ./cmd/workbench`。部署、工作空间和备份操作见[配置与运行](../doc/configuration.md)。

## 工作流检查

修改 GitHub Actions 时执行：

```bash
./scripts/check-workflows.sh
```

需要 Go 和 ShellCheck；脚本使用固定版本 actionlint，并检查工作流中的 shell 脚本及构建辅助脚本。可通过 `ACTIONLINT_BIN`、`SHELLCHECK_BIN` 指定本地工具。缺少 ShellCheck 时直接失败，不能把跳过 shell 检查的结果当作 CI 验证通过。

## 发布和部署验证

`VERSION=v0.1.1 ./scripts/release.sh` 重新构建前端并生成 Linux amd64、arm64 压缩包、部署文件包及 SHA256 校验文件，输出到 `dist/release/v0.1.1/`。版本号需符合 `vMAJOR.MINOR.PATCH`，可带预发布后缀。Actions 的版本、权限、产物和部署步骤见[部署与发布](../doc/deployment.md)。

主分支推送或在 Actions 手动运行 **CI and Build** 会生成可下载的二进制和 Docker 镜像文件，保留在运行的 Artifacts 中 14 天。`scripts/export-build.sh` 从已构建的本地镜像提取二进制、执行 `docker save`，附带部署包和 SHA256 校验文件，输出到 `dist/build/`：

```bash
IMAGE=workbench:local VERSION=v0.0.0-dev.1 ARCH=amd64 ./scripts/export-build.sh
```

`ARCH` 必须与镜像架构一致。镜像文件可用 `docker load --input` 导入；导出的二进制与镜像内程序相同。正式标签发布仍使用 `release.sh` 和 Release 工作流。

`deployment-smoke.py` 仅依赖 Python 3 标准库，使用随机端口、临时密码和独立工作空间，验证首次初始化、登录、健康探针、在线备份、退出，以及重建后的数据和凭据保留。容器模式还检查非 root 用户和只读根文件系统；测试结束清理自己的 Compose 项目和临时卷。

```bash
./scripts/build.sh
python3 scripts/deployment-smoke.py --binary ./workbench
docker build -t workbench:smoke .
python3 scripts/deployment-smoke.py --image workbench:smoke
```

`internal/app/proxy_test.go` 使用真实的本机 HTTPS 代理转发到 HTTP 后端，检查 Secure Cookie、登录、来源与 Host 拒绝、CSRF、SSE 和代理头伪造。普通 Go 测试会执行此项。浏览器 OAuth 回归见下文；全部服务使用临时数据和模拟凭据。

## 浏览器回归

浏览器脚本使用 Playwright 的 chromium API，需要可解析的 Playwright 包与 Chromium。可通过 `PLAYWRIGHT_MODULE` 指定包的绝对路径，`CHROMIUM_PATH` 指定浏览器可执行文件；未指定时使用 Node 模块解析和 Playwright 默认浏览器。

可将测试依赖安装到临时目录，避免修改项目依赖：

```bash
npm install --prefix /tmp/workbench-e2e playwright
```

下面示例假定浏览器位于 `/usr/bin/chromium`；按本机实际路径调整。运行前先构建最新前端，再编译测试程序：

```bash
npm --prefix web run build
go build -o /tmp/workbench-e2e-bin ./cmd/workbench
```

### 模块、总览和分页

`module-platform.e2e.cjs` 覆盖模块启停及接口门控、总览显隐/排序/尺寸持久化、超过一页的待办、投资未连接状态、Schwab 配置保存/密钥不回显/凭据变更校验和移动端溢出检查。它连接已有服务并写入测试凭据和数据，使用全新的临时工作空间、local 模式且关闭 AI：

```bash
WORKBENCH_E2E_DATA_DIR=$(mktemp -d /tmp/workbench-platform.XXXXXX)
OPENAI_API_KEY= WORKBENCH_ALLOWED_HOSTS= /tmp/workbench-e2e-bin \
  -data "$WORKBENCH_E2E_DATA_DIR/data.db" -auth local -listen 127.0.0.1:18086
```

保持服务运行，在另一终端执行：

```bash
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium WORKBENCH_TEST_URL=http://127.0.0.1:18086 \
  node scripts/module-platform.e2e.cjs
```

测试后停止该服务，再清理本次临时目录。截图写入 `/tmp/workbench-platform-dashboard.png`、`/tmp/workbench-platform-mobile.png` 和 `/tmp/workbench-schwab-settings-mobile.png`。投资使用虚构的应用凭据验证本地设置，不发起真实 OAuth 或券商交易。

### 外部模块

`scripts/external-module.e2e.cjs` 仅用于已经保存 `demo_external` 注册记录的旧测试工作空间，覆盖深链接刷新、命令面板、停用后设置和桌面/移动视口；不再尝试网址接入，缺少旧记录时明确失败。旧记录 fixture 与独立进程兼容由 Go 测试中的 `Registry.Attach` 建立。

`scripts/external-module-process.sh` 验证公开网址注册返回 404、未创建模块以及宿主文件哈希保持不变。示例启动步骤见 [examples/external-module/README.md](../examples/external-module/README.md)。

### Futu 与夜盘设置

`futu-settings.e2e.cjs` 自行启动临时工作空间，验证网址注册已移除、Futu 账号密码保存与不回显、夜盘依赖及停用联动、Futu 设置归属投资模块、部署地址不能在页面修改，以及桌面/手机布局。使用虚构凭据和未连接的本机 OpenD，不登录真实券商。

```bash
go build -o /tmp/workbench-futu-e2e-bin ./cmd/workbench
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  node scripts/futu-settings.e2e.cjs
PYTHONDONTWRITEBYTECODE=1 python3 futu-opend/manage_test.py
```

先按上文构建前端。`WORKBENCH_BIN` 可指定测试程序；`WORKBENCH_TEST_MANAGED=false` 验证原生模式从页面启停模拟 OpenD 并回收进程，默认验证共享目录模式。截图写入 `/tmp/workbench-investment-futu-{managed,native}-{1440,390}.png`。Python 测试单独验证配套 OpenD 的配置更换与进程启停。

### Wallos 和待办交互

`wallos.e2e.cjs` 自行创建并清理临时数据库及模拟 Wallos 服务，启动指定的工作台程序，覆盖模块设置、密钥不回显、同步去重、完成/删除/重建、弹窗焦点、桌面/移动端布局与停用门控。

```bash
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium WORKBENCH_BIN=/tmp/workbench-e2e-bin \
  node scripts/wallos.e2e.cjs
```

该脚本使用工作台端口 18137，`WORKBENCH_BIN` 默认是 `/tmp/workbench-wallos-test`。截图写入 `/tmp/wallos-settings-1440.png` 和 `/tmp/wallos-settings-390.png`。外部金额和汇率行为另由 Todo 包中的测试覆盖。

投资适配器的 Node 行为测试位于 `internal/modules/investment/chart-tests/`，运行 `cd web && npm run test:investment`。`npm run lint` 同时检查嵌入 Go 的终端 JS；完整检查入口仍是 `./scripts/test.sh`。测试使用模拟响应与本机 WebSocket，不提交真实券商订单。

### 投资账户管理器

`investment-terminal.e2e.cjs` 在 Chromium 中加载仓库内的终端代码和实际 TradingView 库，验证连续切换主题后图表颜色、实例、标的和周期，以及账户初始化、持仓与订单渲染、订单标的跳转、账户切换及断线重连；打开原生下单面板，检查有效期/交易时段的名称、可选值及已有订单的回填；验证首次加载和刷新不重播历史订单通知，新的成交仍推送一次。Schwab HTTP 和 WebSocket 请求全部使用虚构响应，无需启动工作台、配置凭据或提交交易。

```bash
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  node scripts/investment-terminal.e2e.cjs
```

脚本需要访问 TradingView 静态资源站，带内容哈希的库文件缓存在系统临时目录 `workbench-tv-test-cache/`。截图写入 `/tmp/workbench-investment-account-manager.png` 和 `/tmp/workbench-investment-order-ticket.png`。终端 JS 通过 Go embed 编入程序；修改后需重新编译并启动工作台进程，再刷新浏览器。

`investment-page.e2e.cjs` 自行启动使用临时数据库的工作台，模拟已连接的 Schwab 设置并嵌入带状态标记的测试终端。它验证连续切换主题、图表加载中切换和跟随系统时的颜色同步，以及页面铺满/恢复、浏览器全屏/退出、新窗口、手机尺寸和全屏失败后的恢复，并确认主题和显示模式切换不重建图表。使用端口 18138，无需券商凭据；截图写入 `/tmp/workbench-investment-expanded-mobile.png`。

```bash
go build -o /tmp/workbench-investment-page-test ./cmd/workbench
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  node scripts/investment-page.e2e.cjs
```

运行前需按上文构建最新前端；`WORKBENCH_BIN` 可指定其他测试程序路径。

### Schwab 跨站 OAuth 返回

`schwab-reauthorization.e2e.cjs` 自行启动端口 18139 的临时工作空间，模拟连接故障、授权失效和 OAuth 页面，验证连接错误可重试、失效提示及时出现且刷新后保留、手机铺满布局、设置及总览入口，以及重新授权后恢复投资终端。无需券商账号；后端失效分类、持久状态、401 刷新和不重放交易由 `schwab_auth_test.go` 使用本机模拟服务验证。

```bash
npm --prefix web run build
go build -o workbench ./cmd/workbench
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  node scripts/schwab-reauthorization.e2e.cjs
```

`WORKBENCH_BIN` 可指定其他测试程序；手机截图写入 `/tmp/workbench-schwab-reauthorization-mobile.png`。

`TestSchwabOAuthBrowser` 启动临时 HTTPS 反向代理、HTTP 工作台后端和不同站点的模拟 Schwab，调用 `schwab-oauth.e2e.cjs` 验证点击 Done、服务端换取令牌、自动返回投资，以及 Strict 会话 Cookie 恢复。它使用真实认证和回调处理器，工作台页面为最小测试页面，不访问真实券商。普通 Go 测试默认跳过此项，显式运行：

```bash
WORKBENCH_BROWSER_TEST=1 \
  PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  go test -race ./internal/modules/investment -run '^TestSchwabOAuthBrowser$' -v
```

## 日志设置验证

后端测试覆盖动态等级过滤、共享子 Logger、并发切换、配置持久化、CSRF、非法输入、保存失败、认证拒绝日志和 panic 恢复。`deployment-smoke.py` 同时验证未登录不能改等级，以及二进制/Compose 重启后保留等级并覆盖环境初始值。

在一次性 local 模式工作空间运行 `logging.e2e.cjs`，验证四档切换、保存、刷新恢复与移动端布局，最后恢复 info：

```bash
WORKBENCH_TEST_URL=http://127.0.0.1:18086 node scripts/logging.e2e.cjs
```

`PLAYWRIGHT_MODULE`、`CHROMIUM_PATH` 可指定已安装的 Playwright 和 Chromium，复用上述浏览器测试环境。勿对正式工作空间运行该脚本。
