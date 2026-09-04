# Internal packages

- `foundation/`：数据库、鉴权、可靠事件和模块注册中心。
- `capabilities/`：跨业务模块复用的 AI、对话、Dashboard、通知和调度能力。
- `modules/`：独立业务模块。模块之间禁止直接 import。

业务模块只能通过公开契约、事件或通用能力协作。

