# 平台装配与运行

`app.go` 构造数据库、平台能力和业务模块，组装 Registry、调度、事件投递、鉴权、AI、HTTP 与前端资源，管理 HTTP 服务的运行和退出，TLS 由外部反向代理终止；同时提供模块启停、外部模块接入/代理、备份和维护任务等平台入口。内置模块的额外路由通过路由契约遍历；Registry 统一负责生命周期恢复、启停与关闭，不按投资 ID 分支。初始化后续步骤失败时先关闭 Registry，再释放数据库。

`config.go` 定义默认启动配置与外部 HTTPS 地址校验，命令行和环境变量由 `cmd/workbench` 读取。`settings.go` 管理 AI、外观的持久化及运行时更新；密码变更由鉴权服务处理。

`logging.go` 管理日志设置保存、HTTP 分级访问日志和 panic 恢复。应用、调度、事件投递、认证及投资流连接共享 `foundation/logging` 的动态等级，设置保存后立即生效并在下次启动恢复；中间件覆盖认证拒绝并保留 SSE/WebSocket 能力。

业务规则放在 `internal/modules/<id>/`。新增模块需要在本包构造并加入注册清单。参考[架构](../../doc/architecture.md)、[配置与运行](../../doc/configuration.md)和[开发指南](../../doc/business-development.md)。
