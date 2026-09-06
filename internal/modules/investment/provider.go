package investment

import (
	"context"
	"time"
)

type Quote struct {
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`
	PriceCents int64     `json:"priceCents"`
	ChangeBPS  int64     `json:"changeBps"`
	AsOf       time.Time `json:"asOf"`
}

type QuoteProvider interface {
	Snapshot(context.Context) ([]Quote, error)
}

// MockProvider returns a bounded set of fictional instruments, stable within each five-minute slot.
type MockProvider struct{}

func (MockProvider) Snapshot(ctx context.Context) ([]Quote, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	at := time.Now().UTC().Truncate(5 * time.Minute)
	return []Quote{
		{Symbol: "DEMO-A", Name: "模拟成长", PriceCents: 10250, ChangeBPS: 250, AsOf: at},
		{Symbol: "DEMO-B", Name: "模拟稳健", PriceCents: 9980, ChangeBPS: -20, AsOf: at},
		{Symbol: "DEMO-C", Name: "模拟均衡", PriceCents: 10080, ChangeBPS: 80, AsOf: at},
	}, nil
}
