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

`./scripts/build.sh` 从锁文件安装前端依赖，构建前端，再输出根目录 `workbench` 可执行文件。仅重新编译已有产物时可使用 `go build -o workbench ./cmd/workbench`。部署、工作空间和备份操作见[配置与运行](../doc/configuration.md)。

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

`investment-terminal.e2e.cjs` 在 Chromium 中加载仓库内的终端代码和实际 TradingView 库，验证账户初始化、持仓与订单渲染、订单标的跳转、账户切换及断线重连；打开原生下单面板，检查有效期/交易时段的名称、可选值及已有订单的回填，并检查浏览器异常。Schwab HTTP 和 WebSocket 请求全部使用虚构响应，无需启动工作台、配置凭据或提交交易。

```bash
PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  node scripts/investment-terminal.e2e.cjs
```

脚本需要访问 TradingView 静态资源站，带内容哈希的库文件缓存在系统临时目录 `workbench-tv-test-cache/`。截图写入 `/tmp/workbench-investment-account-manager.png` 和 `/tmp/workbench-investment-order-ticket.png`。终端 JS 通过 Go embed 编入程序；修改后需重新编译并启动工作台进程，再刷新浏览器。

### Schwab 跨站 OAuth 返回

`TestSchwabOAuthBrowser` 启动临时 HTTPS 工作台和不同站点的模拟 Schwab，调用 `schwab-oauth.e2e.cjs` 验证点击 Done、服务端换取令牌、自动返回投资，以及 Strict 会话 Cookie 恢复。它使用真实认证和回调处理器，工作台页面为最小测试页面，不访问真实券商。普通 Go 测试默认跳过此项，显式运行：

```bash
WORKBENCH_BROWSER_TEST=1 \
  PLAYWRIGHT_MODULE=/tmp/workbench-e2e/node_modules/playwright \
  CHROMIUM_PATH=/usr/bin/chromium \
  go test -race ./internal/modules/investment -run '^TestSchwabOAuthBrowser$' -v
```
