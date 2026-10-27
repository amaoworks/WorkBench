package investment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const netWorthTimeZone = "America/New_York"

func (m *Module) startNetWorthSnapshot(ctx context.Context) {
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	if !m.monitorEnabled {
		return
	}
	if m.netWorthCancel != nil {
		m.netWorthCancel()
	}
	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	m.netWorthCancel = cancel
	m.netWorthWG.Add(1)
	go func() {
		defer m.netWorthWG.Done()
		defer cancel()
		if err := m.captureNetWorth(jobCtx); err != nil && !errors.Is(err, context.Canceled) {
			m.logger.Warn("net worth startup snapshot failed", "error", err)
		}
	}()
}

type netWorthPoint struct {
	Day           string   `json:"day"`
	AccountHashes []string `json:"accountHashes"`
	NetWorth      *float64 `json:"netWorth"`
	Cash          *float64 `json:"cash"`
	Invested      *float64 `json:"invested"`
	DayPnl        *float64 `json:"dayPnl"`
}

type netWorthHistory struct {
	Status    string          `json:"status"`
	Message   string          `json:"message,omitempty"`
	Currency  string          `json:"currency"`
	Range     string          `json:"range"`
	Account   string          `json:"account,omitempty"`
	Accounts  []string        `json:"accounts"`
	Points    []netWorthPoint `json:"points"`
	UpdatedAt *time.Time      `json:"updatedAt,omitempty"`
}

func (m *Module) netWorthJob() contracts.JobDefinition {
	return contracts.JobDefinition{
		ID: "investment.capture_networth", Module: "investment",
		Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleCron, Expression: "15 16 * * 1-5"},
		TimeZone: netWorthTimeZone, Timeout: 45 * time.Second,
		OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
		Retry:   contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Minute, MaxWait: 5 * time.Minute},
		Handler: func(ctx context.Context, _ contracts.JobRun) error { return m.captureNetWorth(ctx) },
	}
}

func (m *Module) captureNetWorth(ctx context.Context) error {
	m.monitorMu.Lock()
	active := m.monitorEnabled
	m.monitorMu.Unlock()
	if !active {
		return nil
	}
	record, err := m.loadSchwab(ctx)
	if err != nil {
		return err
	}
	if record.AuthorizedAt.IsZero() {
		return nil
	}
	token, status, message := m.portfolioToken(ctx)
	if status != "ready" {
		if status == "unconfigured" || status == "reauth" {
			return nil
		}
		return fmt.Errorf("net worth snapshot unavailable: %s", message)
	}
	portfolio, err := m.fetchPortfolio(ctx, token, false)
	if err != nil {
		return err
	}
	return m.withCurrentSchwabToken(ctx, token, func() error {
		m.monitorMu.Lock()
		defer m.monitorMu.Unlock()
		if !m.monitorEnabled {
			return nil
		}
		rec, err := m.loadSchwab(ctx)
		if err != nil {
			return err
		}
		if rec.AuthorizedAt.IsZero() {
			return errors.New("net worth snapshot requires an interactive Schwab authorization")
		}
		return m.storeNetWorthSnapshot(ctx, rec.AuthorizedAt.UnixMilli(), portfolio)
	})
}

func (m *Module) storeNetWorthSnapshot(ctx context.Context, authorizedAt int64, portfolio portfolioResponse) error {
	now := m.now().UTC()
	day := now.In(m.marketLocation).Format("2006-01-02")
	tx, err := m.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := m.queries.WithTx(tx)
	if err := queries.DeleteNetWorthAccountScope(ctx, authorizedAt); err != nil {
		return err
	}
	skipped := make([]string, 0)
	for _, account := range portfolio.Accounts {
		if account.Hash == "" {
			return errors.New("net worth snapshot account hash is missing")
		}
		if err := queries.AddNetWorthAccountScope(ctx, investmentsqlc.AddNetWorthAccountScopeParams{
			AuthorizedAt: authorizedAt, AccountHash: account.Hash,
		}); err != nil {
			return err
		}
		if account.NetWorth == nil || account.Cash == nil || account.Invested == nil || account.DayPnl == nil {
			skipped = append(skipped, account.Hash)
			continue
		}
		if err := queries.UpsertNetWorthSnapshot(ctx, investmentsqlc.UpsertNetWorthSnapshotParams{
			AccountHash: account.Hash, Day: day, NetWorth: *account.NetWorth, Cash: *account.Cash,
			Invested: *account.Invested, DayPnl: *account.DayPnl, CapturedAt: now.UnixMilli(),
		}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(skipped) > 0 {
		slices.Sort(skipped)
		return fmt.Errorf("net worth snapshot skipped incomplete accounts: %s", strings.Join(skipped, ", "))
	}
	return nil
}

func netWorthStartDay(now time.Time, value string, loc *time.Location) (string, error) {
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	switch value {
	case "1m":
		return today.AddDate(0, -1, 0).Format("2006-01-02"), nil
	case "3m":
		return today.AddDate(0, -3, 0).Format("2006-01-02"), nil
	case "1y":
		return today.AddDate(-1, 0, 0).Format("2006-01-02"), nil
	case "all":
		return "", nil
	default:
		return "", errors.New("range must be 1m, 3m, 1y, or all")
	}
}

func (m *Module) loadNetWorthHistory(ctx context.Context, rangeValue, account string) (netWorthHistory, error) {
	out := netWorthHistory{Status: "ready", Currency: "USD", Range: rangeValue, Account: account, Accounts: []string{}, Points: []netWorthPoint{}}
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return out, err
	}
	switch schwabConnectionState(rec) {
	case "reauth":
		out.Status, out.Message = "reauth", "Schwab 授权已失效"
		return out, nil
	case "unconfigured", "disconnected":
		out.Status, out.Message = "unconfigured", "尚未连接 Schwab"
		return out, nil
	}
	startDay, err := netWorthStartDay(m.now(), rangeValue, m.marketLocation)
	if err != nil {
		return out, err
	}
	authorizedAt := rec.AuthorizedAt.UnixMilli()
	out.Accounts, err = m.queries.ListNetWorthAccounts(ctx, authorizedAt)
	if err != nil {
		return out, err
	}
	if out.Accounts == nil {
		out.Accounts = []string{}
	}
	if account != "" && !slices.Contains(out.Accounts, account) {
		return out, errors.New("account is not available for the current authorization")
	}
	rows, err := m.queries.ListNetWorthSnapshots(ctx, investmentsqlc.ListNetWorthSnapshotsParams{AuthorizedAt: authorizedAt, StartDay: startDay})
	if err != nil || len(rows) == 0 {
		return out, err
	}
	out.Points, out.UpdatedAt, err = m.netWorthPoints(rows, startDay, account)
	return out, err
}

func (m *Module) netWorthPoints(rows []investmentsqlc.InvestmentNetworthSnapshot, startDay, account string) ([]netWorthPoint, *time.Time, error) {
	byDay := make(map[string][]investmentsqlc.InvestmentNetworthSnapshot)
	actualDays := make(map[string]bool)
	var updatedAt int64
	for _, row := range rows {
		byDay[row.Day] = append(byDay[row.Day], row)
		actualDays[row.Day] = true
		updatedAt = max(updatedAt, row.CapturedAt)
	}
	if startDay == "" {
		startDay = rows[0].Day
	}
	start, err := time.ParseInLocation("2006-01-02", startDay, m.marketLocation)
	if err != nil {
		return nil, nil, err
	}
	localNow := m.now().In(m.marketLocation)
	end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, m.marketLocation)
	points := make([]netWorthPoint, 0)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		weekday := day.Weekday()
		if !actualDays[key] && (weekday == time.Saturday || weekday == time.Sunday) {
			continue
		}
		points = append(points, aggregateNetWorthPoint(key, byDay[key], account))
	}
	updated := time.UnixMilli(updatedAt).UTC()
	return points, &updated, nil
}

func aggregateNetWorthPoint(day string, rows []investmentsqlc.InvestmentNetworthSnapshot, account string) netWorthPoint {
	point := netWorthPoint{Day: day, AccountHashes: []string{}}
	var netWorth, cash, invested, dayPnl float64
	for _, row := range rows {
		if account != "" && row.AccountHash != account {
			continue
		}
		point.AccountHashes = append(point.AccountHashes, row.AccountHash)
		netWorth += row.NetWorth
		cash += row.Cash
		invested += row.Invested
		dayPnl += row.DayPnl
	}
	if len(point.AccountHashes) == 0 {
		return point
	}
	point.NetWorth, point.Cash, point.Invested, point.DayPnl = &netWorth, &cash, &invested, &dayPnl
	return point
}

func (m *Module) getNetWorth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rangeValue := r.URL.Query().Get("range")
	if rangeValue == "" {
		rangeValue = "1m"
	}
	if _, err := netWorthStartDay(m.now(), rangeValue, m.marketLocation); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_networth_query", "时间范围无效")
		return
	}
	out, err := m.loadNetWorthHistory(r.Context(), rangeValue, strings.TrimSpace(r.URL.Query().Get("account")))
	if err != nil {
		if strings.Contains(err.Error(), "range must") || strings.Contains(err.Error(), "account is not available") {
			httpapi.Error(w, http.StatusBadRequest, "invalid_networth_query", err.Error())
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "networth_history_failed", "无法读取净值历史")
		return
	}
	httpapi.Write(w, http.StatusOK, out)
}
