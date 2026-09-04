# 项目目录与依赖边界

当前只创建目录和职责说明，不放置占位实现。空的最终代码目录使用 `.gitkeep` 保留。

```text
.
├── cmd/
│   └── workbench/                 # main；只调用 internal/app
├── internal/
│   ├── app/                       # composition root、启动和退出
│   ├── contracts/                 # 无实现的稳定叶子契约
│   ├── foundation/
│   │   ├── auth/                  # 鉴权、Session、请求安全
│   │   ├── database/
│   │   │   └── migrations/        # 可被本包 go:embed 的应用 migration
│   │   ├── events/                # Outbox dispatcher
│   │   └── modules/               # Registry 与 enabled gate
│   ├── capabilities/
│   │   ├── ai/                    # Provider 与 Tool runtime
│   │   ├── conversation/          # 渠道无关对话引擎
│   │   ├── dashboard/             # Widget 聚合
│   │   ├── notifications/         # 通知服务与 SSE
│   │   └── scheduler/             # Job 调度与恢复
│   ├── modules/
│   │   ├── todo/
│   │   │   └── migrations/
│   │   └── investment/
│   │       └── migrations/
│   └── webui/
│       └── dist/                  # 构建复制后由本包 go:embed
├── web/
│   └── src/
│       ├── app/                   # React 应用装配
│       ├── components/ui/         # 基础 UI
│       ├── features/              # Dashboard、通知等通用功能
│       ├── modules/               # 编译期业务页面和 Widget
│       ├── routes/                # pageKey/route 注册
│       └── shared/                # API、Zod schema、共享类型
├── scripts/                       # 可复现的构建、备份和恢复脚本
└── doc/mvp/                       # 架构、契约、schema、ADR 与清单
```

## Go import 规则

允许：

```text
cmd/workbench → internal/app
internal/app → contracts + foundation + capabilities + modules + webui
foundation/* → contracts
capabilities/* → contracts + foundation 的窄接口
modules/* → contracts
webui → Go 标准库
```

禁止：

```text
module A → module B
foundation → capabilities 或具体 module
contracts → 任意实现包
业务 module → chi、OpenAI SDK、gocron 或数据库驱动细节
```

若实际编码时出现循环 import，应缩小契约并移动到叶子包，不得通过全局 service locator 或无边界的 `common` 包绕开。

## 嵌入资源约定

`go:embed` 不能匹配 `..` 路径，因此：

- Foundation migration 放在 `internal/foundation/database/migrations/`。
- 模块 migration 放在模块自己的 `migrations/`，由该模块 package 嵌入。
- `web/` 只保存前端源码；生产构建产物复制到 `internal/webui/dist/` 后执行 `go build`。
- 构建脚本必须先清理旧 dist，再复制新产物，防止删除过的前端资源残留在二进制中。

