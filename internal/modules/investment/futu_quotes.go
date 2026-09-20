package investment

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	_ "time/tzdata"
)

var nyZone = mustNY()

func mustNY() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}

func isOvernightET(t time.Time) bool {
	h, m, _ := t.In(nyZone).Clock()
	minutes := h*60 + m
	return minutes >= 20*60 || minutes < 4*60
}

func futuKLTypes(resolution string) (klType, subType int, ok bool) {
	switch resolution {
	case "1":
		return klType1Min, subTypeKL1Min, true
	case "5":
		return klType5Min, subTypeKL5Min, true
	case "15":
		return klType15Min, subTypeKL15Min, true
	case "30":
		return klType30Min, subTypeKL30Min, true
	default:
		return 0, 0, false
	}
}

func resolutionForKLType(klType int) string {
	switch klType {
	case klType1Min:
		return "1"
	case klType5Min:
		return "5"
	case klType15Min:
		return "15"
	case klType30Min:
		return "30"
	default:
		return ""
	}
}

type futuBar struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type futuOvernightQuote struct {
	Symbol string  `json:"symbol"`
	Price  float64 `json:"lp"`
	High   float64 `json:"high,omitempty"`
	Low    float64 `json:"low,omitempty"`
	Volume float64 `json:"volume,omitempty"`
	Ch     float64 `json:"ch,omitempty"`
	Chp    float64 `json:"chp,omitempty"`
}

func (c *opendClient) dispatchKL(body []byte) {
	s2c, err := parseFutuS2C(body)
	if err != nil {
		return
	}
	security, _ := s2c["security"].(map[string]any)
	code, _ := security["code"].(string)
	klType, _ := asInt(s2c["klType"])
	resolution := resolutionForKLType(klType)
	if code == "" || resolution == "" {
		return
	}
	for _, bar := range parseKLList(s2c["klList"]) {
		if c.onKL != nil {
			c.onKL(code, resolution, bar)
		}
	}
}

func (c *opendClient) dispatchQuote(body []byte) {
	s2c, err := parseFutuS2C(body)
	if err != nil {
		return
	}
	list, _ := s2c["basicQotList"].([]any)
	for _, item := range list {
		row, _ := item.(map[string]any)
		q, ok := parseOvernightQuote(row)
		if ok && c.onQuote != nil {
			c.onQuote(q)
		}
	}
}

func parseOvernightQuote(row map[string]any) (futuOvernightQuote, bool) {
	security, _ := row["security"].(map[string]any)
	code, _ := security["code"].(string)
	if code == "" {
		return futuOvernightQuote{}, false
	}
	overnight, _ := row["overnight"].(map[string]any)
	price, ok := asFloat(overnight["price"])
	if !ok {
		return futuOvernightQuote{}, false
	}
	q := futuOvernightQuote{Symbol: code, Price: price}
	q.High, _ = asFloat(overnight["highPrice"])
	q.Low, _ = asFloat(overnight["lowPrice"])
	q.Volume, _ = asFloat(overnight["volume"])
	q.Ch, _ = asFloat(overnight["changeVal"])
	q.Chp, _ = asFloat(overnight["changeRate"])
	return q, true
}

func parseKLList(raw any) []futuBar {
	list, _ := raw.([]any)
	out := make([]futuBar, 0, len(list))
	for _, item := range list {
		row, _ := item.(map[string]any)
		if row == nil {
			continue
		}
		if blank, _ := row["isBlank"].(bool); blank {
			continue
		}
		bar, ok := parseKLBar(row)
		if !ok || !isOvernightET(time.UnixMilli(bar.Time).UTC()) {
			continue
		}
		out = append(out, bar)
	}
	return out
}

func parseKLBar(row map[string]any) (futuBar, bool) {
	ts, ok := asFloat(row["timestamp"])
	if !ok || ts <= 0 {
		return futuBar{}, false
	}
	open, ok1 := asFloat(row["openPrice"])
	high, ok2 := asFloat(row["highPrice"])
	low, ok3 := asFloat(row["lowPrice"])
	closePx, ok4 := asFloat(row["closePrice"])
	vol, _ := asFloat(row["volume"])
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return futuBar{}, false
	}
	ms := int64(ts * 1000)
	if ts > 1e12 {
		ms = int64(ts)
	}
	return futuBar{Time: ms, Open: open, High: high, Low: low, Close: closePx, Volume: vol}, true
}

func asInt(v any) (int, bool) {
	f, ok := asFloat(v)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func (c *opendClient) subscribe(ctx context.Context, code string, subTypes []int, sub bool) error {
	list := make([]any, 0, len(subTypes))
	for _, t := range subTypes {
		list = append(list, t)
	}
	_, err := c.call(ctx, protoQotSub, map[string]any{"c2s": map[string]any{
		"securityList": []any{map[string]any{"market": marketUS, "code": code}},
		"subTypeList":  list, "isSubOrUnSub": sub, "isRegOrUnRegPush": true, "isFirstPush": true, "session": sessionALL,
	}})
	return err
}

func (c *opendClient) getKL(ctx context.Context, code string, klType, reqNum int) ([]futuBar, error) {
	s2c, err := c.call(ctx, protoQotGetKL, map[string]any{"c2s": map[string]any{
		"security": map[string]any{"market": marketUS, "code": code},
		"klType":   klType, "reqNum": reqNum, "rehabType": rehabNone,
	}})
	if err != nil {
		return nil, err
	}
	return parseKLList(s2c["klList"]), nil
}

func (c *opendClient) historyQuota(ctx context.Context) (remain int, usedCodes map[string]struct{}, err error) {
	s2c, err := c.call(ctx, protoQotRequestHistoryKLQuota, map[string]any{"c2s": map[string]any{"bGetDetail": true}})
	if err != nil {
		return 0, nil, err
	}
	remain, _ = asInt(s2c["remainQuota"])
	usedCodes = map[string]struct{}{}
	if details, ok := s2c["detailList"].([]any); ok {
		for _, item := range details {
			row, _ := item.(map[string]any)
			sec, _ := row["security"].(map[string]any)
			if code, _ := sec["code"].(string); code != "" {
				usedCodes[code] = struct{}{}
			}
		}
	}
	return remain, usedCodes, nil
}

func (c *opendClient) requestHistoryKL(ctx context.Context, code string, klType int, begin, end string) ([]futuBar, error) {
	var (
		all []futuBar
		key any
	)
	for {
		c2s := map[string]any{
			"rehabType": rehabNone, "klType": klType,
			"security":  map[string]any{"market": marketUS, "code": code},
			"beginTime": begin, "endTime": end, "maxAckKLNum": 1000, "session": sessionALL,
		}
		if key != nil {
			c2s["nextReqKey"] = key
		}
		s2c, err := c.call(ctx, protoQotRequestHistoryKL, map[string]any{"c2s": c2s})
		if err != nil {
			return nil, err
		}
		all = append(all, parseKLList(s2c["klList"])...)
		next, ok := s2c["nextReqKey"]
		if !ok || next == nil || next == "" {
			break
		}
		key = next
	}
	return all, nil
}

func (c *opendClient) globalState(ctx context.Context) (qotLogined bool, err error) {
	s2c, err := c.call(ctx, protoGetGlobalState, map[string]any{"c2s": map[string]any{"userID": 0}})
	if err != nil {
		return false, err
	}
	v, _ := s2c["qotLogined"].(bool)
	return v, nil
}

func (c *opendClient) subInfo(ctx context.Context) (used, remain int, err error) {
	s2c, err := c.call(ctx, protoQotGetSubInfo, map[string]any{"c2s": map[string]any{"isReqAllConn": false}})
	if err != nil {
		return 0, 0, err
	}
	remain, _ = asInt(s2c["remainQuota"])
	if total, ok := asInt(s2c["totalQuota"]); ok {
		used = total - remain
	}
	return used, remain, nil
}
