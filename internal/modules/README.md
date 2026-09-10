# 业务应用层

每个业务占一个后端目录，并在 `web/src/modules/<id>/` 有对应前端目录。模型、业务操作、HTTP、Job、事件、AI Tool、专属集成和数据定义都留在所属模块内。

| 模块 | 当前职责 | 说明 |
|---|---|---|
| `todo/` | 待办、分页、到期通知、AI 创建工具、Wallos 订阅提醒 | [Todo](../../doc/modules/todo.md)、[Wallos](../../doc/modules/todo-wallos.md) |
| `investment/` | 模拟行情、同步、事件提醒、AI 摘要 | [Investment](../../doc/modules/investment.md) |

`module.go` 聚焦构造、Manifest、迁移声明和注册，其余职责按需拆到同包文件；`migrations/`、`query/`、`sqlc/` 属于各业务。模块不能直接 import 其他业务、能力实现或 app，通过 contracts 和构造注入协作，不读写其他业务的表。

`architecture_test.go` 检查后端业务 import 边界。新增模块结构、能力接入和前端注册见[业务开发指南](../../doc/business-development.md)。
