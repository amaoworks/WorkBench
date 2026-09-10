# 平台装配与运行

`app.go` 构造数据库、平台能力和业务模块，组装 Registry、调度、事件投递、鉴权、AI、HTTP 与前端资源，管理运行和退出；同时提供模块启停、备份和维护任务等平台入口。

`config.go` 定义默认启动配置与基础校验，命令行和环境变量由 `cmd/workbench` 读取。`settings.go` 管理 AI、外观的持久化及运行时更新；密码变更由鉴权服务处理。

业务规则放在 `internal/modules/<id>/`。新增模块需要在本包构造并加入注册清单。参考[架构](../../doc/architecture.md)、[配置与运行](../../doc/configuration.md)和[开发指南](../../doc/business-development.md)。
