# 可执行入口

`main.go` 读取启动参数、环境变量和操作系统退出信号，初始化日志，通过 `internal/app` 创建、运行并关闭工作台。

模块清单和服务装配在 `internal/app`，具体业务在 `internal/modules`。启动配置见[配置与运行](../../doc/configuration.md)。
