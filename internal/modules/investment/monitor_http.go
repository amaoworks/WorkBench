package investment

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

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

func (m *Module) getPriceMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := m.queries.ListPriceRules(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "monitor_failed", "无法读取监控规则")
		return
	}
	state, err := m.queries.GetPriceMonitor(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "monitor_failed", "无法读取监控状态")
		return
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
	httpapi.Write(w, 200, map[string]any{"items": items, "status": state.Status, "checkedAt": priceTime(state.CheckedAt), "lastError": state.LastError, "intervalSeconds": 60})
}

func (m *Module) savePriceRule(w http.ResponseWriter, r *http.Request) {
	var input priceRuleInput
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_rule", "规则格式不正确")
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	bps := math.Round(input.ThresholdPercent * 100)
	if !stockSymbol.MatchString(input.Symbol) || (input.Direction != "up" && input.Direction != "down") || input.Enabled == nil ||
		math.IsNaN(bps) || math.IsInf(bps, 0) || bps < 1 || bps > 100000 || (input.Direction == "down" && bps > 10000) || math.Abs(input.ThresholdPercent*100-bps) > 0.000001 {
		httpapi.Error(w, 400, "invalid_rule", "请输入美股或 ETF 代码、上涨/下跌方向，以及最多两位小数的正百分比（上涨不超过 1000%，下跌不超过 100%）")
		return
	}
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var enabled int64
	if *input.Enabled {
		enabled = 1
	}
	now := m.now().UTC().UnixMilli()
	status := http.StatusOK
	if r.Method == http.MethodPost {
		count, err := m.queries.CountPriceRules(ctx)
		if err != nil {
			httpapi.Error(w, 500, "rule_failed", "无法读取规则数量")
			return
		}
		if count >= maxPriceRules {
			httpapi.Error(w, 400, "rule_limit", "最多保存 100 条监控规则")
			return
		}
		id, err = identity.New()
		if err == nil {
			err = m.queries.CreatePriceRule(ctx, investmentsqlc.CreatePriceRuleParams{ID: id, Symbol: input.Symbol, Direction: input.Direction, ThresholdBps: int64(bps), Enabled: enabled, CreatedAt: now, UpdatedAt: now})
		}
		if err != nil {
			httpapi.Error(w, 500, "rule_failed", "无法创建监控规则")
			return
		}
		status = http.StatusCreated
	} else {
		count, err := m.queries.UpdatePriceRule(ctx, investmentsqlc.UpdatePriceRuleParams{ID: id, Symbol: input.Symbol, Direction: input.Direction, ThresholdBps: int64(bps), Enabled: enabled, UpdatedAt: now})
		if err != nil {
			httpapi.Error(w, 500, "rule_failed", "无法更新监控规则")
			return
		}
		if count == 0 {
			httpapi.Error(w, 404, "rule_not_found", "监控规则不存在")
			return
		}
	}
	row, err := m.queries.GetPriceRule(ctx, id)
	if err != nil {
		httpapi.Error(w, 500, "rule_failed", "无法读取监控规则")
		return
	}
	httpapi.Write(w, status, ruleView(row))
}

func (m *Module) deletePriceRule(w http.ResponseWriter, r *http.Request) {
	m.monitorMu.Lock()
	defer m.monitorMu.Unlock()
	count, err := m.queries.DeletePriceRule(r.Context(), chi.URLParam(r, "id"))
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
	rows, err := m.queries.ListPriceTriggers(r.Context(), investmentsqlc.ListPriceTriggersParams{BeforeTime: position.Time, BeforeID: position.ID, PageLimit: 51})
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
