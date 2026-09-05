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
- 设置页通过四个标签切换，只显示当前面板，其他面板保持挂载以保留草稿。支持方向键、Home/End 选择标签。
- `/settings?tab=ai|security|appearance|data` 可直接定位分类，刷新保留分类；未保存的输入仅在本次页面停留期间保留。
- 使用简洁的状态过渡，尊重系统及工作空间的减少动态效果设置。
