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
	"sort"
	"strings"
	"time"
)

type marketSession struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type marketDay struct {
	Date         string `json:"date"`
	IsOpen       bool   `json:"isOpen"`
	SessionHours struct {
		Regular []marketSession `json:"regularMarket"`
	} `json:"sessionHours"`
}

type marketHoursCache struct {
	date     string
	loadedAt time.Time
	day      marketDay
}

type monitoredQuote struct {
	AssetMainType string `json:"assetMainType"`
	Symbol        string `json:"symbol"`
	Realtime      bool   `json:"realtime"`
	Quote         struct {
		ClosePrice float64 `json:"closePrice"`
	} `json:"quote"`
	Regular struct {
		Price float64 `json:"regularMarketLastPrice"`
		Time  int64   `json:"regularMarketTradeTime"`
	} `json:"regular"`
}

func (m *Module) readMarketJSON(ctx context.Context, token, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.schwabAPI+"/marketdata/v1/"+path, nil)
	if err != nil {
		return errors.New("无法创建行情请求")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return errors.New("暂时无法连接 Schwab 行情服务")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		if err := m.refreshRejectedAccessToken(ctx, token); err != nil {
			return err
		}
		return errors.New("行情授权已更新，等待下一次检查")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Schwab 行情查询失败（HTTP %d）", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return errors.New("Schwab 行情响应格式无效")
	}
	return nil
}

// Use the provider's calendar, including holidays and shortened trading days.
// A missing or malformed schedule fails closed instead of guessing an open day.
func (m *Module) regularSession(ctx context.Context, token string, now time.Time) (marketSession, bool, error) {
	day := now.In(m.marketLocation).Format("2006-01-02")
	cache := &m.marketHours
	if cache.date != day || now.Sub(cache.loadedAt) > time.Hour {
		var response struct {
			Equity map[string]marketDay `json:"equity"`
		}
		if err := m.readMarketJSON(ctx, token, "markets/equity?date="+day, &response); err != nil {
			return marketSession{}, false, err
		}
		calendar, ok := response.Equity["EQ"]
		if !ok || calendar.Date != day || (calendar.IsOpen && len(calendar.SessionHours.Regular) == 0) {
			return marketSession{}, false, errors.New("无法确认美股常规交易时段")
		}
		for _, session := range calendar.SessionHours.Regular {
			if session.Start.IsZero() || !session.End.After(session.Start) || session.Start.In(m.marketLocation).Format("2006-01-02") != day {
				return marketSession{}, false, errors.New("美股交易时段无效")
			}
		}
		*cache = marketHoursCache{date: day, loadedAt: now, day: calendar}
	}
	if cache.day.IsOpen {
		for _, session := range cache.day.SessionHours.Regular {
			if !now.Before(session.Start) && now.Before(session.End) {
				return session, true, nil
			}
		}
	}
	return marketSession{}, false, nil
}

func (m *Module) fetchMonitorQuotes(ctx context.Context, token string, symbols map[string]struct{}) (map[string]monitoredQuote, error) {
	keys := make([]string, 0, len(symbols))
	for symbol := range symbols {
		keys = append(keys, symbol)
	}
	sort.Strings(keys)
	result := make(map[string]monitoredQuote, len(keys))
	for start := 0; start < len(keys); start += 50 {
		query := url.Values{"symbols": {strings.Join(keys[start:min(start+50, len(keys))], ",")}, "fields": {"quote,regular"}, "indicative": {"false"}}
		var batch map[string]monitoredQuote
		if err := m.readMarketJSON(ctx, token, "quotes?"+query.Encode(), &batch); err != nil {
			return nil, err
		}
		for symbol, quote := range batch {
			result[symbol] = quote
		}
	}
	return result, nil
}

func (q monitoredQuote) validate(symbol string, now time.Time, session marketSession) error {
	if q.Symbol != symbol || q.AssetMainType != "EQUITY" {
		return errors.New("未返回该标的的美股/ETF 行情，请检查代码和行情权限")
	}
	if !q.Realtime {
		return errors.New("行情为延迟数据，暂不触发预警")
	}
	for _, value := range []float64{q.Regular.Price, q.Quote.ClosePrice} {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("最新价或前收盘价无效，暂不触发预警")
		}
	}
	quotedAt := time.UnixMilli(q.Regular.Time)
	if quotedAt.Before(session.Start) || !quotedAt.Before(session.End) || quotedAt.After(now.Add(5*time.Second)) || now.Sub(quotedAt) > 3*time.Minute {
		return errors.New("常规时段成交行情超过三分钟未更新或时间无效，暂不触发预警")
	}
	if math.IsInf(q.changePercent(), 0) || math.IsNaN(q.changePercent()) {
		return errors.New("涨跌幅无效，暂不触发预警")
	}
	return nil
}

func (q monitoredQuote) changePercent() float64 {
	return (q.Regular.Price/q.Quote.ClosePrice - 1) * 100
}
