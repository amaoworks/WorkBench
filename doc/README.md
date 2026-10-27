# 项目文档

Workbench 技术文档索引：

| 文档 | 内容 |
|---|---|
| [架构与目录](architecture.md) | 三层职责、模块归属、依赖方向、启动与运行流程 |
| [业务开发指南](business-development.md) | 模块结构、后端装配、前端注册、能力接入与验证 |
| [接口与契约](contracts.md) | Module、事件、调度、AI、通知、Widget 和 HTTP 入口 |
| [数据设计](data-model.md) | 表归属、迁移、事务、生成查询、备份与维护 |
| [部署与发布](deployment.md) | 二进制、Compose、systemd、反向代理、升级与 GitHub Actions |
| [配置与运行](configuration.md) | 构建、启动参数、设置、开发服务、备份恢复 |
| [安全边界](security.md) | 鉴权模式、请求保护、凭据与外部服务处理 |
| [文案与文档规范](copywriting-guidelines.md) | 界面文案与技术文档编写规范 |
| [前端界面规范](frontend-guidelines.md) | 顶部导航、通用组件、页面状态、控件、主题、可达性与响应式约定 |
| [前端改版实现](frontend-redesign-backend-requirements.md) | 改版接口、schema、迁移与验证说明 |
| [Todo](modules/todo.md) | 待办、分页、提醒、AI Tool 和代码入口 |
| [Wallos 联动](modules/todo-wallos.md) | Todo 内的订阅同步与账期管理 |
| [Investment](modules/investment.md) | Schwab 行情、持仓、订单、TradingView 终端与价格预警 |
| [资讯](modules/news.md) | RSS/Atom、关注规则、翻译摘要、定时简报和推送 |
| [Telegram 通知](notifications.md) | 价格预警及资讯简报的推送配置、测试与重试策略 |
| [AI 协作指南](../AGENTS.md) | 面向 AI 助手的架构红线、命令速查与开发工作流 |

## 历史归档

- [外部业务模块第一阶段历史记录](archive/external-modules-phase-1.md)（**已废弃归档**）：网址动态接入已取消；现行方式仅为进程内模块（`internal/modules/<id>/`、`web/src/modules/<id>/`），示例见 [`examples/external-module`](../examples/external-module)。已有外部模块记录的兼容管理接口见[接口与契约](contracts.md)。
