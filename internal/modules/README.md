# Business modules

- `todo/`：待办、游标分页、事务事件、到期通知、Widget 与低风险 AI Tool。
- `investment/`：固定模拟行情 Provider、独立表、同步 Job、事件提醒、共享 AI 文本摘要、页面和 Widget。

接入步骤与能力使用规范见 [业务开发指南](../../doc/business-development.md)。每个业务拥有自己的表、migration、SQL、事件、Job 和前端注册项。业务不读写其他业务的表，不直接 import 其他业务或通用能力实现；通过 contracts 和构造函数注入协作。`architecture_test.go` 检查 import 边界。
