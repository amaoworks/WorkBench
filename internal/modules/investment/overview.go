package investment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

// Overview cards read Schwab through short-lived caches so opening the
// dashboard never fans out to the broker on every request.
const (
	overviewSnapshotTTL  = 45 * time.Second
	overviewQuotesTTL    = 30 * time.Second
	overviewSparkTTL     = 5 * time.Minute
	overviewStatusTTL    = time.Minute
	overviewStaleLimit   = 10 * time.Minute
	overviewFetchTimeout = 8 * time.Second
	overviewSparkBudget  = 4 * time.Second
	overviewMaxSymbols   = 12
	overviewMaxHoldings  = 8
	overviewSparkWorkers = 4
	schwabRefreshTTL     = 7 * 24 * time.Hour
)

var overviewSymbolPattern = regexp.MustCompile(`^[A-Z$/][A-Z0-9./-]{0,14}$`)

type overviewState struct {
	snapshotMu sync.Mutex // serializes snapshot refreshes
	quotesMu   sync.Mutex // serializes quote refreshes
	mu         sync.Mutex
	snapshot   *overviewSnapshot
	quotes     map[string]overviewQuotesEntry
	sparks     map[string]overviewSparkEntry
	futu       *overviewFutuEntry
}

type overviewQuotesEntry struct {
	at    time.Time
	items []overviewQuote
}

type overviewSparkEntry struct {
	at     time.Time
	points []float64
}

type overviewFutuEntry struct {
	at     time.Time
	status overviewFutuStatus
}

type overviewMover struct {
	Symbol      string  `json:"symbol"`
	Description string  `json:"description"`
	MarketValue float64 `json:"marketValue"`
	DayPnl      float64 `json:"dayPnl"`
	DayPnlPct   float64 `json:"dayPnlPct"`
}

type overviewSnapshot struct {
	Status      string          `json:"status"` // ok, disconnected, reauth, error
	Message     string          `json:"message,omitempty"`
	Stale       bool            `json:"stale,omitempty"`
	NetWorth    float64         `json:"netWorth"`
	DayPnl      float64         `json:"dayPnl"`
	DayPnlPct   float64         `json:"dayPnlPct"`
	Cash        float64         `json:"cash"`
	BuyingPower float64         `json:"buyingPower"`
	Invested    float64         `json:"invested"`
	Accounts    int             `json:"accounts"`
	Positions   int             `json:"positions"`
	Movers      []overviewMover `json:"movers"`
	Symbols     []string        `json:"symbols"`
	UpdatedAt   *time.Time      `json:"updatedAt,omitempty"`
}

type overviewQuote struct {
	Symbol       string    `json:"symbol"`
	Description  string    `json:"description"`
	Price        float64   `json:"price"`
	ChangePct    float64   `json:"changePct"`
	Session      string    `json:"session"` // pre, regular, post, overnight, closed
	ExtChangePct *float64  `json:"extChangePct,omitempty"`
	Held         bool      `json:"held"`
	Watched      bool      `json:"watched"`
	Spark        []float64 `json:"spark,omitempty"`
}

type overviewQuotes struct {
	Status    string          `json:"status"`
	Message   string          `json:"message,omitempty"`
	Session   string          `json:"session"`
	Items     []overviewQuote `json:"items"`
	UpdatedAt *time.Time      `json:"updatedAt,omitempty"`
}

type overviewSchwabStatus struct {
	Status           string     `json:"status"` // connected, reauth, disconnected, unconfigured
	LastError        string     `json:"lastError,omitempty"`
	TokenExpiresAt   *time.Time `json:"tokenExpiresAt,omitempty"`
	RefreshExpiresAt *time.Time `json:"refreshExpiresAt,omitempty"`
}

type overviewFutuStatus struct {
	Enabled      bool   `json:"enabled"`
	ServiceState string `json:"serviceState,omitempty"`
	Connected    bool   `json:"connected"`
	QotLogined   bool   `json:"qotLogined"`
	LastError    string `json:"lastError,omitempty"`
}

type overviewTrigger struct {
	ID            string    `json:"id"`
	Symbol        string    `json:"symbol"`
	Direction     string    `json:"direction"`
	Threshold     float64   `json:"thresholdPercent"`
	Price         float64   `json:"price"`
	ChangePercent float64   `json:"changePercent"`
	TriggeredAt   time.Time `json:"triggeredAt"`
}

type overviewAlerts struct {
	Schwab   overviewSchwabStatus `json:"schwab"`
	Futu     overviewFutuStatus   `json:"futu"`
	Triggers []overviewTrigger    `json:"triggers"`
}

// --- schwab account snapshot ---

type schwabAccountsResponse []struct {
	SecuritiesAccount struct {
		Positions []struct {
			LongQuantity                   float64 `json:"longQuantity"`
			ShortQuantity                  float64 `json:"shortQuantity"`
			MarketValue                    float64 `json:"marketValue"`
			CurrentDayProfitLoss           float64 `json:"currentDayProfitLoss"`
			CurrentDayProfitLossPercentage float64 `json:"currentDayProfitLossPercentage"`
			Instrument                     struct {
				AssetType   string `json:"assetType"`
				Symbol      string `json:"symbol"`
				Description string `json:"description"`
			} `json:"instrument"`
		} `json:"positions"`
		CurrentBalances struct {
			LiquidationValue        float64 `json:"liquidationValue"`
			CashBalance             float64 `json:"cashBalance"`
			TotalCash               float64 `json:"totalCash"`
			BuyingPower             float64 `json:"buyingPower"`
			CashAvailableForTrading float64 `json:"cashAvailableForTrading"`
			LongMarketValue         float64 `json:"longMarketValue"`
		} `json:"currentBalances"`
	} `json:"securitiesAccount"`
}

func (m *Module) readTraderJSON(ctx context.Context, token, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.schwabAPI+"/trader/v1/"+path, nil)
	if err != nil {
		return errors.New("无法创建账户请求")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return errors.New("暂时无法连接 Schwab 账户服务")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		if err := m.refreshRejectedAccessToken(ctx, token); err != nil {
			return err
		}
		return errors.New("账户授权已更新，请稍后刷新")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Schwab 账户查询失败（HTTP %d）", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(target); err != nil {
		return errors.New("Schwab 账户响应格式无效")
	}
	return nil
}

func schwabConnectionState(rec schwabRecord) string {
	switch {
	case rec.ReauthorizationRequired:
		return "reauth"
	case rec.RefreshToken != "":
		return "connected"
	case rec.AppKey != "":
		return "disconnected"
	default:
		return "unconfigured"
	}
}

// overviewDetached keeps a refresh running when one dashboard request goes away,
// so the next caller can reuse the warmed cache.
func overviewDetached(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (m *Module) overviewSnapshot(ctx context.Context) overviewSnapshot {
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return overviewSnapshot{Status: "error", Message: "无法读取 Schwab 配置", Movers: []overviewMover{}, Symbols: []string{}}
	}
	switch schwabConnectionState(rec) {
	case "reauth":
		return overviewSnapshot{Status: "reauth", Message: "Schwab 授权已失效，请重新授权。", Movers: []overviewMover{}, Symbols: []string{}}
	case "disconnected", "unconfigured":
		return overviewSnapshot{Status: "disconnected", Movers: []overviewMover{}, Symbols: []string{}}
	}
	state := &m.overview
	state.snapshotMu.Lock()
	defer state.snapshotMu.Unlock()
	now := m.now()
	state.mu.Lock()
	cached := state.snapshot
	state.mu.Unlock()
	if cached != nil && cached.UpdatedAt != nil && now.Sub(*cached.UpdatedAt) < overviewSnapshotTTL {
		return *cached
	}
	fetchCtx, cancel := overviewDetached(ctx, overviewFetchTimeout)
	defer cancel()
	fresh, err := m.fetchSnapshot(fetchCtx)
	if err != nil {
		if cached != nil && cached.UpdatedAt != nil && now.Sub(*cached.UpdatedAt) < overviewStaleLimit {
			stale := *cached
			stale.Stale, stale.Message = true, err.Error()
			return stale
		}
		return overviewSnapshot{Status: "error", Message: err.Error(), Movers: []overviewMover{}, Symbols: []string{}}
	}
	state.mu.Lock()
	state.snapshot = &fresh
	state.mu.Unlock()
	return fresh
}

func (m *Module) fetchSnapshot(ctx context.Context) (overviewSnapshot, error) {
	token, err := m.ensureAccessToken(ctx)
	if err != nil {
		return overviewSnapshot{}, err
	}
	if token == "" {
		return overviewSnapshot{}, errors.New("Schwab 尚未授权")
	}
	var accounts schwabAccountsResponse
	if err := m.readTraderJSON(ctx, token, "accounts?fields=positions", &accounts); err != nil {
		return overviewSnapshot{}, err
	}
	return buildSnapshot(accounts, m.now()), nil
}

func buildSnapshot(accounts schwabAccountsResponse, now time.Time) overviewSnapshot {
	out := overviewSnapshot{Status: "ok", Accounts: len(accounts), Movers: []overviewMover{}, Symbols: []string{}}
	held := map[string]*overviewMover{}
	for _, account := range accounts {
		sa := account.SecuritiesAccount
		balances := sa.CurrentBalances
		out.NetWorth += balances.LiquidationValue
		cash := balances.CashBalance
		if cash == 0 {
			cash = balances.TotalCash
		}
		out.Cash += cash
		power := balances.BuyingPower
		if power == 0 {
			power = balances.CashAvailableForTrading
		}
		out.BuyingPower += power
		for _, p := range sa.Positions {
			out.DayPnl += p.CurrentDayProfitLoss
			out.Invested += p.MarketValue
			symbol := strings.ToUpper(strings.TrimSpace(p.Instrument.Symbol))
			if symbol == "" || p.Instrument.AssetType == "OPTION" || p.Instrument.AssetType == "CASH_EQUIVALENT" {
				continue
			}
			mover := held[symbol]
			if mover == nil {
				mover = &overviewMover{Symbol: symbol, Description: p.Instrument.Description}
				held[symbol] = mover
			}
			mover.MarketValue += p.MarketValue
			mover.DayPnl += p.CurrentDayProfitLoss
		}
	}
	movers := make([]overviewMover, 0, len(held))
	for _, mover := range held {
		if base := mover.MarketValue - mover.DayPnl; base != 0 {
			mover.DayPnlPct = mover.DayPnl / math.Abs(base) * 100
		}
		movers = append(movers, *mover)
	}
	out.Positions = len(movers)
	sort.Slice(movers, func(i, j int) bool {
		if movers[i].MarketValue != movers[j].MarketValue {
			return movers[i].MarketValue > movers[j].MarketValue
		}
		return movers[i].Symbol < movers[j].Symbol
	})
	for _, mover := range movers {
		out.Symbols = append(out.Symbols, mover.Symbol)
	}
	sort.SliceStable(movers, func(i, j int) bool { return math.Abs(movers[i].DayPnlPct) > math.Abs(movers[j].DayPnlPct) })
	if len(movers) > 3 {
		movers = movers[:3]
	}
	out.Movers = movers
	if base := out.NetWorth - out.DayPnl; base > 0 {
		out.DayPnlPct = out.DayPnl / base * 100
	}
	updated := now.UTC()
	out.UpdatedAt = &updated
	return out
}

func (m *Module) getOverviewSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, m.overviewSnapshot(r.Context()))
}

// --- quotes ---

// marketPhase classifies US equity trading by New York wall-clock time.
// Exchange holidays are not modelled; quotes still carry their own prices.
func marketPhase(now time.Time, loc *time.Location) string {
	t := now.In(loc)
	minutes := t.Hour()*60 + t.Minute()
	day := t.Weekday()
	switch {
	case day == time.Saturday, day == time.Sunday && minutes < 20*60, day == time.Friday && minutes >= 20*60:
		return "closed"
	case minutes >= 4*60 && minutes < 9*60+30:
		return "pre"
	case minutes >= 9*60+30 && minutes < 16*60:
		return "regular"
	case minutes >= 16*60 && minutes < 20*60:
		return "post"
	default:
		return "overnight"
	}
}

func normalizeOverviewSymbol(raw string) string {
	symbol := strings.ToUpper(strings.TrimSpace(raw))
	if i := strings.LastIndex(symbol, ":"); i >= 0 {
		symbol = symbol[i+1:]
	}
	if !overviewSymbolPattern.MatchString(symbol) {
		return ""
	}
	return symbol
}

func (m *Module) watchlistSymbols(ctx context.Context) []string {
	rec, err := m.queries.GetWatchlists(ctx)
	if err != nil {
		return nil
	}
	var state *watchlistsState
	if json.Unmarshal([]byte(rec.Content), &state) != nil || state == nil {
		return nil
	}
	var symbols []string
	for _, list := range state.Lists {
		if state.ActiveID != "" && list.ID != state.ActiveID {
			continue
		}
		for _, raw := range list.Symbols {
			if strings.HasPrefix(raw, "###") {
				continue
			}
			if symbol := normalizeOverviewSymbol(raw); symbol != "" {
				symbols = append(symbols, symbol)
			}
		}
	}
	return symbols
}

type schwabQuoteEntry struct {
	Reference struct {
		Description string `json:"description"`
	} `json:"reference"`
	Quote struct {
		LastPrice               float64 `json:"lastPrice"`
		ClosePrice              float64 `json:"closePrice"`
		NetPercentChange        float64 `json:"netPercentChange"`
		PostMarketPercentChange float64 `json:"postMarketPercentChange"`
	} `json:"quote"`
	Regular struct {
		RegularMarketLastPrice     float64 `json:"regularMarketLastPrice"`
		RegularMarketPercentChange float64 `json:"regularMarketPercentChange"`
	} `json:"regular"`
}

func quoteFromSchwab(symbol string, entry schwabQuoteEntry, phase string) (overviewQuote, bool) {
	q := overviewQuote{Symbol: symbol, Description: entry.Reference.Description, Session: phase}
	regular := entry.Regular.RegularMarketLastPrice
	last := entry.Quote.LastPrice
	switch phase {
	case "regular":
		q.Price, q.ChangePct = firstPositive(regular, last), entry.Regular.RegularMarketPercentChange
		if regular == 0 {
			q.ChangePct = entry.Quote.NetPercentChange
		}
	case "pre":
		q.Price, q.ChangePct = firstPositive(last, regular), entry.Quote.NetPercentChange
		ext := entry.Quote.NetPercentChange
		q.ExtChangePct = &ext
	default:
		q.Price, q.ChangePct = firstPositive(last, regular), entry.Regular.RegularMarketPercentChange
		if phase == "post" || phase == "overnight" {
			ext := entry.Quote.PostMarketPercentChange
			q.ExtChangePct = &ext
		}
	}
	if q.Price <= 0 || math.IsNaN(q.Price) || math.IsInf(q.Price, 0) {
		return overviewQuote{}, false
	}
	return q, true
}

func firstPositive(values ...float64) float64 {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func (m *Module) overviewQuotes(ctx context.Context) overviewQuotes {
	now := m.now()
	phase := marketPhase(now, m.marketLocation)
	snapshot := m.overviewSnapshot(ctx)
	if snapshot.Status == "disconnected" || snapshot.Status == "reauth" {
		return overviewQuotes{Status: snapshot.Status, Message: snapshot.Message, Session: phase, Items: []overviewQuote{}}
	}
	held := map[string]bool{}
	symbols := []string{}
	for _, symbol := range snapshot.Symbols {
		if len(symbols) >= overviewMaxHoldings {
			break
		}
		if symbol = normalizeOverviewSymbol(symbol); symbol != "" && !held[symbol] {
			held[symbol] = true
			symbols = append(symbols, symbol)
		}
	}
	watched := map[string]bool{}
	for _, symbol := range m.watchlistSymbols(ctx) {
		watched[symbol] = true
		if len(symbols) < overviewMaxSymbols && !slices.Contains(symbols, symbol) {
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		return overviewQuotes{Status: "ok", Session: phase, Items: []overviewQuote{}}
	}
	key := strings.Join(symbols, ",")
	state := &m.overview
	state.quotesMu.Lock()
	defer state.quotesMu.Unlock()
	state.mu.Lock()
	cached, ok := state.quotes[key]
	state.mu.Unlock()
	if ok && now.Sub(cached.at) < overviewQuotesTTL {
		return overviewQuotes{Status: "ok", Session: phase, Items: cached.items, UpdatedAt: &cached.at}
	}
	fetchCtx, cancel := overviewDetached(ctx, overviewFetchTimeout)
	defer cancel()
	items, err := m.fetchOverviewQuotes(fetchCtx, symbols, phase)
	if err != nil {
		if ok && now.Sub(cached.at) < overviewStaleLimit {
			return overviewQuotes{Status: "ok", Message: err.Error(), Session: phase, Items: cached.items, UpdatedAt: &cached.at}
		}
		return overviewQuotes{Status: "error", Message: err.Error(), Session: phase, Items: []overviewQuote{}}
	}
	for i := range items {
		items[i].Held, items[i].Watched = held[items[i].Symbol], watched[items[i].Symbol]
	}
	at := now.UTC()
	state.mu.Lock()
	if state.quotes == nil || len(state.quotes) > 16 {
		state.quotes = map[string]overviewQuotesEntry{}
	}
	state.quotes[key] = overviewQuotesEntry{at: at, items: items}
	state.mu.Unlock()
	return overviewQuotes{Status: "ok", Session: phase, Items: items, UpdatedAt: &at}
}

func (m *Module) fetchOverviewQuotes(ctx context.Context, symbols []string, phase string) ([]overviewQuote, error) {
	token, err := m.ensureAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var response map[string]schwabQuoteEntry
	path := "quotes?symbols=" + url.QueryEscape(strings.Join(symbols, ",")) + "&fields=quote,regular,reference"
	if err := m.readMarketJSON(ctx, token, path, &response); err != nil {
		return nil, err
	}
	items := make([]overviewQuote, 0, len(symbols))
	for _, symbol := range symbols {
		entry, ok := response[symbol]
		if !ok {
			continue
		}
		if q, ok := quoteFromSchwab(symbol, entry, phase); ok {
			items = append(items, q)
		}
	}
	sparks := m.overviewSparks(ctx, token, symbols)
	for i := range items {
		items[i].Spark = sparks[items[i].Symbol]
	}
	return items, nil
}

// overviewSparks returns recent 30-minute closes per symbol. Missing or slow
// series are left out; the card then renders without a trend line.
func (m *Module) overviewSparks(ctx context.Context, token string, symbols []string) map[string][]float64 {
	out := map[string][]float64{}
	now := m.now()
	state := &m.overview
	var missing []string
	state.mu.Lock()
	for _, symbol := range symbols {
		if entry, ok := state.sparks[symbol]; ok && now.Sub(entry.at) < overviewSparkTTL {
			out[symbol] = entry.points
		} else {
			missing = append(missing, symbol)
		}
	}
	state.mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, overviewSparkBudget)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan string)
	for range min(overviewSparkWorkers, len(missing)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for symbol := range jobs {
				var response struct {
					Candles []struct {
						Close float64 `json:"close"`
					} `json:"candles"`
				}
				path := "pricehistory?symbol=" + url.QueryEscape(symbol) + "&periodType=day&period=2&frequencyType=minute&frequency=30&needExtendedHoursData=false"
				if m.readMarketJSON(ctx, token, path, &response) != nil {
					continue
				}
				points := make([]float64, 0, 13)
				candles := response.Candles
				if len(candles) > 13 {
					candles = candles[len(candles)-13:]
				}
				for _, candle := range candles {
					if candle.Close > 0 {
						points = append(points, candle.Close)
					}
				}
				if len(points) < 2 {
					continue
				}
				mu.Lock()
				out[symbol] = points
				mu.Unlock()
			}
		}()
	}
feed:
	for _, symbol := range missing {
		select {
		case jobs <- symbol:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	state.mu.Lock()
	if state.sparks == nil || len(state.sparks) > 64 {
		state.sparks = map[string]overviewSparkEntry{}
	}
	for _, symbol := range missing {
		if points, ok := out[symbol]; ok {
			state.sparks[symbol] = overviewSparkEntry{at: now, points: points}
		}
	}
	state.mu.Unlock()
	return out
}

func (m *Module) getOverviewQuotes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, m.overviewQuotes(r.Context()))
}

// --- alerts ---

func (m *Module) overviewFutu(ctx context.Context) overviewFutuStatus {
	state := &m.overview
	now := m.now()
	state.mu.Lock()
	cached := state.futu
	state.mu.Unlock()
	if cached != nil && now.Sub(cached.at) < overviewStatusTTL {
		return cached.status
	}
	rec, err := m.loadFutu(ctx)
	if err != nil {
		return overviewFutuStatus{LastError: "无法读取富途配置"}
	}
	status := overviewFutuStatus{Enabled: rec.Enabled, LastError: rec.LastError}
	if !rec.Enabled {
		return status
	}
	if m.opend != nil {
		var message string
		status.ServiceState, message = m.opend.status()
		if message != "" {
			status.LastError = message
		}
	}
	if m.opend == nil || status.ServiceState == "running" {
		checkCtx, cancel := overviewDetached(ctx, 2*time.Second)
		defer cancel()
		if client, err := m.futu.ensure(checkCtx); err == nil {
			if loggedIn, err := client.globalState(checkCtx); err == nil {
				status.Connected, status.QotLogined, status.LastError = true, loggedIn, ""
			} else {
				status.LastError = err.Error()
			}
		} else {
			status.LastError = err.Error()
		}
	}
	state.mu.Lock()
	state.futu = &overviewFutuEntry{at: now, status: status}
	state.mu.Unlock()
	return status
}

func (m *Module) overviewAlerts(ctx context.Context) (overviewAlerts, error) {
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return overviewAlerts{}, err
	}
	out := overviewAlerts{Schwab: overviewSchwabStatus{Status: schwabConnectionState(rec), LastError: rec.LastError}, Triggers: []overviewTrigger{}}
	if out.Schwab.Status == "connected" {
		if !rec.TokenExpiresAt.IsZero() {
			expires := rec.TokenExpiresAt.UTC()
			out.Schwab.TokenExpiresAt = &expires
		}
		if !rec.AuthorizedAt.IsZero() {
			expires := rec.AuthorizedAt.Add(schwabRefreshTTL).UTC()
			out.Schwab.RefreshExpiresAt = &expires
		}
	}
	out.Futu = m.overviewFutu(ctx)
	rows, err := m.queries.ListPriceTriggers(ctx, investmentsqlc.ListPriceTriggersParams{BeforeTime: math.MaxInt64, BeforeID: "\uffff", PageLimit: 5})
	if err != nil {
		return overviewAlerts{}, err
	}
	since := m.now().Add(-72 * time.Hour).UnixMilli()
	for _, row := range rows {
		if row.TriggeredAt < since {
			continue
		}
		out.Triggers = append(out.Triggers, overviewTrigger{ID: row.ID, Symbol: row.Symbol, Direction: row.Direction, Threshold: float64(row.ThresholdBps) / 100,
			Price: row.Price, ChangePercent: row.ChangePercent, TriggeredAt: time.UnixMilli(row.TriggeredAt).UTC()})
	}
	return out, nil
}

func (m *Module) getOverviewAlerts(w http.ResponseWriter, r *http.Request) {
	out, err := m.overviewAlerts(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "overview_alerts_failed", "无法读取提醒与连接状态")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, out)
}
