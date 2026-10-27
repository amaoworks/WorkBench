# Workbench 前端

React + TypeScript 前端，使用 Vite、Tailwind CSS、Radix UI、Lucide、React Router、TanStack Query 和 Zod。安装版本以 `package-lock.json` 为准。

## 目录与归属

```text
src/app/                 登录、应用装配、顶部导航外壳、路由兜底、主题
src/features/ai/         平台 AI 对话面板
src/features/dashboard/ 总览及布局配置
src/features/notifications/ 通知列表与 SSE
src/features/settings/  模块管理、AI、安全、外观、数据与运行设置
src/modules/registry.ts 编译期业务注册目录
src/modules/<id>/       业务页面、Widget、模块设置、queries.ts、schema.ts
src/components/ui/      通用界面组件
src/shared/             HTTP 客户端、平台共享 schema、工具
src/styles.css          主题变量和共享样式
src/assets/fonts/       本地打包的 Outfit 标题字体（woff2 与 SIL OFL 许可证）
```

业务前端与 `internal/modules/<id>/` 一一对应。业务接口函数、查询 hooks 和缓存刷新放模块内 `queries.ts`，数据校验和推导类型放 `schema.ts`。共享 `api` 返回 `unknown`，读取字段前调用 `apiValidated` 或 schema 的 `parse`；设置类型也由 `features/settings/schema.ts` 推导。页面保留表单与交互状态；平台 shared 不存放 Task 等业务专属模型。业务之间不直接导入彼此实现。

## 模块注册和缓存

`src/modules/<id>/<id>.module.ts` 声明 pages、widgets，以及可选的 icons、settings、`menu`（顶部导航下拉项；目前只有投资声明，空菜单渲染为普通链接）和 `subpages`（挂在 `${route}/${path}` 下的子页面）。平台页面的菜单放在 `src/app/navigation.ts`。Registry 自动收集声明，组件按需加载。后端 `pageKey/widgetKind` 只映射到已编译的本地注册项；Shell 和总览不包含业务组件实现。外部模块不进入该注册表，页面为 `/apps/{id}/{pageName}`，由通用 iframe 容器加载 `/modules/{id}/ui/*` 与设置 `/modules/{id}/settings/*`。未知图标回退到宿主默认图标。

模块设置接收 `{ enabled: boolean }`，停用时不发起业务查询。设置弹窗由平台管理，没有设置组件的模块展示暂无配置内容。

业务 queryKey 以模块 ID 开头，Widget 使用 `[moduleId, "widget", widget.id]`。启停模块后平台取消旧查询、清除对应业务和 Widget 缓存，再刷新模块、总览与 AI 状态；业务写入后刷新实际受影响的查询。分页使用后端 nextCursor。接入示例见[业务开发指南](../doc/business-development.md)。

## 界面约定

完整的组件选型、状态、可达性与响应式要求见[前端界面规范](../doc/frontend-guidelines.md)。

- 图标使用 Lucide。标题使用 PageHeader，卡片和按钮使用共享 Card、Button；按钮形式的站内跳转使用 ButtonLink。
- 配色读取 `styles.css` 的语义变量，兼容明暗主题，尊重系统及工作空间的减少动态效果设置。默认 palette 为 TradingView 风格，可选 `claude`（暖陶）；主题默认跟随系统。标题字体 Outfit 随构建打包，不访问外部字体 CDN。
- 外壳是顶部导航，不使用侧栏和页脚。`Ctrl+J`（macOS 上是 `⌘J`）打开或关闭 AI 面板，头像菜单提供主题切换。
- 删除确认使用共享 ConfirmDialog 与 useConfirmDelete，支持键盘操作，并按操作结果恢复焦点。
- 设置是单页：左侧吸顶分类导航，右侧依次是 外观、业务模块、AI、通知推送、账户安全、数据与运行 分区卡片，行内左侧名称、右侧控件。`/settings?tab=modules|ai|notifications|security|appearance|data` 滚动到对应分区，`?module=<id>` 打开业务设置弹窗。
- 业务设置弹窗最大宽度 480px，支持 Escape、遮罩及关闭按钮，接入 `useReportDirty` 拦截未保存草稿关闭，并将焦点还给触发按钮。
- 通知的查看、已读、归档使用共享 Button/ButtonLink 的 ghost、sm 规格；业务只提供 actionRoute 和 actionLabel。
- 宽度不超过 900px 时设置分类变为横向滚动胶囊，避免纵向溢出；新增页面检查桌面和窄屏宽度。
- `/investment` 只显示图表（铺满导航下方区域，角落图标按钮切换铺满/全屏/新窗口），价格监控在 `/investment/monitor`。

## 开发与构建

推荐在仓库根目录执行 `./scripts/dev.sh`，统一启动后端和 Vite。脚本透传后端参数，自动匹配代理地址与允许的 Host；支持 `--web-port`、`--web-host` 和 `--dev-url`。React/CSS 使用热更新，投资终端的磁盘资源修改后自动刷新。Go 不监听文件，重新执行脚本才编译。启动与退出说明见 [scripts/README](../scripts/README.md#开发模式)。

在本目录执行 `npm ci`，使用 `npm run dev` 开发、`npm run lint` 检查、`npm run test:contracts` 验证真实后端响应与前端 schema、`npm run build` 执行类型检查和生产构建。产物直接写入 `internal/webui/dist/` 并检入，随后可嵌入 Go 程序。

Vite 代理和 Host 配置见[配置与运行](../doc/configuration.md)，完整检查及浏览器回归见 [scripts/README](../scripts/README.md)。
