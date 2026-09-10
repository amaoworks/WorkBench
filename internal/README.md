# 后端代码

后端按三层组织：`modules/` 是业务应用层，`capabilities/` 是通用能力层，`foundation/` 是基础设施层。

- `modules/`：每个业务拥有自己的目录、数据、HTTP、Job、事件和工具；模块间通过契约或事件协作。
- `capabilities/`：AI、对话、通知、调度和总览等复用能力。
- `foundation/`：数据库、鉴权、可靠事件、注册目录，以及 HTTP/ID 工具。
- `contracts/`：跨层接口和资源描述类型。
- `app/`：依赖装配、平台路由、设置和生命周期。
- `webui/`：嵌入前端构建产物和静态资源处理。

完整目录、实际依赖方向和运行流程见[架构说明](../doc/architecture.md)。新增业务见[开发指南](../doc/business-development.md)，功能调整以当前需求和实现为准。
