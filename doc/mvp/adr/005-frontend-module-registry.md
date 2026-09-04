# ADR-005：前端编译期模块注册表

- 状态：Accepted
- 日期：2026-09-04

## 决策

页面和 Widget 在前端编译期注册。后端动态返回 enabled、导航、`pageKey`、`widgetKind` 和数据端点，前端将这些键映射到已打包组件。使用 React Router、TanStack Query、Zod、Vite glob 和 `React.lazy`。

## 理由

浏览器无法凭后端字符串安全地获得未打包 React 组件。编译期注册既保留动态导航，又使 bundle、类型和权限边界可控。

## 后果

新增 UI 模块需要重新构建前端。未知 key 必须产生可诊断的兼容性错误，不能动态执行服务端提供的代码。

