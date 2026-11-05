package investment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
)

// Field names and enum values follow FutunnOpen/py-futu-api's Qot_Common,
// Qot_GetStaticInfo, Qot_GetBasicQot and Common protobuf definitions.
type futuQuote struct {
	Symbol      string   `json:"symbol"`
	Description string   `json:"description"`
	Price       float64  `json:"price"`
	Change      *float64 `json:"change"`
	ChangePct   *float64 `json:"changePct"`
	Volume      *float64 `json:"volume,omitempty"`
	High        *float64 `json:"high,omitempty"`
	Low         *float64 `json:"low,omitempty"`
	Time        *int64   `json:"time,omitempty"`
	Session     string   `json:"session"`
}

type futuSymbol struct {
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	Exchange    string `json:"exchange"`
	Type        string `json:"type"`
}

var futuUSSymbol = regexp.MustCompile(`^[A-Z][A-Z0-9.\-]{0,14}$`)
var errFutuDisabled = errors.New("富途行情未启用")
var errFutuSymbol = errors.New("仅支持美股和 ETF 代码")

func normalizeFutuSymbol(symbol string) (string, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !futuUSSymbol.MatchString(symbol) {
		return "", errFutuSymbol
	}
	return symbol, nil
}

func futuSession(session string) (string, error) {
	if session == "" {
		return "night", nil
	}
	switch session {
	case "regular", "extended", "premarket", "postmarket", "night", "equity24h":
		return session, nil
	default:
		return "", errors.New("无效的交易时段")
	}
}

func intradayFutuResolution(resolution string) bool {
	return resolution != "1D" && resolution != "1W" && resolution != "1M"
}

func filterFutuBars(bars []futuBar, session, resolution string) []futuBar {
	out := make([]futuBar, 0, len(bars))
	for _, bar := range bars {
		if !intradayFutuResolution(resolution) {
			if session == "regular" || session == "extended" || session == "equity24h" {
				out = append(out, bar)
			}
			continue
		}
		t := time.UnixMilli(bar.Time).In(nyZone)
		minutes := t.Hour()*60 + t.Minute()
		accept := false
		switch session {
		case "regular":
			accept = minutes >= 570 && minutes < 960
		case "extended":
			accept = minutes >= 240 && minutes < 1200
		case "premarket":
			accept = minutes >= 240 && minutes < 570
		case "postmarket":
			accept = minutes >= 960 && minutes < 1200
		case "night":
			accept = isOvernightET(t)
		case "equity24h":
			accept = true
		}
		if accept {
			out = append(out, bar)
		}
	}
	return out
}

func (c *opendClient) staticSymbols(ctx context.Context, codes []string) ([]futuSymbol, error) {
	securities := make([]any, 0, len(codes))
	for _, code := range codes {
		securities = append(securities, map[string]any{"market": marketUS, "code": code})
	}
	s2c, err := c.call(ctx, protoQotGetStaticInfo, map[string]any{"c2s": map[string]any{"securityList": securities}})
	if err != nil {
		return nil, err
	}
	list, _ := s2c["staticInfoList"].([]any)
	out := make([]futuSymbol, 0, len(list))
	wanted := make(map[string]bool, len(codes))
	for _, code := range codes {
		wanted[code] = true
	}
	for _, item := range list {
		row, _ := item.(map[string]any)
		basic, _ := row["basic"].(map[string]any)
		security, _ := basic["security"].(map[string]any)
		market, _ := asInt(security["market"])
		code, _ := security["code"].(string)
		secType, _ := asInt(basic["secType"])
		if !wanted[code] || market != marketUS || (secType != 3 && secType != 4) {
			continue
		}
		if delisted, _ := basic["delisting"].(bool); delisted {
			continue
		}
		name, _ := basic["name"].(string)
		typeName := "stock"
		if secType == 4 {
			typeName = "fund"
		}
		exchangeID, _ := asInt(basic["exchType"])
		exchange := map[int]string{4: "NYSE", 5: "NASDAQ", 6: "OTC", 7: "AMEX"}[exchangeID]
		out = append(out, futuSymbol{Symbol: code, Description: name, Exchange: exchange, Type: typeName})
	}
	return out, nil
}

func (c *opendClient) requireEquities(ctx context.Context, codes []string) ([]futuSymbol, error) {
	symbols, err := c.staticSymbols(ctx, codes)
	if err != nil {
		return nil, err
	}
	found := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		found[symbol.Symbol] = true
	}
	for _, code := range codes {
		if !found[code] {
			return nil, fmt.Errorf("%s: %w", code, errFutuSymbol)
		}
	}
	return symbols, nil
}

// Extended-session objects have no timestamp in the upstream protocol. Never
// label their price with the regular session's updateTimestamp or local time.
func parseMainFutuQuote(row map[string]any, now time.Time, overnightEnabled bool) (futuOvernightQuote, bool) {
	security, _ := row["security"].(map[string]any)
	code, _ := security["code"].(string)
	if code == "" {
		return futuOvernightQuote{}, false
	}
	name, _ := row["name"].(string)
	t := now.In(nyZone)
	minutes := t.Hour()*60 + t.Minute()
	session, field := "regular", ""
	switch {
	case minutes >= 240 && minutes < 570:
		session, field = "premarket", "preMarket"
	case minutes >= 960 && minutes < 1200:
		session, field = "postmarket", "afterMarket"
	case overnightEnabled && isOvernightET(t):
		session, field = "night", "overnight"
	}
	if field != "" {
		if extended, _ := row[field].(map[string]any); extended != nil {
			price := optionalFutuNumber(extended["price"])
			if price != nil && *price > 0 {
				return futuOvernightQuote{Symbol: code, Description: name, Price: *price, Session: session,
					High: optionalFutuNumber(extended["highPrice"]), Low: optionalFutuNumber(extended["lowPrice"]), Volume: optionalFutuNumber(extended["volume"]), Ch: optionalFutuNumber(extended["changeVal"]), Chp: optionalFutuNumber(extended["changeRate"])}, true
			}
		}
	}
	price := optionalFutuNumber(row["curPrice"])
	if price == nil || *price <= 0 {
		return futuOvernightQuote{}, false
	}
	q := futuOvernightQuote{Symbol: code, Description: name, Price: *price, Session: "regular", High: optionalFutuNumber(row["highPrice"]), Low: optionalFutuNumber(row["lowPrice"]), Volume: optionalFutuNumber(row["volume"])}
	if last := optionalFutuNumber(row["lastClosePrice"]); last != nil && *last > 0 {
		change, pct := *price-*last, (*price-*last) / *last * 100
		q.Ch, q.Chp = &change, &pct
	}
	if ts := optionalFutuNumber(row["updateTimestamp"]); ts != nil && *ts > 0 {
		ms := int64(*ts * 1000)
		q.Time = &ms
	} else if text, _ := row["updateTime"].(string); text != "" {
		if ts, err := time.ParseInLocation("2006-01-02 15:04:05", text, nyZone); err == nil {
			ms := ts.UnixMilli()
			q.Time = &ms
		}
	}
	return q, true
}

func (c *opendClient) basicQuotes(ctx context.Context, codes []string) ([]map[string]any, error) {
	securities := make([]any, 0, len(codes))
	for _, code := range codes {
		securities = append(securities, map[string]any{"market": marketUS, "code": code})
	}
	s2c, err := c.call(ctx, protoQotGetBasicQot, map[string]any{"c2s": map[string]any{"securityList": securities}})
	if err != nil {
		return nil, err
	}
	list, _ := s2c["basicQotList"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if row, ok := item.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *Module) futuQuotes(ctx context.Context, symbols []string) ([]futuQuote, error) {
	if _, err := m.requireUser(ctx); err != nil {
		return nil, err
	}
	if len(symbols) == 0 || len(symbols) > 50 {
		return nil, errors.New("请提供 1 至 50 个标的")
	}
	codes := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		code, err := normalizeFutuSymbol(symbol)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	client, err := m.futu.ensure(ctx)
	if err != nil {
		return nil, err
	}
	metadata, err := client.requireEquities(ctx, codes)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(metadata))
	for _, item := range metadata {
		names[item.Symbol] = item.Description
	}
	for _, code := range codes {
		if err := m.futu.hold(ctx, client, code, subTypeBasic, true); err != nil {
			return nil, err
		}
		defer m.futu.release(context.Background(), client, code, subTypeBasic)
	}
	rows, err := client.basicQuotes(ctx, codes)
	if err != nil {
		return nil, err
	}
	rec, err := m.loadFutu(ctx)
	if err != nil {
		return nil, err
	}
	quotes := make(map[string]futuQuote, len(rows))
	for _, row := range rows {
		q, ok := parseMainFutuQuote(row, m.now(), rec.OvernightEnabled)
		if !ok {
			continue
		}
		if _, wanted := names[q.Symbol]; !wanted {
			continue
		}
		if q.Description == "" {
			q.Description = names[q.Symbol]
		}
		quotes[q.Symbol] = futuQuote{Symbol: q.Symbol, Description: q.Description, Price: q.Price, Change: q.Ch, ChangePct: q.Chp, Volume: q.Volume, High: q.High, Low: q.Low, Time: q.Time, Session: q.Session}
	}
	out := make([]futuQuote, 0, len(codes))
	for _, code := range codes {
		q, ok := quotes[code]
		if !ok {
			return nil, fmt.Errorf("%s 暂无可用行情，请检查富途行情权限和订阅额度", code)
		}
		out = append(out, q)
	}
	return out, nil
}

func (m *Module) getFutuQuotes(w http.ResponseWriter, r *http.Request) {
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, 401, "authentication_required", "请登录")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	quotes, err := m.futuQuotes(ctx, strings.Split(r.URL.Query().Get("symbols"), ","))
	if err != nil {
		writeFutuError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, map[string]any{"items": quotes})
}

func (m *Module) getFutuSymbols(w http.ResponseWriter, r *http.Request) {
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, 401, "authentication_required", "请登录")
		return
	}
	code, err := normalizeFutuSymbol(r.URL.Query().Get("query"))
	if err != nil {
		httpapi.Error(w, 400, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client, err := m.futu.ensure(ctx)
	if err != nil {
		writeFutuError(w, err)
		return
	}
	symbols, err := client.staticSymbols(ctx, []string{code})
	if err != nil {
		writeFutuError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, map[string]any{"items": symbols})
}

func writeFutuError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFutuDisabled), errors.Is(err, errOverlayOff):
		httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
	case errors.Is(err, errFutuSymbol):
		httpapi.Error(w, http.StatusBadRequest, "unsupported_symbol", err.Error())
	default:
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
	}
}

func futuMainCacheKey(session, resolution string) string {
	return "main:none:" + session + ":" + resolution
}

func (g *futuGateway) mainKline(ctx context.Context, symbol, resolution, session string, fromMs, toMs int64, requireHistory bool) ([]futuBar, error) {
	klType, subType, ok := futuKLTypes(resolution)
	if !ok {
		return nil, errors.New("不支持的 K 线周期")
	}
	if !intradayFutuResolution(resolution) && session != "regular" {
		return nil, errors.New("扩展时段仅支持分钟 K 线")
	}
	if requireHistory && (fromMs <= 0 || toMs <= fromMs) {
		return nil, errors.New("历史行情范围无效")
	}
	client, err := g.ensureConnection(ctx, session == "equity24h")
	if err != nil {
		return nil, err
	}
	if _, err := client.requireEquities(ctx, []string{symbol}); err != nil {
		return nil, err
	}
	if err := g.hold(ctx, client, symbol, subTypeBasic, true); err != nil {
		return nil, err
	}
	defer g.release(context.Background(), client, symbol, subTypeBasic)
	if err := g.hold(ctx, client, symbol, subType, true); err != nil {
		return nil, err
	}
	defer g.release(context.Background(), client, symbol, subType)
	bars, err := client.getKLAll(ctx, symbol, klType, 1000)
	if err != nil {
		return nil, err
	}
	key := futuMainCacheKey(session, resolution)
	if err := g.storeBars(ctx, symbol, key, filterFutuBars(bars, session, resolution)); err != nil {
		return nil, err
	}
	if fromMs > 0 {
		// A timestamp in cache does not prove history coverage. Fetch the exact
		// requested span instead of treating old night-only bars as full data.
		remain, used, err := client.historyQuota(ctx)
		if err != nil {
			return nil, err
		}
		if _, alreadyUsed := used[symbol]; remain <= 0 && !alreadyUsed {
			return nil, errors.New("历史 K 线额度不足")
		}
		endMs := toMs
		if endMs == 0 {
			endMs = time.Now().UnixMilli()
		}
		begin, end := time.UnixMilli(fromMs).In(nyZone).Format("2006-01-02"), time.UnixMilli(endMs-1).In(nyZone).Format("2006-01-02")
		sessionID := sessionETH
		if session == "regular" {
			sessionID = sessionRTH
		} else if session == "equity24h" {
			sessionID = sessionALL
		}
		history, err := client.requestHistoryKLSession(ctx, symbol, klType, begin, end, sessionID)
		if err != nil {
			return nil, err
		}
		if err := g.storeBars(ctx, symbol, key, filterFutuBars(history, session, resolution)); err != nil {
			return nil, err
		}
	}
	out, err := g.loadBars(ctx, symbol, key, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	return out, nil
}
