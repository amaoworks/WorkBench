package news

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"workbench/internal/contracts"
)

func (m *Module) aiTools() []contracts.AITool {
	return []contracts.AITool{
		{Name: "news.list_articles", Module: "news", SchemaVersion: 1, Description: "按关键词、来源、主题和阅读或规则状态分页查询已存资讯",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":500},"sourceId":{"type":"string","maxLength":64},"topic":{"type":"string","maxLength":120},"unread":{"type":"boolean"},"starred":{"type":"boolean"},"matched":{"type":"boolean"},"cursor":{"type":["string","null"],"maxLength":1000},"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["query","sourceId","topic","unread","starred","matched","cursor","limit"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly, Handler: m.listArticlesTool},
		{Name: "news.get_article", Module: "news", SchemaVersion: 1, Description: "读取一篇已存资讯的原文、摘要、来源链接和时间",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"articleId":{"type":"string","minLength":1,"maxLength":64}},"required":["articleId"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly, Handler: m.getArticleTool},
		{Name: "news.create_digest", Module: "news", SchemaVersion: 1, Description: "用已存资讯生成并保存简报，不提交外部推送",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"topic":{"type":"string","maxLength":120},"periodHours":{"type":"integer","minimum":1,"maximum":168},"maxItems":{"type":"integer","minimum":1,"maximum":30}},"required":["topic","periodHours","maxItems"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskLowWrite, Handler: m.createDigestTool},
	}
}

func decodeNewsTool(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("工具参数无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("工具参数无效")
	}
	return nil
}

func (m *Module) listArticlesTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return contracts.ToolResult{}, err
	}

	var args struct {
		Query    string  `json:"query"`
		SourceID string  `json:"sourceId"`
		Topic    string  `json:"topic"`
		Unread   bool    `json:"unread"`
		Starred  bool    `json:"starred"`
		Matched  bool    `json:"matched"`
		Cursor   *string `json:"cursor"`
		Limit    int     `json:"limit"`
	}
	if err := decodeNewsTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	opCtx, cancel := m.operation(ctx)
	defer cancel()
	cursor := ""
	if args.Cursor != nil {
		cursor = *args.Cursor
	}
	page, err := m.articles(opCtx, articleListParams{
		Search: args.Query, SourceID: args.SourceID, Topic: args.Topic,
		Unread: args.Unread, Starred: args.Starred, Matched: args.Matched, Cursor: cursor, Limit: args.Limit,
	})
	if err != nil {
		return contracts.ToolResult{}, err
	}
	return marshalNewsTool(page)
}

func (m *Module) getArticleTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return contracts.ToolResult{}, err
	}

	var args struct {
		ArticleID string `json:"articleId"`
	}
	if err := decodeNewsTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	opCtx, cancel := m.operation(ctx)
	defer cancel()
	article, err := m.Article(opCtx, args.ArticleID)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	return marshalNewsTool(article)
}

func (m *Module) createDigestTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return contracts.ToolResult{}, err
	}

	var args struct {
		Topic       string `json:"topic"`
		PeriodHours int64  `json:"periodHours"`
		MaxItems    int64  `json:"maxItems"`
	}
	if err := decodeNewsTool(call.Arguments, &args); err != nil {
		return contracts.ToolResult{}, err
	}
	slot := ""
	if call.IdempotencyKey != "" {
		slot = "tool:" + hash(call.IdempotencyKey)
	}
	digest, err := m.CreateDigest(ctx, DigestInput{Topic: args.Topic, PeriodHours: args.PeriodHours, MaxItems: args.MaxItems, Push: false}, slot)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	return marshalNewsTool(digest)
}

func marshalNewsTool(value any) (contracts.ToolResult, error) {
	body, err := json.Marshal(value)
	return contracts.ToolResult{Content: body}, err
}
