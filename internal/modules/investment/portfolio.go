package investment

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
)

type portfolioAccount struct {
	Hash     string   `json:"accountHash"`
	Label    string   `json:"label"`
	NetWorth *float64 `json:"netWorth"`
	Cash     *float64 `json:"cash"`
	Invested *float64 `json:"invested"`
	DayPnl   *float64 `json:"dayPnl"`
}

type portfolioPosition struct {
	AccountHash      string   `json:"accountHash"`
	AccountLabel     string   `json:"accountLabel"`
	Symbol           string   `json:"symbol"`
	Description      string   `json:"description"`
	AssetType        string   `json:"assetType"`
	Quantity         *float64 `json:"quantity"`
	AveragePrice     *float64 `json:"averagePrice"`
	LastPrice        *float64 `json:"lastPrice"`
	MarketValue      *float64 `json:"marketValue"`
	DayPnl           *float64 `json:"dayPnl"`
	DayPnlPct        *float64 `json:"dayPnlPct"`
	UnrealizedPnl    *float64 `json:"unrealizedPnl"`
	UnrealizedPnlPct *float64 `json:"unrealizedPnlPct"`
	Weight           *float64 `json:"weight"`
}

type portfolioResponse struct {
	Status    string              `json:"status"`
	Message   string              `json:"message,omitempty"`
	Accounts  []portfolioAccount  `json:"accounts"`
	Items     []portfolioPosition `json:"items"`
	UpdatedAt *time.Time          `json:"updatedAt,omitempty"`
}

type traderInstrument struct {
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	AssetType   string `json:"assetType"`
}

type traderPosition struct {
	LongQuantity                   *float64         `json:"longQuantity"`
	ShortQuantity                  *float64         `json:"shortQuantity"`
	AveragePrice                   *float64         `json:"averagePrice"`
	MarketValue                    *float64         `json:"marketValue"`
	CurrentDayProfitLoss           *float64         `json:"currentDayProfitLoss"`
	CurrentDayProfitLossPercentage *float64         `json:"currentDayProfitLossPercentage"`
	LongOpenProfitLoss             *float64         `json:"longOpenProfitLoss"`
	ShortOpenProfitLoss            *float64         `json:"shortOpenProfitLoss"`
	Instrument                     traderInstrument `json:"instrument"`
}

type traderAccount struct {
	SecuritiesAccount struct {
		AccountNumber json.RawMessage  `json:"accountNumber"`
		Type          string           `json:"type"`
		Positions     []traderPosition `json:"positions"`
		Balances      struct {
			LiquidationValue *float64 `json:"liquidationValue"`
			CashBalance      *float64 `json:"cashBalance"`
			TotalCash        *float64 `json:"totalCash"`
		} `json:"currentBalances"`
	} `json:"securitiesAccount"`
}

type traderAccountIdentity struct {
	AccountNumber json.RawMessage `json:"accountNumber"`
	Hash          string          `json:"hashValue"`
}

func traderNumber(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func accountLabel(number, kind string) string {
	if len(number) > 4 {
		number = number[len(number)-4:]
	}
	return strings.TrimSpace(kind + " ••" + number)
}

func (m *Module) portfolioToken(ctx context.Context) (string, string, string) {
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return "", "error", "无法读取 Schwab 配置"
	}
	switch schwabConnectionState(rec) {
	case "unconfigured", "disconnected":
		return "", "unconfigured", "尚未连接 Schwab"
	case "reauth":
		return "", "reauth", "Schwab 授权已失效"
	}
	token, err := m.ensureAccessToken(ctx)
	if err != nil {
		if errors.Is(err, errSchwabReauthorizationRequired) {
			return "", "reauth", err.Error()
		}
		return "", "error", err.Error()
	}
	return token, "ready", ""
}

func (m *Module) accountIdentities(ctx context.Context, token string) (map[string]string, error) {
	var raw []traderAccountIdentity
	if err := m.readTraderJSON(ctx, token, "accounts/accountNumbers", &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for _, entry := range raw {
		if number := traderNumber(entry.AccountNumber); number != "" && entry.Hash != "" {
			out[number] = entry.Hash
		}
	}
	return out, nil
}

func nullableSum(values ...*float64) *float64 {
	total := 0.0
	for _, value := range values {
		if value == nil {
			return nil
		}
		total += *value
	}
	return &total
}

func valueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func positionOpenPnl(p traderPosition) *float64 {
	if p.LongQuantity == nil && p.ShortQuantity == nil {
		return nil
	}
	values := []*float64{}
	if valueOrZero(p.LongQuantity) > 0 {
		values = append(values, p.LongOpenProfitLoss)
	}
	if valueOrZero(p.ShortQuantity) > 0 {
		values = append(values, p.ShortOpenProfitLoss)
	}
	return nullableSum(values...)
}

func (m *Module) fetchPortfolio(ctx context.Context, token string, includeQuotes bool) (portfolioResponse, error) {
	out := portfolioResponse{Status: "ready", Accounts: []portfolioAccount{}, Items: []portfolioPosition{}}
	identities, err := m.accountIdentities(ctx, token)
	if err != nil {
		return out, err
	}
	var raw []traderAccount
	if err := m.readTraderJSON(ctx, token, "accounts?fields=positions", &raw); err != nil {
		return out, err
	}
	symbols := []string{}
	for _, entry := range raw {
		account := entry.SecuritiesAccount
		number := traderNumber(account.AccountNumber)
		hash := identities[number]
		if hash == "" {
			return out, errors.New("Schwab 账户标识缺失")
		}
		label := accountLabel(number, account.Type)
		marketValues, dayPnls := []*float64{}, []*float64{}
		for _, rawPosition := range account.Positions {
			p := portfolioPosition{
				AccountHash: hash, AccountLabel: label, Symbol: rawPosition.Instrument.Symbol,
				Description: rawPosition.Instrument.Description, AssetType: rawPosition.Instrument.AssetType,
				AveragePrice: rawPosition.AveragePrice, MarketValue: rawPosition.MarketValue,
				DayPnl: rawPosition.CurrentDayProfitLoss, DayPnlPct: rawPosition.CurrentDayProfitLossPercentage,
				UnrealizedPnl: positionOpenPnl(rawPosition),
			}
			if p.Symbol == "" {
				return out, errors.New("Schwab 持仓代码缺失")
			}
			if rawPosition.LongQuantity != nil || rawPosition.ShortQuantity != nil {
				quantity := valueOrZero(rawPosition.LongQuantity) - valueOrZero(rawPosition.ShortQuantity)
				p.Quantity = &quantity
			}
			if p.MarketValue != nil && p.UnrealizedPnl != nil {
				if cost := math.Abs(*p.MarketValue - *p.UnrealizedPnl); cost > 0 {
					pct := *p.UnrealizedPnl / cost * 100
					p.UnrealizedPnlPct = &pct
				}
			}
			out.Items = append(out.Items, p)
			marketValues = append(marketValues, p.MarketValue)
			dayPnls = append(dayPnls, p.DayPnl)
			if !slices.Contains(symbols, p.Symbol) {
				symbols = append(symbols, p.Symbol)
			}
		}
		cash := account.Balances.CashBalance
		if cash == nil {
			cash = account.Balances.TotalCash
		}
		out.Accounts = append(out.Accounts, portfolioAccount{Hash: hash, Label: label,
			NetWorth: account.Balances.LiquidationValue, Cash: cash, Invested: nullableSum(marketValues...), DayPnl: nullableSum(dayPnls...)})
	}
	prices := map[string]*float64{}
	if !includeQuotes {
		symbols = nil
	}
	for batch := range slices.Chunk(symbols, 100) {
		var quotes map[string]struct {
			Quote struct {
				LastPrice *float64 `json:"lastPrice"`
			} `json:"quote"`
		}
		path := "quotes?symbols=" + url.QueryEscape(strings.Join(batch, ",")) + "&fields=quote"
		if err := m.readMarketJSON(ctx, token, path, &quotes); err != nil {
			out.Message = "部分行情暂不可用"
			continue
		}
		for symbol, quote := range quotes {
			prices[symbol] = quote.Quote.LastPrice
		}
	}
	values := make([]*float64, 0, len(out.Accounts))
	for _, account := range out.Accounts {
		values = append(values, account.Invested)
	}
	invested := nullableSum(values...)
	for i := range out.Items {
		p := &out.Items[i]
		p.LastPrice = prices[p.Symbol]
		if p.MarketValue != nil && invested != nil && *invested != 0 {
			weight := *p.MarketValue / *invested * 100
			p.Weight = &weight
		}
	}
	at := m.now().UTC()
	out.UpdatedAt = &at
	return out, nil
}

func (m *Module) getPositions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, status, message := m.portfolioToken(r.Context())
	out := portfolioResponse{Status: status, Message: message, Accounts: []portfolioAccount{}, Items: []portfolioPosition{}}
	if status == "ready" {
		fresh, err := m.fetchPortfolio(r.Context(), token, true)
		if err == nil {
			out = fresh
		} else {
			out.Status, out.Message = "error", err.Error()
			if errors.Is(err, errSchwabReauthorizationRequired) {
				out.Status = "reauth"
			}
		}
	}
	httpapi.Write(w, http.StatusOK, out)
}

type traderOrderLeg struct {
	LegID       *int             `json:"legId"`
	Instruction string           `json:"instruction"`
	Quantity    *float64         `json:"quantity"`
	Instrument  traderInstrument `json:"instrument"`
}

type traderExecution struct {
	LegID    *int     `json:"legId"`
	Price    *float64 `json:"price"`
	Quantity *float64 `json:"quantity"`
}

type traderOrder struct {
	ID            json.RawMessage  `json:"orderId"`
	AccountNumber json.RawMessage  `json:"accountNumber"`
	EnteredAt     string           `json:"enteredTime"`
	ClosedAt      string           `json:"closeTime"`
	Status        string           `json:"status"`
	OrderType     string           `json:"orderType"`
	Duration      string           `json:"duration"`
	Quantity      *float64         `json:"quantity"`
	Filled        *float64         `json:"filledQuantity"`
	Price         *float64         `json:"price"`
	StopPrice     *float64         `json:"stopPrice"`
	Legs          []traderOrderLeg `json:"orderLegCollection"`
	Activities    []struct {
		Type       string            `json:"activityType"`
		Executions []traderExecution `json:"executionLegs"`
	} `json:"orderActivityCollection"`
	Children []traderOrder `json:"childOrderStrategies"`
}

type portfolioOrderLeg struct {
	Symbol      string   `json:"symbol"`
	Instruction string   `json:"instruction"`
	Quantity    *float64 `json:"quantity"`
	Filled      *float64 `json:"filledQuantity"`
	AverageFill *float64 `json:"averageFill"`
}

func orderLegFill(order traderOrder, index int) (*float64, *float64) {
	quantity, total := 0.0, 0.0
	for _, activity := range order.Activities {
		if activity.Type != "" && activity.Type != "EXECUTION" {
			continue
		}
		for _, execution := range activity.Executions {
			matches := len(order.Legs) == 1 || (execution.LegID != nil && order.Legs[index].LegID != nil && *execution.LegID == *order.Legs[index].LegID)
			if !matches {
				if execution.LegID == nil {
					return nil, nil
				}
				continue
			}
			if execution.Quantity == nil || execution.Price == nil || *execution.Quantity <= 0 {
				return nil, nil
			}
			quantity += *execution.Quantity
			total += *execution.Quantity * *execution.Price
		}
	}
	if quantity == 0 {
		return nil, nil
	}
	average := total / quantity
	return &quantity, &average
}

type portfolioOrder struct {
	ID           string              `json:"id"`
	AccountHash  string              `json:"accountHash"`
	AccountLabel string              `json:"accountLabel"`
	Legs         []portfolioOrderLeg `json:"legs"`
	OrderType    string              `json:"orderType"`
	Duration     string              `json:"duration"`
	Quantity     *float64            `json:"quantity"`
	Filled       *float64            `json:"filledQuantity"`
	Price        *float64            `json:"price"`
	StopPrice    *float64            `json:"stopPrice"`
	AverageFill  *float64            `json:"averageFill"`
	Status       string              `json:"status"`
	Group        string              `json:"group"`
	EnteredAt    string              `json:"enteredAt"`
	ClosedAt     string              `json:"closedAt"`
}

type ordersResponse struct {
	Status    string           `json:"status"`
	Message   string           `json:"message,omitempty"`
	Items     []portfolioOrder `json:"items"`
	UpdatedAt *time.Time       `json:"updatedAt,omitempty"`
}

func orderGroup(status string) string {
	switch status {
	case "FILLED":
		return "filled"
	case "CANCELED", "REJECTED", "EXPIRED", "REPLACED":
		return "canceled"
	case "AWAITING_PARENT_ORDER", "AWAITING_CONDITION", "AWAITING_STOP_CONDITION", "AWAITING_MANUAL_REVIEW", "ACCEPTED", "AWAITING_UR_OUT", "PENDING_ACTIVATION", "QUEUED", "WORKING", "NEW", "AWAITING_RELEASE_TIME", "PENDING_ACKNOWLEDGEMENT", "PENDING_CANCEL", "PENDING_REPLACE", "PENDING_RECALL":
		return "working"
	default:
		return "other"
	}
}

func orderAverageFill(order traderOrder) *float64 {
	if len(order.Legs) == 0 {
		return nil
	}
	type fill struct{ quantity, value float64 }
	fills := make([]fill, len(order.Legs))
	for _, activity := range order.Activities {
		if activity.Type != "" && activity.Type != "EXECUTION" {
			continue
		}
		for _, execution := range activity.Executions {
			if execution.Price == nil || execution.Quantity == nil || *execution.Quantity <= 0 {
				return nil
			}
			index := -1
			if len(order.Legs) == 1 {
				index = 0
			} else if execution.LegID != nil {
				for i, leg := range order.Legs {
					if leg.LegID != nil && *leg.LegID == *execution.LegID {
						index = i
						break
					}
				}
			}
			if index < 0 {
				return nil
			}
			fills[index].quantity += *execution.Quantity
			fills[index].value += *execution.Quantity * *execution.Price
		}
	}
	if len(order.Legs) == 1 {
		if fills[0].quantity == 0 {
			return nil
		}
		average := fills[0].value / fills[0].quantity
		return &average
	}
	if order.Quantity == nil || *order.Quantity <= 0 {
		return nil
	}
	average, fraction := 0.0, -1.0
	for i, leg := range order.Legs {
		if leg.Quantity == nil || *leg.Quantity <= 0 || fills[i].quantity == 0 {
			return nil
		}
		part := fills[i].quantity / *leg.Quantity
		if fraction >= 0 && math.Abs(part-fraction) > 1e-8 {
			return nil // Unmatched partial legs do not have a completed strategy price.
		}
		fraction = part
		sign := 0.0
		if strings.HasPrefix(leg.Instruction, "BUY") {
			sign = 1
		} else if strings.HasPrefix(leg.Instruction, "SELL") {
			sign = -1
		} else {
			return nil
		}
		average += sign * fills[i].value / fills[i].quantity * (*leg.Quantity / *order.Quantity)
	}
	return &average
}

const portfolioOrderLimit = 500

func (m *Module) readPortfolioOrders(ctx context.Context, token string, from, until time.Time) ([]traderOrder, error) {
	requests := 0
	var read func(time.Time, time.Time) ([]traderOrder, error)
	read = func(start, end time.Time) ([]traderOrder, error) {
		requests++
		if requests > 128 {
			return nil, errors.New("订单过多，无法完整读取")
		}
		query := url.Values{"fromEnteredTime": {start.UTC().Format(time.RFC3339Nano)}, "toEnteredTime": {end.UTC().Format(time.RFC3339Nano)}, "maxResults": {strconv.Itoa(portfolioOrderLimit)}}
		var orders []traderOrder
		if err := m.readTraderJSON(ctx, token, "orders?"+query.Encode(), &orders); err != nil {
			return nil, err
		}
		if len(orders) < portfolioOrderLimit {
			return orders, nil
		}
		if end.Sub(start) <= time.Millisecond {
			return nil, errors.New("订单结果达到上限，无法完整读取")
		}
		middle := start.Add(end.Sub(start) / 2)
		left, err := read(start, middle)
		if err != nil {
			return nil, err
		}
		right, err := read(middle, end)
		return append(left, right...), err
	}
	return read(from, until)
}

func normalizePortfolioOrders(raw []traderOrder, identities map[string]string, filter string) ([]portfolioOrder, error) {
	items := []portfolioOrder{}
	seen := map[string]bool{}
	var visit func(traderOrder, json.RawMessage) error
	visit = func(order traderOrder, inherited json.RawMessage) error {
		if traderNumber(order.AccountNumber) == "" {
			order.AccountNumber = inherited
		}
		if len(order.Legs) > 0 {
			number, id := traderNumber(order.AccountNumber), traderNumber(order.ID)
			hash := identities[number]
			if hash == "" || id == "" {
				return errors.New("Schwab 订单或账户标识缺失")
			}
			key := hash + ":" + id
			group := orderGroup(order.Status)
			if !seen[key] && (filter == "all" || filter == group) {
				legs := make([]portfolioOrderLeg, 0, len(order.Legs))
				for i, leg := range order.Legs {
					filled, average := orderLegFill(order, i)
					legs = append(legs, portfolioOrderLeg{Symbol: leg.Instrument.Symbol, Instruction: leg.Instruction,
						Quantity: leg.Quantity, Filled: filled, AverageFill: average})
				}
				items = append(items, portfolioOrder{ID: id, AccountHash: hash, AccountLabel: accountLabel(number, ""),
					Legs: legs, OrderType: order.OrderType, Duration: order.Duration, Quantity: order.Quantity, Filled: order.Filled,
					Price: order.Price, StopPrice: order.StopPrice, AverageFill: orderAverageFill(order), Status: order.Status,
					Group: group, EnteredAt: order.EnteredAt, ClosedAt: order.ClosedAt})
				seen[key] = true
			}
		}
		for _, child := range order.Children {
			if err := visit(child, order.AccountNumber); err != nil {
				return err
			}
		}
		return nil
	}
	for _, order := range raw {
		if err := visit(order, nil); err != nil {
			return nil, err
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].EnteredAt == items[j].EnteredAt {
			return items[i].AccountHash+":"+items[i].ID < items[j].AccountHash+":"+items[j].ID
		}
		left, leftErr := time.Parse(time.RFC3339Nano, items[i].EnteredAt)
		right, rightErr := time.Parse(time.RFC3339Nano, items[j].EnteredAt)
		if leftErr == nil && rightErr == nil {
			return left.After(right)
		}
		return items[i].EnteredAt > items[j].EnteredAt
	})
	return items, nil
}

func (m *Module) getOrders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	days := 7
	var err error
	if query.Has("days") {
		days, err = strconv.Atoi(query.Get("days"))
	}
	if err != nil || !slices.Contains([]int{1, 7, 30, 90}, days) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_range", "时间范围无效")
		return
	}
	filter := query.Get("status")
	if filter == "" {
		filter = "all"
	}
	if !slices.Contains([]string{"all", "working", "filled", "canceled"}, filter) {
		httpapi.Error(w, http.StatusBadRequest, "invalid_status", "订单状态无效")
		return
	}
	location := m.marketLocation
	if name := query.Get("tz"); name != "" {
		location, err = time.LoadLocation(name)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid_timezone", "时区无效")
			return
		}
	}
	now := m.now().In(location)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -(days - 1))
	token, status, message := m.portfolioToken(r.Context())
	out := ordersResponse{Status: status, Message: message, Items: []portfolioOrder{}}
	if status == "ready" {
		identities, err := m.accountIdentities(r.Context(), token)
		if err == nil {
			var raw []traderOrder
			raw, err = m.readPortfolioOrders(r.Context(), token, from, now)
			if err == nil {
				out.Items, err = normalizePortfolioOrders(raw, identities, filter)
			}
		}
		if err != nil {
			out.Status, out.Message, out.Items = "error", err.Error(), []portfolioOrder{}
			if errors.Is(err, errSchwabReauthorizationRequired) {
				out.Status = "reauth"
			}
		} else {
			at := m.now().UTC()
			out.UpdatedAt = &at
		}
	}
	httpapi.Write(w, http.StatusOK, out)
}
