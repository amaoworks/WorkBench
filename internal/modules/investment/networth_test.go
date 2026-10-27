package investment

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func number(value float64) *float64 { return &value }

func snapshotAccount(hash string, netWorth, cash, invested, dayPnl *float64) portfolioAccount {
	return portfolioAccount{Hash: hash, NetWorth: netWorth, Cash: cash, Invested: invested, DayPnl: dayPnl}
}

func TestStoreNetWorthSnapshotUpsertsAndCommitsCompleteAccounts(t *testing.T) {
	m := openInvestmentModule(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 8, 16, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	auth := now.Add(-time.Hour).UnixMilli()
	portfolio := portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("hash-a", number(100), number(20), number(80), number(3)),
		snapshotAccount("hash-b", number(50), nil, number(40), number(1)),
	}}
	err := m.storeNetWorthSnapshot(ctx, auth, portfolio)
	if err == nil || !strings.Contains(err.Error(), "hash-b") {
		t.Fatalf("partial error = %v", err)
	}
	var snapshots, scope int
	if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_networth_snapshots").Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_networth_account_scopes").Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || scope != 2 {
		t.Fatalf("snapshots=%d scope=%d", snapshots, scope)
	}
	now = now.Add(time.Hour)
	if err := m.storeNetWorthSnapshot(ctx, auth, portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("hash-a", number(110), number(25), number(85), number(4)),
	}}); err != nil {
		t.Fatal(err)
	}
	var netWorth float64
	var capturedAt int64
	if err := m.deps.DB.QueryRow("SELECT net_worth, captured_at FROM investment_networth_snapshots WHERE account_hash='hash-a'").Scan(&netWorth, &capturedAt); err != nil {
		t.Fatal(err)
	}
	if netWorth != 110 || capturedAt != now.UnixMilli() {
		t.Fatalf("netWorth=%v capturedAt=%d", netWorth, capturedAt)
	}
}

func TestNetWorthHistoryAggregatesWithoutInventingMissingAccounts(t *testing.T) {
	m := openInvestmentModule(t)
	ctx := context.Background()
	loc := m.marketLocation
	authorizedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	seedSchwab(t, m, schwabRecord{
		AppKey: "key", AppSecret: "secret", AccessToken: "access", RefreshToken: "refresh",
		TokenExpiresAt: authorizedAt.Add(24 * time.Hour), AuthorizedAt: authorizedAt,
	})
	m.now = func() time.Time { return time.Date(2026, 3, 6, 16, 15, 0, 0, loc) }
	if err := m.storeNetWorthSnapshot(ctx, authorizedAt.UnixMilli(), portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("hash-a", number(100), number(20), number(80), number(2)),
	}}); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 3, 9, 16, 15, 0, 0, loc) }
	if err := m.storeNetWorthSnapshot(ctx, authorizedAt.UnixMilli(), portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("hash-a", number(110), number(25), number(85), number(3)),
		snapshotAccount("hash-b", number(50), number(10), number(40), number(1)),
	}}); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Date(2026, 3, 10, 16, 15, 0, 0, loc) }
	err := m.storeNetWorthSnapshot(ctx, authorizedAt.UnixMilli(), portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("hash-a", number(112), number(25), number(87), number(2)),
		snapshotAccount("hash-b", number(51), nil, number(41), number(1)),
	}})
	if err == nil {
		t.Fatal("incomplete account was not reported")
	}

	history, err := m.loadNetWorthHistory(ctx, "all", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Accounts) != 2 || len(history.Points) != 3 {
		t.Fatalf("history=%+v", history)
	}
	if got := history.Points[0]; len(got.AccountHashes) != 1 || got.NetWorth == nil || *got.NetWorth != 100 {
		t.Fatalf("first point=%+v", got)
	}
	if got := history.Points[1]; len(got.AccountHashes) != 2 || got.NetWorth == nil || *got.NetWorth != 160 {
		t.Fatalf("aggregate point=%+v", got)
	}
	if got := history.Points[2]; len(got.AccountHashes) != 1 || got.NetWorth == nil || *got.NetWorth != 112 {
		t.Fatalf("partial day=%+v", got)
	}
	selected, err := m.loadNetWorthHistory(ctx, "all", "hash-b")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Points[0].NetWorth != nil || selected.Points[1].NetWorth == nil || selected.Points[2].NetWorth != nil {
		t.Fatalf("selected missing values were filled: %+v", selected.Points)
	}
}

func TestNetWorthHistoryScopesCurrentAuthorization(t *testing.T) {
	m := openInvestmentModule(t)
	ctx := context.Background()
	now := time.Date(2026, 4, 1, 16, 15, 0, 0, m.marketLocation)
	m.now = func() time.Time { return now }
	oldAuth := now.Add(-48 * time.Hour)
	newAuth := now.Add(-24 * time.Hour)
	if err := m.storeNetWorthSnapshot(ctx, oldAuth.UnixMilli(), portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("old-hash", number(100), number(20), number(80), number(1)),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := m.storeNetWorthSnapshot(ctx, newAuth.UnixMilli(), portfolioResponse{Accounts: []portfolioAccount{
		snapshotAccount("new-hash", number(200), number(30), number(170), number(2)),
	}}); err != nil {
		t.Fatal(err)
	}
	seedSchwab(t, m, schwabRecord{AppKey: "key", AppSecret: "secret", AccessToken: "access", RefreshToken: "refresh", AuthorizedAt: newAuth})
	history, err := m.loadNetWorthHistory(ctx, "all", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Accounts) != 1 || history.Accounts[0] != "new-hash" || len(history.Points) != 1 || history.Points[0].NetWorth == nil || *history.Points[0].NetWorth != 200 {
		t.Fatalf("history leaked old authorization: %+v", history)
	}
}

func TestNetWorthRangeUsesNewYorkCalendarAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation(netWorthTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 21, 0, 0, 0, time.UTC)
	start, err := netWorthStartDay(now, "1m", loc)
	if err != nil || start != "2026-10-06" {
		t.Fatalf("start=%q err=%v", start, err)
	}
	start, err = netWorthStartDay(now, "1y", loc)
	if err != nil || start != "2025-11-06" {
		t.Fatalf("year start=%q err=%v", start, err)
	}
}

func TestCaptureNetWorthDoesNotWriteWhenDisabledOrUnauthorized(t *testing.T) {
	m := openInvestmentModule(t)
	m.monitorMu.Lock()
	m.monitorEnabled = false
	m.monitorMu.Unlock()
	if err := m.captureNetWorth(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.monitorMu.Lock()
	m.monitorEnabled = true
	m.monitorMu.Unlock()
	if err := m.captureNetWorth(context.Background()); err != nil {
		t.Fatalf("unconfigured capture should be idle: %v", err)
	}
	var count int
	if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_networth_snapshots").Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshots=%d err=%v", count, err)
	}
}

func TestNetWorthJobSchedule(t *testing.T) {
	m := openInvestmentModule(t)
	job := m.netWorthJob()
	if job.Schedule.Expression != "15 16 * * 1-5" || job.TimeZone != netWorthTimeZone || job.Timeout != 45*time.Second {
		t.Fatalf("job=%+v", job)
	}
}

func TestNetWorthStartupCapturesWithoutWaitingForMarketClose(t *testing.T) {
	m := portfolioModule(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/trader/v1/accounts/accountNumbers":
			io.WriteString(w, `[{"accountNumber":"1234","hashValue":"startup-hash"}]`)
		case "/trader/v1/accounts":
			io.WriteString(w, `[{"securitiesAccount":{"accountNumber":"1234","currentBalances":{"liquidationValue":100,"cashBalance":100},"positions":[]}}]`)
		default:
			t.Errorf("snapshot should not request quotes: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if err := m.OnEnabledChanged(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int
		if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_networth_snapshots WHERE account_hash='startup-hash'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup did not capture a snapshot")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNetWorthDisableCancelsAnInflightStartupCapture(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	m := portfolioModule(t, func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	})
	if err := m.OnEnabledChanged(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("startup capture did not start")
	}
	if err := m.OnEnabledChanged(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("disabling the module did not cancel capture")
	}
	var count int
	if err := m.deps.DB.QueryRow("SELECT COUNT(*) FROM investment_networth_snapshots").Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled capture wrote %d rows: %v", count, err)
	}
}
