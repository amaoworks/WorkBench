# 业务扩展与总览配置补齐

本轮目标：模块管理、独立总览布局、业务自有前端注册、业务主动调用 AI、第二个业务验证，以及开发指南。业务仍采用编译期装配、运行时启停；推送范围为站内通知与 SSE，外部通知渠道沿用后续路线。

## 实施状态

- [x] 设置页模块启停与前端缓存刷新
- [x] 总览卡片独立显隐、顺序、尺寸与持久化
- [x] 页面/Widget 由业务目录声明，Shell 自动收集
- [x] 共享 AI 文本能力与实时设置切换
- [x] Investment 模拟行情：独立表、同步 Job、事件通知、AI 摘要、页面和 Widget
- [x] Todo 游标分页及前端加载更多
- [x] 完整业务开发指南、接口与数据文档同步
- [x] 后端故障/恢复测试、前端检查及浏览器验证

本轮实现与验证已完成。功能测试于 2026-09-05 通过，2026-09-06 完成文档与交付核对。完成项以本文件和对应测试为准。

## 已落地的接口

- `GET /api/modules`、`PUT /api/modules/{id}/enabled`：模块清单与启停。
- `GET /api/dashboard`：仅返回启用且选择显示的卡片。
- `GET /api/dashboard/widgets`：全部已注册卡片及有效布局，包含停用业务。
- `PUT /api/dashboard/layout`：保存完整 `items: [{id, visible, size, order}]`；`DELETE` 恢复默认。
- 布局保存在现有 `workspace_settings` 的 `dashboard` section，无需修改旧 schema。
- `GET /api/modules/investment/overview`：固定模拟品种及已保存摘要；`POST /sync`、`POST /summary` 使用 `{}` 请求。
- 业务通过 `contracts.TextGenerator` 调用共享 AI；不会获得对话 Gateway 的全业务 Tool 权限。

模拟行情为固定的三个虚构品种，非真实行情；外部通知渠道和二进制热加载沿用后续扩展路线。

## 开发入口

[业务开发指南](business-development.md) 包含模块目录、命名与依赖规则、应用装配、数据库事务、事件、Job、AI、站内通知、页面和 Widget 注册、扩展平台能力及验收流程。README、核心契约、数据设计、项目结构和 Web 文档已同步。

## 验收证据

| 要求 | 实现与验证 |
|---|---|
| 模块管理与导航同步 | `ModulesPanel.tsx`；浏览器实际启停模拟行情，检查导航与总览消失/恢复；现有 Registry、Scheduler、Tool、Dispatcher Gate 测试继续通过 |
| 卡片独立显隐、顺序与尺寸 | `dashboard/service.go`、`DashboardPage.tsx`；浏览器隐藏 Todo 卡片后仍可访问待办，修改顺序/尺寸并刷新确认 |
| 布局持久化与默认值 | `TestDashboardLayoutIndependentOfModuleAndSurvivesRestart` 覆盖保存、非法输入不覆盖、CSRF、业务启停、重启和恢复默认 |
| 业务自有前端注册 | 两个业务各有 `*.module.ts`；`modules/registry.ts` 自动收集；生产构建分别生成页面和 Widget 懒加载文件 |
| 主动调用共享 AI | `TestInvestmentUsesLiveAIAndPreservesDataAcrossDisableAndRestart` 覆盖开启设置、生成并保存摘要、重启和关闭 AI 后降级；TextService 测试覆盖无 Tool、错误脱敏、配置切换不打断旧请求 |
| 第二业务完整链路 | Investment 独立 migration/SQL、五分钟 Job、事务事件、幂等提醒、页面与卡片；`TestSyncIsAtomicAndRepeatedSnapshotsDoNotPublishAgain` 验证事件失败回滚、重复快照不重发、重复消费不重复通知 |
| 旧工作空间升级与备份 | `TestUpgradeTodoOnlyWorkspacePreservesDataAndAddsDefaultWidget` 从仅 Todo 的数据库升级，保留业务数据/停用状态/布局，新增默认卡片，并验证在线备份恢复 |
| Todo 分页 | `TestTaskCursorTraversesTiesAndRejectsInvalidInput` 遍历相同排序值及完成状态混合的数据，验证无遗漏/重复和非法游标；浏览器加载 51 条任务跨页 |
| 业务依赖规范 | `internal/modules/architecture_test.go` 检查业务间 import 和对能力实现的直接依赖；SQL 表归属仍由审查保证 |
| 前端实际可用性 | 桌面与 390px 移动端浏览器检查通过，无页面运行错误或横向溢出；截图目视检查通过 |

通过的命令：

```bash
GOCACHE=/tmp/workbench-review-gocache SQLC_BIN=/tmp/workbench-tools/sqlc ./scripts/test.sh
GOCACHE=/tmp/workbench-review-gocache go test ./internal/app ./internal/modules/investment ./internal/modules/todo
GOCACHE=/tmp/workbench-go-cache-race go test -race ./...
GOCACHE=/tmp/workbench-review-gocache go vet ./...
git diff --check
```

`test.sh` 包含 sqlc 再生成一致性、全部 Go 测试、前端 lint 和生产构建；补充的应用测试包含旧工作空间升级与备份恢复。集成测试通过本机临时 HTTP 服务模拟 AI，没有调用真实模型服务。

浏览器测试使用本地 Playwright/Chromium 和全新临时工作空间。可按以下方式复现（需要两个终端；路径按本机安装调整）：

```bash
# 终端一：先构建前端和临时二进制，再启动独立数据目录。
npm --prefix web run build
go build -o /tmp/workbench-platform ./cmd/workbench
platform_check_dir=$(mktemp -d /tmp/workbench-platform-check.XXXXXX)
/tmp/workbench-platform -listen 127.0.0.1:18086 -data "$platform_check_dir/data.db"

# 终端二：这会创建 51 条待办、修改模块和布局，仅对上面的临时实例运行。
PLAYWRIGHT_MODULE=/root/.cache/ms-playwright-go/1.57.0/package \
CHROMIUM_PATH=/root/.cache/ms-playwright/chromium-1208/chrome-linux64/chrome \
WORKBENCH_TEST_URL=http://127.0.0.1:18086 \
node scripts/module-platform.e2e.cjs
```

测试输出：`PASS: module toggles, hidden widgets, order/size persistence, pagination, mock sync, AI unavailable, mobile layout; no page errors`。脚本生成 `/tmp/workbench-platform-dashboard.png` 和 `/tmp/workbench-platform-mobile.png`，不写入业务源码或真实用户工作空间。

## 保留的边界

- 新业务需要构建并装配到程序，运行时开关不会安装未知插件。
- 禁用阻止后续入口，不承诺强制中断已经开始的请求或后台 Handler。
- 卡片尺寸表示响应式网格占列，顺序通过上下移动设置；不提供任意像素位置编辑。
- 当前共享数据库连接不是权限沙箱；外部消息渠道、真实行情和真实交易未在本轮实现。
