# Scripts

开发、构建、测试、打包、备份与恢复脚本目录。脚本不得绕过应用的 migration 或一致性备份流程。


`test.sh` 在重新运行 sqlc 前后比较 Foundation 和所有模块的生成目录，不要求工作区无未提交改动。SQLC_BIN 可指定 sqlc 1.31.x 的路径。

`module-platform.e2e.cjs` 需要 Playwright/Chromium，仅对全新临时工作空间运行，会创建任务并修改模块和总览设置。可配置 PLAYWRIGHT_MODULE、CHROMIUM_PATH、WORKBENCH_TEST_URL；详见 [业务扩展验收](../doc/module-platform.md)。
