# ADR-002：chi 与受控模块注册

- 状态：Accepted
- 日期：2026-09-04

## 决策

HTTP 路由使用 chi，但模块接口仅暴露 `net/http` 类型。Module 收敛为 `Manifest()`、`Migrations()` 和 `Register(ModuleRegistrar)`；Registrar 暂存所有资源，校验成功后原子提交。

## 理由

避免业务模块依赖具体路由框架，并防止初始化中途失败留下部分 Route、Tool、Widget 或 Job。

## 后果

主程序负责显式收集模块和启动顺序。资源 ID、路由冲突和模块归属必须在对外服务前完成校验。

