# Stable contracts

存放跨层使用的最小接口和资源描述类型，例如 Module、Event、Job、AITool、Widget 与 Notification 契约。该包不得依赖 Foundation、Capabilities 或具体业务模块实现，防止循环依赖。

实现时可在不产生环依赖的前提下拆成多个叶子子包，不应演变为杂物 `common` 包。

