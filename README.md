# Workbench

Workbench 是一个本地优先、单用户的个人工作台。项目采用 Go 模块化单体、React 前端和单文件 SQLite 数据库，目标部署形态是“一个可执行文件 + 一个数据库文件”。

MVP 基础能力已落地；当前继续提供模块管理、总览布局配置和 Investment 模拟行情验证模块。扩展范围与验收见 [业务扩展记录](doc/module-platform.md)。开发环境需要 Go 1.27.x、Node.js 24+、npm 11+ 与 sqlc 1.31.x；只构建已检入的生成产物时不要求安装 sqlc。

- 业务开发指南：[doc/business-development.md](doc/business-development.md)
- MVP 文档入口：[doc/mvp/README.md](doc/mvp/README.md)
- 总体架构：[doc/mvp/architecture-v1.2.md](doc/mvp/architecture-v1.2.md)
- 核心契约：[doc/mvp/contracts/core-contracts.md](doc/mvp/contracts/core-contracts.md)
- 数据设计：[doc/mvp/database/schema.md](doc/mvp/database/schema.md)
- 配置与运行：[doc/mvp/configuration.md](doc/mvp/configuration.md)
- 安全威胁模型：[doc/mvp/security-threat-model.md](doc/mvp/security-threat-model.md)
- 实施与验收清单：[doc/mvp/implementation-checklist.md](doc/mvp/implementation-checklist.md)

## 目录

```text
cmd/workbench/          可执行文件入口
internal/app/           依赖装配与应用生命周期
internal/contracts/     各层共享的稳定接口和描述类型
internal/foundation/    数据库、鉴权、事件、模块注册等底座
internal/capabilities/  AI、调度、通知、对话、总览等通用能力
internal/modules/       待办、投资等业务模块
internal/webui/         内嵌前端产物及其 HTTP Handler
web/                    React + TypeScript 前端
scripts/                开发、构建和发布脚本
doc/mvp/                MVP 架构与决策文档
```

## 构建与运行

默认使用当前机器 `PATH` 中的 `go` 执行测试和编译。

```bash
./scripts/test.sh
./scripts/build.sh
./workbench
```

默认监听 `127.0.0.1:8080`，数据保存在 `~/.workbench/data.db`。启动时可通过 `-listen` 指定地址和端口：

```bash
./workbench -listen 127.0.0.1:9090
```

首次启用密码模式时，密码长度必须至少 8 个 Unicode 字符，且大写字母、小写字母、数字、特殊符号四类中至少包含三类。特殊符号包括标点和符号，空白不计入该类。请将示例密码替换为自己的密码：

```bash
WORKBENCH_PASSWORD='MySecret1!' ./workbench -auth password -listen 127.0.0.1:9090
```

非 loopback 监听必须同时提供 `-tls-cert` 与 `-tls-key`。AI 为可选能力，设置 `OPENAI_API_KEY` 后启用；模型可通过 `OPENAI_MODEL` 调整。

使用通配监听或域名访问时，以可重复的 `-allowed-host host:port`（或逗号分隔的 `WORKBENCH_ALLOWED_HOSTS`）明确允许浏览器发送的 Host；例如 `-listen 0.0.0.0:8443 -allowed-host workbench.lan:8443`。自定义兼容端点可通过受信任的启动环境变量 `OPENAI_BASE_URL` 设置。

## 设置页面

登录后从侧栏进入「设置」（`/settings`），通过标签在同一内容区域切换配置，切换时保留未提交的表单输入：

- **业务模块**：启用/停用已编译业务；自动更新导航和总览，保留历史数据与布局偏好。
- **AI 配置**：AI 开关、接口地址、模型与 API Key；支持保存前测试连接，保存后新请求立即使用新配置。
- **账户安全**：验证当前密码后修改密码，所有已有登录失效，需重新登录。
- **外观**：日光、夜幕、跟随系统，以及减少动态效果；自动保存到工作空间数据库。
- **数据与运行**：创建经过完整性检查的在线备份，查看监听地址、数据路径、认证模式和 Host 配置。

AI 环境变量作为尚未保存页面设置时的初始值；在页面保存 AI 配置后，数据库中的设置优先，重启不会被环境变量覆盖。API Key 仅保存、不回显，更换接口地址需要重新填写密钥。接口须兼容 Responses API，测试会发送一条简短请求，可能产生少量费用。

端口、数据库路径、认证模式、TLS 和允许访问的 Host 仍使用启动参数管理，修改后需要重启。数据库及其备份含已保存的 API Key，应按敏感文件保管。升级到此版本后，已有密码登录会话需要重新登录一次。

## 总览与业务扩展

待办支持 [Wallos 订阅提醒联动](doc/todo-wallos.md)：在待办模块设置中连接自托管 Wallos，每小时同步，将当月应付订阅提前加入去重的续费待办，再按提前天数、小时和时区发送站内提醒。

总览的“配置总览”支持按卡片选择显隐、调整顺序和尺寸、恢复默认，设置保存在工作空间数据库中。隐藏卡片不影响业务运行，禁用业务也不会丢弃布局偏好。页面和卡片由各业务目录的 `*.module.ts` 声明，Shell 自动收集并按需加载。

当前有 Todo 和模拟行情两个业务。模拟行情只使用三个虚构品种，支持定时/手动同步、事务事件、幂等站内提醒，以及主动调用共享 AI 生成并保存摘要。未启用 AI 不影响行情同步和浏览。新增业务仍需重新构建程序，运行时可启停；外部通知渠道仍属于后续扩展。
