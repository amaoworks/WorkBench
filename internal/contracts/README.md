# 跨层契约

存放 Module、Event、Job、AITool、TextGenerator、Widget、Notification 和 APIError 等共享接口与资源描述。具体定义以本包 Go 源码为准，使用语义见[接口与契约](../../doc/contracts.md)。

本包只依赖标准库，不依赖 foundation、capabilities、app 或具体业务实现。业务专属模型保留在业务包；需要跨边界协作时设计最小接口或事件载荷，避免把本包变成所有类型的集合。
