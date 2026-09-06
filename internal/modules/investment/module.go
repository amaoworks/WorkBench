package investment

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Dependencies struct {
	DB            *sql.DB
	Events        contracts.EventPublisher
	Notifications contracts.NotificationService
	AI            contracts.TextGenerator
	Quotes        QuoteProvider
}

type Module struct {
	deps    Dependencies
	queries *investmentsqlc.Queries
}

func New(deps Dependencies) (*Module, error) {
	if deps.DB == nil || deps.Events == nil || deps.Notifications == nil || deps.AI == nil || deps.Quotes == nil {
		return nil, errors.New("investment dependencies are required")
	}
	return &Module{deps: deps, queries: investmentsqlc.New(deps.DB)}, nil
}
func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "investment", Name: "模拟行情", Version: "0.1.0", ContractVersion: 1, Icon: "investment.chart",
		Navigation: []contracts.NavigationItem{{Label: "模拟行情", Route: "/investment", PageKey: "investment.overview", Order: 20}}}
}
func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}
func (m *Module) Register(r contracts.ModuleRegistrar) error {
	return errors.Join(
		r.Handle("GET", "/api/modules/investment/overview", http.HandlerFunc(m.overview)),
		r.Handle("POST", "/api/modules/investment/sync", http.HandlerFunc(m.syncHTTP)),
		r.Handle("POST", "/api/modules/investment/summary", http.HandlerFunc(m.summarize)),
		r.Widget(contracts.WidgetDefinition{ID: "investment.overview", Module: "investment", SchemaVersion: 1,
			Title: "模拟行情", WidgetKind: "investment.overview", DataRoute: "/api/modules/investment/overview", Size: contracts.WidgetMedium, Order: 20}),
		r.Job(contracts.JobDefinition{ID: "investment.sync", Module: "investment", Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: 5 * time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry: contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: time.Minute}, Handler: func(ctx context.Context, _ contracts.JobRun) error { return m.Sync(ctx) }}),
		r.Consume(contracts.EventConsumer{ID: "investment.price_alert", Module: "investment", Topics: []string{"investment.price.updated"},
			Timeout: 10 * time.Second, MaxAttempts: 3, Handler: m.priceAlert}),
	)
}

func (m *Module) Sync(ctx context.Context) error {
	quotes, err := m.deps.Quotes.Snapshot(ctx)
	if err != nil {
		return err
	}
	if len(quotes) > 100 {
		return errors.New("quote snapshot exceeds 100 instruments")
	}
	tx, err := m.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := m.queries.WithTx(tx)
	for _, quote := range quotes {
		if quote.Symbol == "" || quote.Name == "" || quote.PriceCents <= 0 || quote.AsOf.IsZero() {
			return errors.New("invalid quote")
		}
		changed, err := queries.SaveQuote(ctx, investmentsqlc.SaveQuoteParams{Symbol: quote.Symbol, Name: quote.Name, PriceCents: quote.PriceCents, ChangeBps: quote.ChangeBPS, AsOf: quote.AsOf.UnixMilli()})
		if err != nil {
			return err
		}
		if changed == 0 {
			continue
		}
		payload, _ := json.Marshal(quote)
		if _, err := m.deps.Events.PublishTx(ctx, tx, contracts.NewEvent{Topic: "investment.price.updated", SchemaVersion: 1,
			SourceModule: "investment", AggregateID: quote.Symbol, Payload: payload}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Module) priceAlert(ctx context.Context, event contracts.Event) error {
	if event.SchemaVersion != 1 {
		return errors.New("unsupported price event version")
	}
	var quote Quote
	if err := json.Unmarshal(event.Payload, &quote); err != nil {
		return err
	}
	if quote.ChangeBPS < 200 && quote.ChangeBPS > -200 {
		return nil
	}
	_, err := m.deps.Notifications.Create(ctx, contracts.NewNotification{SourceModule: "investment", Severity: contracts.NotificationInfo,
		Title: "模拟行情波动提醒", Content: fmt.Sprintf("%s（模拟数据）变动 %.2f%%", quote.Name, float64(quote.ChangeBPS)/100),
		ActionLabel: "查看模拟行情", ActionRoute: "/investment", SourceEventID: event.ID,
		IdempotencyKey: "investment:price:" + quote.Symbol + ":" + fmt.Sprint(quote.AsOf.UnixMilli())})
	return err
}

type Summary struct {
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}
type Overview struct {
	Source  string   `json:"source"`
	Items   []Quote  `json:"items"`
	Summary *Summary `json:"summary"`
}

func (m *Module) readOverview(ctx context.Context) (Overview, error) {
	value := Overview{Source: "mock", Items: []Quote{}}
	rows, err := m.queries.ListQuotes(ctx)
	if err != nil {
		return value, err
	}
	for _, row := range rows {
		value.Items = append(value.Items, Quote{Symbol: row.Symbol, Name: row.Name, PriceCents: row.PriceCents, ChangeBPS: row.ChangeBps, AsOf: time.UnixMilli(row.AsOf).UTC()})
	}
	summary, err := m.queries.GetSummary(ctx)
	if err == nil {
		value.Summary = &Summary{Content: summary.Content, CreatedAt: time.UnixMilli(summary.CreatedAt).UTC()}
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return value, err
}
func (m *Module) overview(w http.ResponseWriter, r *http.Request) {
	value, err := m.readOverview(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "investment_failed", "无法读取模拟行情")
		return
	}
	httpapi.Write(w, 200, value)
}
func (m *Module) syncHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_request", "请提交空 JSON 对象")
		return
	}
	if err := m.Sync(r.Context()); err != nil {
		httpapi.Error(w, 502, "sync_failed", "模拟行情同步失败")
		return
	}
	m.overview(w, r)
}
func (m *Module) summarize(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_request", "请提交空 JSON 对象")
		return
	}
	overview, err := m.readOverview(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "investment_failed", "无法读取模拟行情")
		return
	}
	if len(overview.Items) == 0 {
		httpapi.Error(w, 409, "quotes_required", "请先同步模拟行情")
		return
	}
	raw, _ := json.Marshal(overview.Items)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	text, err := m.deps.AI.GenerateText(ctx, contracts.TextRequest{Profile: "default", Instruction: "用中文简要描述以下虚构模拟行情的数值变化，明确说明数据为模拟。仅描述给定数据，不给投资建议，不执行任何操作。", Input: string(raw)})
	if errors.Is(err, contracts.ErrAIUnavailable) {
		httpapi.Error(w, 503, "ai_unavailable", "AI 未启用，请在设置中配置；行情浏览与同步仍可使用")
		return
	}
	if err != nil {
		httpapi.Error(w, 502, "summary_failed", "AI 摘要生成失败，请稍后重试")
		return
	}
	summary := Summary{Content: text, CreatedAt: time.Now().UTC()}
	if err := m.queries.SaveSummary(r.Context(), investmentsqlc.SaveSummaryParams{Content: text, CreatedAt: summary.CreatedAt.UnixMilli()}); err != nil {
		httpapi.Error(w, 500, "summary_failed", "无法保存摘要")
		return
	}
	httpapi.Write(w, 200, summary)
}
