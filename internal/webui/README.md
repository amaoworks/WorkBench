# Embedded web UI

该包使用 `go:embed` 持有前端生产构建产物，并提供 SPA 静态资源 Handler。

构建流程：

```text
Vite 从 web/ 构建并清理写入 internal/webui/dist/ → go build
```

API 路径的 404 不得回退到 `index.html`；只有前端路由使用 SPA fallback。生产产物提交到仓库，保证检出源码后 Go 可以直接编译；发布构建仍会从锁文件重新生成并覆盖它们。
