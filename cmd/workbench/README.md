# 可执行入口

`main.go` 读取启动参数、环境变量和操作系统退出信号，支持版本查询与 HTTP 健康探针，初始化日志，通过 `internal/app` 创建、运行并关闭工作台。

`-log-level` / `WORKBENCH_LOG_LEVEL` 设置初始日志最低等级（默认 info）；工作空间内已保存的等级优先。运行日志及启动失败以 JSON 写入 stderr，正常 `-version` 输出仍写入 stdout。

模块清单和服务装配在 `internal/app`，具体业务在 `internal/modules`。程序仅监听 HTTP，HTTPS 由反向代理提供，`-public-url` 指定浏览器访问来源。部署和发布见[部署与发布](../../doc/deployment.md)，启动配置见[配置与运行](../../doc/configuration.md)。
