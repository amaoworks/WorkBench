# 项目文档

这里描述 Workbench 当前代码实现、模块边界和开发方式。代码、数据库迁移与可执行测试是核对行为的依据；发现文档与实现不符时，应核对需求并同步修正文档。

后续开发以当前需求为准。这些文档说明现状，不构成固定路线图，也不要求延续早期阶段的范围、目标或实施顺序。修改架构时同步更新相应说明，不另保留一套相互冲突的现行设计。

| 文档 | 内容 |
|---|---|
| [架构与目录](architecture.md) | 三层职责、模块归属、依赖方向、启动与运行流程 |
| [业务开发指南](business-development.md) | 模块结构、后端装配、前端注册、能力接入与验证 |
| [接口与契约](contracts.md) | Module、事件、调度、AI、通知、Widget 和 HTTP 入口 |
| [数据设计](data-model.md) | 表归属、迁移、事务、生成查询、备份与维护 |
| [部署与发布](deployment.md) | 二进制、Compose、systemd、反向代理、升级与 GitHub Actions |
| [配置与运行](configuration.md) | 构建、启动参数、设置、开发服务、备份恢复 |
| [安全边界](security.md) | 当前鉴权、请求保护、凭据和外部服务处理 |
| [Todo](modules/todo.md) | 待办、分页、提醒、AI Tool 和代码入口 |
| [Wallos 联动](modules/todo-wallos.md) | Todo 内的订阅同步、账期去重与金额说明 |
| [Investment](modules/investment.md) | Schwab 行情、持仓、订单和 TradingView 终端 |

根 [README](../README.md) 面向使用和启动，各代码目录中的 README 说明本目录职责。新增或调整功能时更新对应主题及入口链接；不把临时执行计划、完成清单和一次性验收记录作为长期开发依据。

## 相关说明

- [外部业务模块第一阶段](external-modules-phase-1.md)：协议、执行记录和验收对照。现行接口以本目录主题文档及代码为准。
