package investment

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
	"workbench/internal/contracts"

	"github.com/go-chi/chi/v5"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/identity"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const maxPriceRules = 100

var stockSymbol = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,14}$`)

type priceRuleInput struct {
	Symbol           string  `json:"symbol"`
	Direction        string  `json:"direction"`
	ThresholdPercent float64 `json:"thresholdPercent"`
	Enabled          *bool   `json:"enabled"`
}

type priceRuleView struct {
	ID               string     `json:"id"`
	Symbol           string     `json:"symbol"`
	Direction        string     `json:"direction"`
	ThresholdPercent float64    `json:"thresholdPercent"`
	Enabled          bool       `json:"enabled"`
	Price            *float64   `json:"price"`
	PreviousClose    *float64   `json:"previousClose"`
	ChangePercent    *float64   `json:"changePercent"`
	QuoteAt          *time.Time `json:"quoteAt"`
	CheckedAt        *time.Time `json:"checkedAt"`
	LastError        string     `json:"lastError"`
}

func priceTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.UnixMilli(value.Int64).UTC()
	return &t
}

func priceNumber(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func ruleView(row investmentsqlc.InvestmentPriceRule) priceRuleView {
	return priceRuleView{ID: row.ID, Symbol: row.Symbol, Direction: row.Direction,
		ThresholdPercent: float64(row.ThresholdBps) / 100, Enabled: row.Enabled == 1,
		Price: priceNumber(row.Price), PreviousClose: priceNumber(row.PreviousClose), ChangePercent: priceNumber(row.ChangePercent),
		QuoteAt: priceTime(row.QuoteAt), CheckedAt: priceTime(row.CheckedAt), LastError: row.LastError}
}

type monitorOperationError struct {
	status        int
	code, message string
}

func (e *monitorOperationError) Error() string { return e.message }

func writeMonitorError(w http.ResponseWriter, err error) {
	var operationError *monitorOperationError
	if errors.As(err, &operationError) {
		httpapi.Error(w, operationError.status, operationError.code, operationError.message)
		return
	}
	httpapi.Error(w, http.StatusInternalServerError, "monitor_failed", err.Error())
}

func (m *Module) priceMonitor(ctx context.Context) (map[string]any, error) {
	rows, err := m.queries.ListPriceRules(ctx, contracts.UserID(ctx))
	if err != nil {
		return nil, &monitorOperationError{status: 500, code: "monitor_failed", message: "无法读取监控规则"}
	}
	state, err := m.queries.GetPriceMonitor(ctx, contracts.UserID(ctx))
	if errors.Is(err, sql.ErrNoRows) {
		state.Status = "waiting"
		err = nil
	}
	if err != nil {
		return nil, &monitorOperationError{status: 500, code: "monitor_failed", message: "无法读取监控状态"}
	}
	items := make([]priceRuleView, 0, len(rows))
	active := false
	for _, row := range rows {
		items = append(items, ruleView(row))
		active = active || row.Enabled == 1
	}
	if !active {
		state.Status, state.LastError = "idle", ""
	} else if state.CheckedAt.Valid && m.now().Sub(time.UnixMilli(state.CheckedAt.Int64)) > 3*time.Minute {
		state.Status, state.LastError = "stale", "后台检查已超过三分钟未更新"
	} else if state.Status == "idle" {
		state.Status = "waiting"
	}
	return map[string]any{"items": items, "status": state.Status, "checkedAt": priceTime(state.CheckedAt), "lastError": state.LastError, "intervalSeconds": 60}, nil
}

func (m *Module) getPriceMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := m.priceMonitor(r.Context())
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	httpapi.Write(w, 200, result)
}

func (m *Module) savePriceRuleValue(ctx context.Context, id string, input priceRuleInput, create bool) (priceRuleView, int, error) {
	if _, err := m.requireUser(ctx); err != nil {
		return priceRuleView{}, 0, err
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	bps := math.Round(input.ThresholdPercent * 100)
	if !stockSymbol.MatchString(input.Symbol) || (input.Direction != "up" && input.Direction != "down") || input.Enabled == nil ||
		math.IsNaN(bps) || math.IsInf(bps, 0) || bps < 1 || bps > 100000 || (input.Direction == "down" && bps > 10000) || math.Abs(input.ThresholdPercent*100-bps) > 0.000001 {
		return priceRuleView{}, 0, &monitorOperationError{status: 400, code: "invalid_rule", message: "请输入美股或 ETF 代码、上涨/下跌方向，以及最多两位小数的正百分比（上涨不超过 1000%，下跌不超过 100%）"}
	}
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	var enabled int64
	if *input.Enabled {
		enabled = 1
	}
	now := m.now().UTC().UnixMilli()
	status := http.StatusOK
	if create {
		count, err := m.queries.CountPriceRules(ctx, contracts.UserID(ctx))
		if err != nil {
			return priceRuleView{}, 0, &monitorOperationError{status: 500, code: "rule_failed", message: "无法读取规则数量"}
		}
		if count >= maxPriceRules {
			return priceRuleView{}, 0, &monitorOperationError{status: 400, code: "rule_limit", message: "最多保存 100 条监控规则"}
		}
		id, err = identity.New()
		if err == nil {
			err = m.queries.CreatePriceRule(ctx, investmentsqlc.CreatePriceRuleParams{UserID: contracts.UserID(ctx), ID: id, Symbol: input.Symbol, Direction: input.Direction, ThresholdBps: int64(bps), Enabled: enabled, CreatedAt: now, UpdatedAt: now})
		}
		if err != nil {
			return priceRuleView{}, 0, &monitorOperationError{status: 500, code: "rule_failed", message: "无法创建监控规则"}
		}
		status = http.StatusCreated
	} else {
		count, err := m.queries.UpdatePriceRule(ctx, investmentsqlc.UpdatePriceRuleParams{UserID: contracts.UserID(ctx), ID: id, Symbol: input.Symbol, Direction: input.Direction, ThresholdBps: int64(bps), Enabled: enabled, UpdatedAt: now})
		if err != nil {
			return priceRuleView{}, 0, &monitorOperationError{status: 500, code: "rule_failed", message: "无法更新监控规则"}
		}
		if count == 0 {
			return priceRuleView{}, 0, &monitorOperationError{status: 404, code: "rule_not_found", message: "监控规则不存在"}
		}
	}
	row, err := m.queries.GetPriceRule(ctx, investmentsqlc.GetPriceRuleParams{UserID: contracts.UserID(ctx), ID: id})
	if err != nil {
		return priceRuleView{}, 0, &monitorOperationError{status: 500, code: "rule_failed", message: "无法读取监控规则"}
	}
	return ruleView(row), status, nil
}

func (m *Module) savePriceRule(w http.ResponseWriter, r *http.Request) {
	var input priceRuleInput
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_rule", "规则格式不正确")
		return
	}
	result, status, err := m.savePriceRuleValue(r.Context(), chi.URLParam(r, "id"), input, r.Method == http.MethodPost)
	if err != nil {
		writeMonitorError(w, err)
		return
	}
	httpapi.Write(w, status, result)
}

func (m *Module) deletePriceRule(w http.ResponseWriter, r *http.Request) {
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	count, err := m.queries.DeletePriceRule(r.Context(), investmentsqlc.DeletePriceRuleParams{UserID: contracts.UserID(r.Context()), ID: chi.URLParam(r, "id")})
	if err != nil {
		httpapi.Error(w, 500, "rule_failed", "无法删除监控规则")
		return
	}
	if count == 0 {
		httpapi.Error(w, 404, "rule_not_found", "监控规则不存在")
		return
	}
	httpapi.Write(w, 200, map[string]bool{"deleted": true})
}

type triggerCursor struct {
	Time int64  `json:"time"`
	ID   string `json:"id"`
}
type priceTriggerView struct {
	ID               string    `json:"id"`
	RuleID           string    `json:"ruleId"`
	Symbol           string    `json:"symbol"`
	Direction        string    `json:"direction"`
	ThresholdPercent float64   `json:"thresholdPercent"`
	Price            float64   `json:"price"`
	PreviousClose    float64   `json:"previousClose"`
	ChangePercent    float64   `json:"changePercent"`
	QuoteAt          time.Time `json:"quoteAt"`
	TriggeredAt      time.Time `json:"triggeredAt"`
	TradingDate      string    `json:"tradingDate"`
	NotificationID   string    `json:"notificationId"`
}

func (m *Module) getPriceHistory(w http.ResponseWriter, r *http.Request) {
	position := triggerCursor{Time: math.MaxInt64, ID: "\uffff"}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &position) != nil || position.ID == "" || position.Time <= 0 {
			httpapi.Error(w, 400, "invalid_cursor", "分页位置无效")
			return
		}
	}
	rows, err := m.queries.ListPriceTriggers(r.Context(), investmentsqlc.ListPriceTriggersParams{UserID: contracts.UserID(r.Context()), BeforeTime: position.Time, BeforeID: position.ID, PageLimit: 51})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpapi.Error(w, 500, "history_failed", "无法读取触发记录")
		return
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(triggerCursor{Time: last.TriggeredAt, ID: last.ID})
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	items := make([]priceTriggerView, 0, len(rows))
	for _, row := range rows {
		items = append(items, priceTriggerView{ID: row.ID, RuleID: row.RuleID, Symbol: row.Symbol, Direction: row.Direction, ThresholdPercent: float64(row.ThresholdBps) / 100,
			Price: row.Price, PreviousClose: row.PreviousClose, ChangePercent: row.ChangePercent, QuoteAt: time.UnixMilli(row.QuoteAt).UTC(), TriggeredAt: time.UnixMilli(row.TriggeredAt).UTC(), TradingDate: row.TradingDate, NotificationID: row.NotificationID})
	}
	httpapi.Write(w, 200, map[string]any{"items": items, "nextCursor": next})
}
