package investment

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOverviewFutuUnknownChangeAndTimeRemainUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, field, session string
		hour                 int
	}{
		{"regular", "", "regular", 15},
		{"premarket", "preMarket", "pre", 12},
		{"postmarket", "afterMarket", "post", 21},
		{"overnight", "overnight", "overnight", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := openInvestmentModule(t)
			now := time.Date(2026, 10, 27, tc.hour, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			fake := overviewFutuFixture(t, m, now)
			overviewWatchlist(t, m, testContext(context.Background()), "AAPL")
			if tc.field == "overnight" {
				if err := m.storeFutu(testContext(context.Background()), futuRecord{Enabled: true, OvernightEnabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			fake.mu.Lock()
			if tc.field == "" {
				delete(fake.basicQotList[0], "lastClosePrice")
				delete(fake.basicQotList[0], "updateTimestamp")
			} else {
				// Extended prices have no timestamp: the regular quote timestamp
				// must not be reused for them.
				fake.basicQotList[0][tc.field] = map[string]any{"price": 105.0}
			}
			fake.mu.Unlock()
			got := m.overviewQuotes(testContext(context.Background()))
			if got.Status != "ok" || got.Provider != "futu" || len(got.Items) != 1 || got.Items[0].Price != 105 || got.Items[0].Session != tc.session || got.Items[0].ChangePct != nil || got.UpdatedAt != nil || len(got.Items[0].Spark) != 0 {
				t.Fatalf("unknown data must not become zero or current time: %+v", got)
			}
			response := httptest.NewRecorder()
			m.getOverviewQuotes(response, testRequest("GET", "/", nil))
			for _, invented := range []string{`"changePct"`, `"updatedAt"`, `"spark"`} {
				if strings.Contains(response.Body.String(), invented) {
					t.Fatalf("invented %s: %s", invented, response.Body.String())
				}
			}
		})
	}
}
