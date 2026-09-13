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

保持 Workbench 已在运行。打开设置 → 业务模块 → 接入外部服务：

- 服务地址：`http://127.0.0.1:8091`
- 服务凭据：`example-token`

接入后默认停用。启用后打开 `/apps/demo_external/overview`，设置页可在停用时继续保存配置。

## 测试参数

| 参数 | 作用 |
|---|---|
| `-delay-state 6s` | `PUT /_workbench/state` 超时，宿主应返回 202 pending |
| `-stale-generation` | 状态响应使用旧代数 |
| `-fault` | 状态应用返回 500 |
| `-title 新标题` | 仅改示例页面文案，用于验证不重建宿主即可更新 |

修改 `ui/index.html` 后重启本进程，刷新 Workbench 页面即可看到新内容；不要重启或重建 Workbench。
