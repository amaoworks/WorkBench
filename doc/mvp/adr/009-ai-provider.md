# ADR-009：AI Provider 与 OpenAI MVP

- 状态：Accepted
- 日期：2026-09-04

## 决策

AI 层使用供应商无关 Provider 接口，MVP 只实现 OpenAI 官方 Go SDK 与 Responses API，调用默认显式设置 `store:false`。基础接口为 `Generate/Stream`，并提供模型 profile、严格 Tool Schema、风险分级、用户确认、幂等与审计。

## 理由

Provider 边界避免业务模块直接依赖供应商 SDK；Responses API 支持所需的生成和工具调用能力。默认关闭远端存储符合本地优先预期。

## 后果

业务模块只能注册 AITool，不直接调用供应商 API。站内保存对话时必须区分本地历史与供应商状态；流式请求需要支持取消、超时和半途失败。

参考：OpenAI 官方文档，[Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)。
