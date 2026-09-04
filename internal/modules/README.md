# Business modules

- `todo/`：MVP 端到端验证模块。
- `investment/`：MVP 后的投资模块，最初使用 mock 数据 Provider。

每个模块拥有自己的表前缀、migration、HTTP handler、事件消费者、Job、AITool、Widget 和前端注册项。模块不能读写其他模块的表。

