# ADR-007：显式鉴权模式

- 状态：Accepted
- 日期：2026-09-04

## 决策

提供显式 `local` 和 `password` 模式。`local` 只监听 loopback；`password` 使用 Argon2id、服务端 Session、SCS 语义和自定义 modernc SQLite Store。启用 Host、Origin 和 CSRF 防护，非 loopback 默认要求 HTTPS。

## 理由

监听地址自动触发鉴权容易出现配置歧义。JWT 不利于个人场景中的即时撤销，服务端 Session 更简单且可控。

## 后果

不安全的监听/鉴权组合必须启动失败。密码重设、Session 清理、Cookie 策略和反向代理可信边界都需要测试。

