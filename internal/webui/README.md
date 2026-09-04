# Embedded web UI

该包使用 `go:embed` 持有前端生产构建产物，并提供 SPA 静态资源 Handler。

构建流程：

```text
web/ 构建 → 清理并复制到 internal/webui/dist/ → go build
```

API 路径的 404 不得回退到 `index.html`；只有前端路由使用 SPA fallback。`dist/` 当前为空骨架，后续由可复现构建脚本生成。

