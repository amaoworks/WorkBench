package investment

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOvernightQuotePreservesZeroAndMissingValues(t *testing.T) {
	row := map[string]any{
		"security":  map[string]any{"code": "AAPL"},
		"overnight": map[string]any{"price": 101, "changeVal": 0, "changeRate": 0, "volume": "0"},
	}
	quote, ok := parseOvernightQuote(row)
	if !ok {
		t.Fatal("valid overnight quote was rejected")
	}
	encoded, err := json.Marshal(quote)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ch", "chp", "volume"} {
		if value, exists := values[field]; !exists || value != float64(0) {
			t.Fatalf("zero %s must be retained: %s", field, encoded)
		}
	}
	for _, field := range []string{"high", "low"} {
		if _, exists := values[field]; exists {
			t.Fatalf("missing %s must not become zero: %s", field, encoded)
		}
	}
	for _, price := range []any{nil, 0, -1, "NaN", math.Inf(1)} {
		row["overnight"] = map[string]any{"price": price}
		if _, ok := parseOvernightQuote(row); ok {
			t.Fatalf("invalid price %v was accepted", price)
		}
	}
}

func TestQuoteOnlyFutuLeaseSharesBasicWithoutAffectingKlines(t *testing.T) {
	fake := startFakeOpenD(t)
	fake.mu.Lock()
	fake.basicQotList = []map[string]any{{"security": map[string]any{"code": "AAPL"}, "overnight": map[string]any{"price": 101, "changeVal": 0}}}
	fake.mu.Unlock()
	module := openInvestmentModule(t)
	module.deps.FutuOpenDAddress = fake.addr()
	for _, config := range []struct {
		path string
		save func(http.ResponseWriter, *http.Request)
	}{
		{"/api/modules/investment/futu", module.saveFutu},
		{"/api/modules/investment/overnight", module.saveOvernight},
	} {
		response := httptest.NewRecorder()
		config.save(response, httptest.NewRequest(http.MethodPut, config.path, strings.NewReader(`{"enabled":true}`)))
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gateway := module.futu
	generation, applied, err := gateway.addSub(ctx, "AAPL", "", "")
	if err != nil || !applied {
		t.Fatalf("quote-only ADD: applied=%v err=%v", applied, err)
	}
	assertCounts := func(basic, kline int) {
		t.Helper()
		gateway.mu.Lock()
		defer gateway.mu.Unlock()
		for sub, count := range map[int]int{subTypeBasic: basic, subTypeKL5Min: kline} {
			lease := gateway.refs[subKey{symbol: "AAPL", sub: sub}]
			if (lease == nil && count != 0) || (lease != nil && lease.count != count) {
				t.Fatalf("subscription %d: count=%d lease=%+v", sub, count, lease)
			}
		}
	}
	assertCounts(1, 0)
	for _, resolution := range []string{"5", ""} {
		if _, applied, err := gateway.addSub(ctx, "AAPL", resolution, generation); err != nil || !applied {
			t.Fatalf("ADD %q: applied=%v err=%v", resolution, applied, err)
		}
	}
	assertCounts(3, 1)
	fake.mu.Lock()
	reads := fake.quoteN
	fake.mu.Unlock()
	if reads != 2 {
		t.Fatalf("each quote listener must read an initial snapshot, including a shared BASIC lease: reads=%d", reads)
	}
	for index, resolution := range []string{"", "5", ""} {
		if _, applied, err := gateway.releaseSub(ctx, "AAPL", resolution, generation); err != nil || !applied {
			t.Fatalf("UNSUB %q: applied=%v err=%v", resolution, applied, err)
		}
		kline := 0
		if index == 0 {
			kline = 1
		}
		assertCounts(2-index, kline)
	}
}
