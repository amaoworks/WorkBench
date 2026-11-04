package news

import (
	"encoding/json"
	"net/http"
	"testing"

	"workbench/internal/contracts"
	"workbench/internal/modules/news/sqlc"
)

type newsToolRegistrar struct {
	tools []contracts.AITool
}

func (*newsToolRegistrar) Handle(string, string, http.Handler) error { return nil }
func (*newsToolRegistrar) Consume(contracts.EventConsumer) error     { return nil }
func (r *newsToolRegistrar) Tool(tool contracts.AITool) error {
	r.tools = append(r.tools, tool)
	return nil
}
func (*newsToolRegistrar) Widget(contracts.WidgetDefinition) error { return nil }
func (*newsToolRegistrar) Job(contracts.JobDefinition) error       { return nil }

func TestNewsAIToolsRegisterWithExpectedRisk(t *testing.T) {
	m, _, _ := openModule(t)
	r := &newsToolRegistrar{}
	if err := m.Register(r); err != nil {
		t.Fatal(err)
	}
	want := map[string]contracts.ToolRisk{
		"news.list_articles": contracts.ToolRiskReadOnly,
		"news.get_article":   contracts.ToolRiskReadOnly,
		"news.create_digest": contracts.ToolRiskLowWrite,
	}
	if len(r.tools) != len(want) {
		t.Fatalf("tools=%d want=%d", len(r.tools), len(want))
	}
	for _, tool := range r.tools {
		if tool.Risk != want[tool.Name] || tool.Handler == nil || !json.Valid(tool.ParametersSchema) {
			t.Fatalf("invalid tool registration: %+v", tool)
		}
	}
}

func TestNewsAIToolsReuseArticleFiltersAndDigestCreation(t *testing.T) {
	m, _, _ := openModule(t)
	ingest(t, m)
	article := firstArticle(t, m)
	ctx := testContext()

	listed, err := m.listArticlesTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"query":"earnings","sourceId":"","topic":"","unread":true,"starred":false,"matched":false,"cursor":null,"limit":10}`)})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []Article `json:"items"`
	}
	if err := json.Unmarshal(listed.Content, &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != article.ID {
		t.Fatalf("page=%s err=%v", listed.Content, err)
	}
	got, err := m.getArticleTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"articleId":"` + article.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var full Article
	if err := json.Unmarshal(got.Content, &full); err != nil || full.Content == "" || full.URL == "" || len(full.Origins) != 1 {
		t.Fatalf("article=%s err=%v", got.Content, err)
	}
	digestResult, err := m.createDigestTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"topic":"","periodHours":24,"maxItems":10}`)})
	if err != nil {
		t.Fatal(err)
	}
	var digest Digest
	if err := json.Unmarshal(digestResult.Content, &digest); err != nil || len(digest.Items) != 1 || digest.NotificationID != "" {
		t.Fatalf("digest=%s err=%v", digestResult.Content, err)
	}
	stored, err := m.q.GetDigest(ctx, newssqlc.GetDigestParams{UserID: contracts.UserID(ctx), ID: digest.ID})
	if err != nil || stored.NotificationID != "" {
		t.Fatalf("stored digest=%+v err=%v", stored, err)
	}
	if _, err := m.createDigestTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"topic":"","periodHours":0,"maxItems":10}`)}); err == nil {
		t.Fatal("digest accepted an invalid period")
	}
}

func TestNewsReadToolHonorsModuleLifecycle(t *testing.T) {
	m, _, _ := openModule(t)
	if err := m.OnEnabledChanged(testContext(), false); err != nil {
		t.Fatal(err)
	}
	_, err := m.listArticlesTool(testContext(), contracts.ToolCall{Arguments: json.RawMessage(`{"query":"","sourceId":"","topic":"","unread":false,"starred":false,"matched":false,"cursor":null,"limit":10}`)})
	if err == nil {
		t.Fatal("disabled module accepted a read tool operation")
	}
}
