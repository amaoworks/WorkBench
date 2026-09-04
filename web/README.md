# Workbench Web

计划技术栈：React、TypeScript、Vite、Tailwind CSS、shadcn/ui、React Router、TanStack Query 与 Zod。

```text
src/app/                    应用装配、Provider 和主题
src/components/ui/          通用 UI 基础组件
src/features/dashboard/     Dashboard 通用能力
src/features/notifications/ 通知中心与 SSE
src/modules/                编译期业务模块页面/Widget
src/routes/                 路由与 pageKey 注册
src/shared/                 API client、schema、工具与共享类型
```

后端返回的 `pageKey/widgetKind` 只能映射到本地编译期注册项。

