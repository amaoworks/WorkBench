# Workbench 前端

React + TypeScript 前端，使用 Vite、Tailwind CSS、Radix UI、Lucide、React Router、TanStack Query 和 Zod。安装版本以 `package-lock.json` 为准。

## 目录与归属

```text
src/app/                 登录、应用装配、导航外壳、主题、命令面板
src/features/ai/         平台 AI 对话面板
src/features/dashboard/ 总览及布局配置
src/features/notifications/ 通知列表与 SSE
src/features/settings/  模块管理、AI、安全、外观、数据与运行设置
src/modules/registry.ts 编译期业务注册目录
src/modules/<id>/       业务页面、Widget、模块设置、queries.ts、schema.ts
src/components/ui/      通用界面组件
src/shared/             HTTP 客户端、平台共享 schema、工具
src/styles.css          主题变量和共享样式
```

业务前端与 `internal/modules/<id>/` 一一对应。业务接口函数、查询 hooks 和缓存刷新放模块内 `queries.ts`，数据校验和类型放 `schema.ts`。页面保留表单与交互状态；平台 shared 不存放 Task 等业务专属模型。业务之间不直接导入彼此实现。

## 模块注册和缓存

`src/modules/<id>/<id>.module.ts` 声明 pages、widgets、可选 icons 和 settings。Registry 自动收集声明，组件按需加载。后端 `pageKey/widgetKind` 只映射到已编译的本地注册项；Shell 和总览不包含业务组件实现。

模块设置接收 `{ enabled: boolean }`，停用时不发起业务查询。设置弹窗由平台管理，没有设置组件的模块展示暂无配置内容。

业务 queryKey 以模块 ID 开头，Widget 使用 `["widget", widget.id]`。启停模块后平台取消旧查询、清除对应业务和 Widget 缓存，再刷新模块、总览与 AI 状态；业务写入后刷新实际受影响的查询。分页使用后端 nextCursor。接入示例见[业务开发指南](../doc/business-development.md)。

## 界面约定

- 图标使用 Lucide。标题使用 PageHeader，卡片和按钮使用共享 Card、Button；按钮形式的站内跳转使用 ButtonLink。
- 配色读取 `styles.css` 的语义变量，兼容明暗主题，尊重系统及工作空间的减少动态效果设置。
- 删除确认使用共享 ConfirmDialog，支持键盘操作与关闭后的焦点恢复。
- 设置页面有五个标签，切换时保留当前页面内的表单草稿；支持方向键、Home/End。`/settings?tab=modules|ai|security|appearance|data` 可定位标签。
- 业务设置弹窗最大宽度 480px，支持 Escape、遮罩及关闭按钮，并将焦点还给触发按钮。
- 通知的查看、已读、归档使用共享 Button/ButtonLink 的 ghost、sm 规格；业务只提供 actionRoute 和 actionLabel。
- 设置标签栏按需横向滚动，避免纵向溢出；新增页面检查桌面和窄屏宽度。

## 开发与构建

在本目录执行 `npm ci`，使用 `npm run dev` 开发、`npm run lint` 检查、`npm run build` 执行类型检查和生产构建。产物直接写入 `internal/webui/dist/` 并检入，随后可嵌入 Go 程序。

Vite 代理和 Host 配置见[配置与运行](../doc/configuration.md)，完整检查及浏览器回归见 [scripts/README](../scripts/README.md)。
