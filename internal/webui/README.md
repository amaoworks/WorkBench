# 内嵌前端资源

`webui.go` 通过 `go:embed dist/*` 持有生产前端资源，提供静态文件和前端路由的 SPA fallback。未知 `/api/` 路径返回 404。

Vite 从 `web/` 构建，清理并直接输出到 `internal/webui/dist/`，然后编译 Go 程序。产物检入仓库，因此已有产物时可直接编译 Go；修改前端后同时更新构建产物，避免二进制包含旧页面。

构建和测试入口见 [scripts/README](../../scripts/README.md)。
