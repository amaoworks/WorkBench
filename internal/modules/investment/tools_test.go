package investment

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"workbench/internal/contracts"
)

type investmentToolRegistrar struct {
	tools []contracts.AITool
}

func (*investmentToolRegistrar) Handle(string, string, http.Handler) error { return nil }
func (*investmentToolRegistrar) Consume(contracts.EventConsumer) error     { return nil }
func (r *investmentToolRegistrar) Tool(tool contracts.AITool) error {
	r.tools = append(r.tools, tool)
	return nil
}
func (*investmentToolRegistrar) Widget(contracts.WidgetDefinition) error { return nil }
func (*investmentToolRegistrar) Job(contracts.JobDefinition) error       { return nil }

func TestInvestmentAIToolsRegisterWithExpectedRisk(t *testing.T) {
	m := openInvestmentModule(t)
	r := &investmentToolRegistrar{}
	if err := m.Register(r); err != nil {
		t.Fatal(err)
	}
	want := map[string]contracts.ToolRisk{
		"investment.positions":      contracts.ToolRiskReadOnly,
		"investment.orders":         contracts.ToolRiskReadOnly,
		"investment.list_monitors":  contracts.ToolRiskReadOnly,
		"investment.create_monitor": contracts.ToolRiskLowWrite,
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

func TestInvestmentAIToolsReusePortfolioAndMonitorOperations(t *testing.T) {
	m := openInvestmentModule(t)
	ctx := testContext(context.Background())

	positions, err := m.positionsTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"accountHash":null}`)})
	if err != nil {
		t.Fatal(err)
	}
	var portfolio portfolioResponse
	if err := json.Unmarshal(positions.Content, &portfolio); err != nil || portfolio.Status != "unconfigured" || portfolio.Items == nil {
		t.Fatalf("portfolio=%s err=%v", positions.Content, err)
	}
	if _, err := m.ordersTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"days":2,"status":"all","timeZone":"UTC","accountHash":null}`)}); err == nil {
		t.Fatal("orders accepted an unsupported date range")
	}
	if _, err := m.createMonitorTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"symbol":"AAPL","direction":"down","thresholdPercent":101,"enabled":true}`)}); err == nil {
		t.Fatal("monitor accepted a decline over 100 percent")
	}
	created, err := m.createMonitorTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"symbol":" aapl ","direction":"up","thresholdPercent":2.5,"enabled":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	var rule priceRuleView
	if err := json.Unmarshal(created.Content, &rule); err != nil || rule.Symbol != "AAPL" || rule.ThresholdPercent != 2.5 {
		t.Fatalf("rule=%s err=%v", created.Content, err)
	}
	listed, err := m.listMonitorsTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var monitor struct {
		Items []priceRuleView `json:"items"`
	}
	if err := json.Unmarshal(listed.Content, &monitor); err != nil || len(monitor.Items) != 1 || monitor.Items[0].ID != rule.ID {
		t.Fatalf("monitor=%s err=%v", listed.Content, err)
	}
	if _, err := m.listMonitorsTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"extra":true}`)}); err == nil {
		t.Fatal("tool accepted an unknown argument")
	}
}
