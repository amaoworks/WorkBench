# Workbench Web

技术栈：React、TypeScript、Vite、Tailwind CSS、Radix UI、Lucide、React Router、TanStack Query 与 Zod。

```text
src/app/                    应用装配、Provider 和主题
src/components/ui/          通用 UI 基础组件
src/features/dashboard/     Dashboard 通用能力
src/features/notifications/ 通知中心与 SSE
src/features/settings/      AI、安全、外观、数据与运行设置
src/modules/                编译期业务模块页面/Widget
src/shared/                 API client、schema、工具与共享类型
```

后端返回的 `pageKey/widgetKind` 只能映射到本地编译期注册项。

## 界面约定

- 图标统一使用 Lucide，不使用表情符号。
- 页面标题使用 `PageHeader`，卡片和按钮使用通用 `Card`、`Button`，表单使用共享输入框样式。
- 配色从 `styles.css` 的语义变量读取，统一明暗主题、状态色、边框和圆角；新页面不要另起一套固定色板。
- 设置页通过五个标签切换，只显示当前面板，其他面板保持挂载以保留草稿。支持方向键、Home/End 选择标签。
- `/settings?tab=modules|ai|security|appearance|data` 可直接定位分类，刷新保留分类；未保存的输入仅在本次页面停留期间保留。
- 使用简洁的状态过渡，尊重系统及工作空间的减少动态效果设置。

## 业务注册与总览

业务通过 `src/modules/<id>/<id>.module.ts` 声明 `pages`、`widgets` 和可选 `icons`。`modules/registry.ts` 编译时收集描述，组件通过 React.lazy 加载。Shell/总览不包含业务组件实现。

总览配置支持显隐、上下移和尺寸；模块管理位于设置的业务模块标签。queryKey、接口调用、Zod 校验与接入流程见 [业务开发指南](../doc/business-development.md)。

## 通知操作与导航按钮

通知的查看、标为已读、归档统一使用共享 `Button` / `ButtonLink` 的 `ghost`、`sm` 规格，保持相同字体、行高、内边距和控件高度。`ButtonLink` 保留站内链接语义，与 `Button` 共用样式定义；新业务需要按钮形式的跳转时复用它。

业务通知只提供 `actionRoute` 和 `actionLabel`，由通知中心统一渲染，不自行定义通知操作的样式。设置标签栏只允许必要的横向滚动，禁用纵向溢出；选中指示线绘制在标签内部，避免产生额外滚动条。
