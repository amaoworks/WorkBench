# Workbench Agent 指南 (Agent Guidelines)

Workbench 是本地优先、单用户的个人工作台，采用 Go 模块化单体、内嵌 React SPA 与共享数据库工作空间；原生运行默认 SQLite，Docker Compose 默认 PostgreSQL，两者通过启动配置选择。
在修改或扩展本代码库前，请务必遵守以下架构规则、命令规范与绝对红线。

---

## 一、核心技术栈与关键依赖

* **后端**：Go 1.27.1，SQLite 使用 `modernc.org/sqlite`，PostgreSQL 使用 `pgx` 的 `database/sql` 适配，`sqlc` v1.31.x。
* **前端**：Node.js 24+，npm 11+，React 19，TypeScript，Vite，Tailwind CSS，Radix UI，TanStack Query，Zod。
* **构建与嵌入**：前端产物输出至 `internal/webui/dist/`，通过 `embed.FS` 编入单一二进制文件。

---

## 二、架构原则与绝对红线 (Critical Invariants)

### 1. 严格的模块隔离与唯一形态
* 业务模块严格对应 `internal/modules/<id>/` 与 `web/src/modules/<id>/` 两个目录。新业务一律采用进程内模块，严禁使用外部模块模式（外部模块代码仅用于历史数据兼容）。
* **严禁跨业务模块直接 `import` 实现代码**（例如 `todo` 模块绝对不能直接导入 `investment`）。
* 跨模块通信必须通过 [internal/contracts](internal/contracts) 声明的抽象接口，或通过事件总线（`EventPublisher.PublishTx` / `ModuleRegistrar.Consume`）解耦。
* 通用能力（AI、调度、通知、总览）只通过 `contracts` 接口向业务暴露，业务不直接依赖 `internal/capabilities` 具体实现。

### 2. 拒绝过度设计与过度防御 (KISS / YAGNI)
* **精准实现，不过度泛化**：严格按当前需求编写具体逻辑，坚决不预留臆想的扩展层、不编写无调用的空壳包装、不预先创建空目录。
* **避免过度防御**：不添加冗余的包装层或臆想的重试与兜底逻辑，不编写防御性务虚注释；专注于清晰的业务路径与明确的错误返回。

### 3. 数据库设计与事务控制
* 平台和内置业务共享所选数据库及其 `*sql.DB` 连接池，业务连接默认最多 4 个。业务表名使用 `<module_id>_` 前缀（如 `todo_tasks`）。
* **成对双迁移**：SQLite 迁移放在 `migrations/`，PostgreSQL 迁移放在同包的 `migrations-postgres/`；两者必须使用相同迁移版本号成对添加。
* **单引擎 SQL 生成**：固定查询集中在归属包的 `query/`，仅通过 SQLite engine 的 `sqlc generate` 统一生成代码（切勿修改 `sqlc.yaml` 引入 postgres 引擎）。PostgreSQL 驱动在运行时自动将 `?` 占位符转换为 `$N`。
* 业务写入及其对应事件通过 `PublishTx` 在同一个事务 (`*sql.Tx`) 中提交；通知与业务写入需要原子提交时使用 `CreateTx`。独立事件或提醒可使用 `Publish` / `Create`，由服务管理自身事务。
* 同一数据库只运行一个工作台进程。SQLite 使用 `<db_path>.lock` 文件锁；PostgreSQL 使用独立会话持有同一数据库和 schema 范围的 advisory lock。PostgreSQL 默认事务使用 Serializable 隔离级别，序列化冲突会回滚并返回错误。

### 4. 文案与文档编写规范
* 严格执行 [doc/copywriting-guidelines.md](doc/copywriting-guidelines.md)。
* **拒绝元叙事与防御性声明**：直接描述设计与接口，禁止在正文中添加辩解性、务虚的废话（如“以当前需求为准、不构成路线图”）。
* **高信噪比与工具化**：界面标题采用纯净名词，表单占位符内化，不追加解释显而易见常识的 `<small>` 提示行，操作按钮使用紧凑动宾短语。

### 5. 前端类型与注册约定
* 共享 `api` 返回 `unknown`，禁止裸泛型断言；消费 JSON 响应必须使用 `apiValidated(url, schema)` 或 `schema.parse(await api(url))`。业务 schema 放在模块内的 `schema.ts`，平台共享 schema 放在共享目录。
* 内置业务前端在 `web/src/modules/<id>/<id>.module.ts` 声明实际提供的资源，由平台 Registry 装配。`pages`、`widgets` 字段可为空映射，`settings`、`icons` 可选；禁止在外壳层强耦合业务组件。
* 界面组件、页面状态、控件选型、可达性与响应式遵循 [doc/frontend-guidelines.md](doc/frontend-guidelines.md)，优先复用共享组件，不重复实现交互。

---

## 三、常用开发与验证命令

| 命令 | 用途 | 适用场景 |
|---|---|---|
| `./scripts/test.sh` | **仓库标准基准检查** | **日常开发核心闭环（耗时约 15s）**：自动检查 sqlc 漂移、Go 测试、前端/终端 lint、契约测试与前端构建；通过即代表满足合入标准 |
| `./scripts/dev.sh -auth password -listen 127.0.0.1:9090` | **本地前后端联调** | 启动后端服务与 Vite 开发服务器（支持热更新与代理配置） |
| `sqlc generate` | **重新生成 SQL 代码** | 只要修改了 `migrations/` 或 `query/*.sql`，必须运行此命令 |
| `go test ./...` | **Go 单元测试** | 仅验证后端测试用例 |
| `npm run lint --prefix web` | **前端代码规范检查** | 检查 ESLint 规范 |
| `npm run build --prefix web` | **前端生产构建** | 生成前端静态产物至 `internal/webui/dist/` |

> [!NOTE]
> `scripts/*.e2e.cjs` 为重型端到端浏览器回归（需外置 Playwright/Chromium 环境），**日常开发与自动化检查无需全量运行**，仅在专门排查对应端到端链路时针对单项执行。

---

## 四、功能开发标准工作流

按本次修改涉及的职责选择步骤和文件，不需要修改的文件不创建、不改动。

1. **调整数据模型或查询**：
   * 涉及表结构变更时，成对在平台或业务所属的 `migrations/` 与 `migrations-postgres/` 中添加同版本迁移。
   * 固定查询放在平台或业务所属的 `query/`。
   * 新增生成目标时在 [sqlc.yaml](sqlc.yaml) 登记路径；修改 SQL 后运行 `sqlc generate` 并提交生成结果。
2. **实现后端服务**：
   * 新增业务模块时实现 [contracts.Module](internal/contracts/module.go)（声明 Manifest、Migrations 和 Register）。
   * 构造函数显式接收依赖，新增模块在 [internal/app/app.go](internal/app/app.go) 的初始化列表中注册。
   * 按变更行为维护对应测试，不编写多余的包装层。
3. **实现前端功能**：
   * 按需在 `web/src/modules/<id>/` 添加业务组件；接口函数和查询 hooks 放在 `queries.ts`，响应解析使用 `schema.ts` 校验。
   * 在 `<id>.module.ts` 注册实际提供的资源，无页面或 Widget 时保留对应空映射。
4. **闭环自检**：
   * 代码或 SQL 变更后运行 `./scripts/test.sh`，确认基准检查一次性通过。
   * 纯文档修改检查链接、格式和说明与实现的一致性。

---

## 五、核心文档地图与深入参考

* **系统架构与目录分层**：[doc/architecture.md](doc/architecture.md)
* **业务模块开发 SOP**：[doc/business-development.md](doc/business-development.md)
* **跨层接口与契约规范**：[doc/contracts.md](doc/contracts.md)
* **数据库模型与维护**：[doc/data-model.md](doc/data-model.md)
* **文案与文档规范**：[doc/copywriting-guidelines.md](doc/copywriting-guidelines.md)
* **前端界面规范**：[doc/frontend-guidelines.md](doc/frontend-guidelines.md)
* **部署与运维发布**：[doc/deployment.md](doc/deployment.md)
* **配置参数与运行日志**：[doc/configuration.md](doc/configuration.md)
* **安全边界与鉴权模型**：[doc/security.md](doc/security.md)
