# 外部示例模块

独立 HTTP 服务，实现 Workbench 外部模块协议 v1。它不导入 `workbench/internal/...`，也不会编进核心二进制。

## 启动

在仓库根目录：

```bash
go run ./examples/external-module -listen 127.0.0.1:8091 -token example-token
```

或先编译：

```bash
go build -o /tmp/workbench-example-external ./examples/external-module
/tmp/workbench-example-external -listen 127.0.0.1:8091 -token example-token
```

当前版本只从项目代码和文件注册新业务模块，已移除设置页的网址接入入口及 `POST /api/modules/external`。本示例保留用于旧工作空间外部模块的兼容测试。

已有 `demo_external` 注册记录的工作空间仍可启停、修改设置并访问 `/apps/demo_external/overview`。Go 兼容测试通过内部 `Registry.Attach` 建立 fixture，不提供运行时注册新模块的公开入口。

## 测试参数

| 参数 | 作用 |
|---|---|
| `-delay-state 6s` | `PUT /_workbench/state` 超时，宿主应返回 202 pending |
| `-stale-generation` | 状态响应使用旧代数 |
| `-fault` | 状态应用返回 500 |
| `-title 新标题` | 仅改示例页面文案，用于验证不重建宿主即可更新 |

修改 `ui/index.html` 后重启本进程，刷新 Workbench 页面即可看到新内容；不要重启或重建 Workbench。
