package investment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"workbench/internal/contracts"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

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
