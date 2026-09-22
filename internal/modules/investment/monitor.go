package investment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/identity"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

func (m *Module) monitorJob() contracts.JobDefinition {
	return contracts.JobDefinition{ID: "investment.monitor_prices", Module: "investment",
		Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Minute}, TimeZone: "America/New_York",
		Timeout: 45 * time.Second, OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
		Retry: contracts.RetryPolicy{MaxAttempts: 1}, Handler: func(ctx context.Context, _ contracts.JobRun) error { return m.scanPrices(ctx) }}
}

func (m *Module) monitorState(ctx context.Context, status, message string) error {
	return m.queries.UpdatePriceMonitor(ctx, investmentsqlc.UpdatePriceMonitorParams{Status: status, CheckedAt: sql.NullInt64{Int64: m.now().UnixMilli(), Valid: true}, LastError: message})
}

func (m *Module) scanPrices(ctx context.Context) error {
	m.scanMu.Lock()
	defer m.scanMu.Unlock()
	m.monitorMu.Lock()
	active := m.monitorEnabled
	m.monitorMu.Unlock()
	if !active {
		return nil
	}
	rules, err := m.queries.ListPriceRules(ctx)
	if err != nil {
		return err
	}
	symbols := make(map[string]struct{})
	for _, rule := range rules {
		if rule.Enabled == 1 {
			symbols[rule.Symbol] = struct{}{}
		}
	}
	if len(symbols) == 0 {
		return m.monitorState(ctx, "idle", "")
	}
	fail := func(err error) error { return errors.Join(err, m.monitorState(ctx, "error", err.Error())) }
	if m.deps.Notifications == nil {
		return fail(errors.New("通知服务不可用"))
	}
	token, err := m.ensureAccessToken(ctx)
	if err != nil {
		return fail(err)
	}
	session, open, err := m.regularSession(ctx, token, m.now())
	if err != nil {
		return fail(err)
	}
	if !open {
		return m.monitorState(ctx, "closed", "")
	}
	quotes, err := m.fetchMonitorQuotes(ctx, token, symbols)
	if err != nil {
		return fail(err)
	}

	// Configuration changes, deletions and disable operations can finish while
	// HTTP is in flight. Only commit against the still-current rule and token.
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	if !m.monitorEnabled {
		return nil
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return err
	}
	if rec.AccessToken != token || rec.ReauthorizationRequired {
		return fail(errors.New("Schwab 授权已变更，等待下一次检查"))
	}
	now := m.now().UTC()
	if now.Before(session.Start) || !now.Before(session.End) {
		return m.monitorState(ctx, "closed", "")
	}
	hasInvalid := false
	for _, rule := range rules {
		if rule.Enabled != 1 {
			continue
		}
		quote := quotes[rule.Symbol]
		validation := quote.validate(rule.Symbol, now, session)
		hasInvalid = hasInvalid || validation != nil
		if err := m.evaluatePrice(ctx, rule, quote, validation, now); err != nil {
			return fail(err)
		}
	}
	if hasInvalid {
		return m.monitorState(ctx, "error", "部分标的行情不可用，请查看规则中的原因")
	}
	return m.monitorState(ctx, "monitoring", "")
}

// Quote state, the daily trigger and the notification event commit together.
// A failed write cannot consume the daily allowance without queuing a notice.
func (m *Module) evaluatePrice(ctx context.Context, rule investmentsqlc.InvestmentPriceRule, quote monitoredQuote, invalid error, now time.Time) error {
	tx, err := m.deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := investmentsqlc.New(tx)
	current, err := queries.GetPriceRule(ctx, rule.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Enabled != 1 || current.Version != rule.Version {
		return nil
	}
	update := investmentsqlc.UpdatePriceRuleQuoteParams{ID: rule.ID, Version: rule.Version, CheckedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}}
	if invalid != nil {
		update.LastError = invalid.Error()
		if err := queries.UpdatePriceRuleQuote(ctx, update); err != nil {
			return err
		}
		return tx.Commit()
	}
	change := quote.changePercent()
	update.Price = sql.NullFloat64{Float64: quote.Regular.Price, Valid: true}
	update.PreviousClose = sql.NullFloat64{Float64: quote.Quote.ClosePrice, Valid: true}
	update.ChangePercent = sql.NullFloat64{Float64: change, Valid: true}
	update.QuoteAt = sql.NullInt64{Int64: quote.Regular.Time, Valid: true}
	if err := queries.UpdatePriceRuleQuote(ctx, update); err != nil {
		return err
	}
	threshold := float64(rule.ThresholdBps) / 100
	triggered := (rule.Direction == "up" && change+1e-9 >= threshold) || (rule.Direction == "down" && change-1e-9 <= -threshold)
	if !triggered {
		return tx.Commit()
	}
	id, err := identity.New()
	if err != nil {
		return err
	}
	date := now.In(m.marketLocation).Format("2006-01-02")
	count, err := queries.CreatePriceTrigger(ctx, investmentsqlc.CreatePriceTriggerParams{ID: id, RuleID: rule.ID, TradingDate: date,
		Symbol: rule.Symbol, Direction: rule.Direction, ThresholdBps: rule.ThresholdBps, Price: quote.Regular.Price, PreviousClose: quote.Quote.ClosePrice,
		ChangePercent: change, QuoteAt: quote.Regular.Time, TriggeredAt: now.UnixMilli()})
	if err != nil {
		return err
	}
	if count == 0 {
		return tx.Commit()
	}
	direction := "上涨"
	if rule.Direction == "down" {
		direction = "下跌"
	}
	notice, err := m.deps.Notifications.CreateTx(ctx, tx, contracts.NewNotification{SourceModule: "investment", Severity: contracts.NotificationWarning,
		Title:       fmt.Sprintf("%s %s达到 %.2f%%", rule.Symbol, direction, threshold),
		Content:     fmt.Sprintf("最新价：%.4f USD\n前一交易日收盘价：%.4f USD\n涨跌幅：%+.2f%%\n行情时间：%s\n常规交易时段 · 每条规则每交易日提醒一次", quote.Regular.Price, quote.Quote.ClosePrice, change, time.UnixMilli(quote.Regular.Time).In(m.marketLocation).Format("2006-01-02 15:04:05 MST")),
		ActionLabel: "查看价格监控", ActionRoute: "/investment?tab=monitor", IdempotencyKey: "investment:monitor:" + rule.ID + ":" + date})
	if err != nil {
		return err
	}
	if err := queries.SetPriceTriggerNotification(ctx, investmentsqlc.SetPriceTriggerNotificationParams{ID: id, NotificationID: notice.ID}); err != nil {
		return err
	}
	return tx.Commit()
}
