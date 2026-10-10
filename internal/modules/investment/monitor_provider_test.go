package investment

import (
	"context"
	"errors"
	"testing"
	"time"
)

type monitorProviderFixture struct {
	batch   monitorQuoteBatch
	current bool
}

func (p monitorProviderFixture) Fetch(_ context.Context, symbols map[string]struct{}, _ time.Time) (monitorQuoteBatch, bool, error) {
	if _, ok := symbols["AAPL"]; !ok {
		return monitorQuoteBatch{}, false, errors.New("missing requested symbol")
	}
	return p.batch, true, nil
}

func (p monitorProviderFixture) WithCurrent(_ context.Context, version string, commit func() error) error {
	if !p.current || version != p.batch.authVersion {
		return errors.New("provider authorization changed")
	}
	return commit()
}

func TestMonitorUsesProviderAndRejectsInvalidatedBatch(t *testing.T) {
	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "current", false: "invalidated"}[current], func(t *testing.T) {
			m := openInvestmentModule(t)
			now := time.Now().UTC()
			m.now = func() time.Time { return now }
			addPriceRule(t, m, "provider-rule", "AAPL", "up", 300)
			m.monitorQuotes = monitorProviderFixture{
				current: current,
				batch: monitorQuoteBatch{
					authVersion: "provider:g3",
					session:     marketSession{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
					quotes:      map[string]monitoredQuote{"AAPL": priceFixture("AAPL", 104, now)},
				},
			}
			// No Schwab credentials are configured; only the injected provider is used.
			err := m.scanPrices(context.Background())
			if current {
				if err != nil {
					t.Fatal(err)
				}
				countTriggers(t, m, 1)
			} else {
				if err == nil {
					t.Fatal("accepted an invalidated batch")
				}
				countTriggers(t, m, 0)
			}
		})
	}
}

func TestNewRequiresMonitorNotifications(t *testing.T) {
	m := openInvestmentModule(t)
	if _, err := New(Dependencies{DB: m.deps.DB}); err == nil {
		t.Fatal("registered monitoring without notifications")
	}
}
