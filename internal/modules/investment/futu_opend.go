package investment

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const (
	opendHeaderSize = 44
	opendFmtJSON    = 1

	protoInitConnect              uint32 = 1001
	protoGetGlobalState           uint32 = 1002
	protoNotify                   uint32 = 1003
	protoKeepAlive                uint32 = 1004
	protoQotSub                   uint32 = 3001
	protoQotGetSubInfo            uint32 = 3003
	protoQotGetBasicQot           uint32 = 3004
	protoQotUpdateBasicQot        uint32 = 3005
	protoQotGetKL                 uint32 = 3006
	protoQotUpdateKL              uint32 = 3007
	protoQotRequestHistoryKL      uint32 = 3103
	protoQotRequestHistoryKLQuota uint32 = 3104

	retSucceed     = 0
	packetEncNone  = -1
	sessionALL     = 3
	marketUS       = 11
	rehabNone      = 0
	subTypeBasic   = 1
	klType1Min     = 1
	klType5Min     = 6
	klType15Min    = 7
	klType30Min    = 8
	subTypeKL1Min  = 11
	subTypeKL5Min  = 7
	subTypeKL15Min = 8
	subTypeKL30Min = 9
	opendClientVer = 300
	maxOpendBody   = 4 << 20
)

var (
	errFutuTradeForbidden = errors.New("富途牛牛交易协议已禁用")
	errFutuProtoForbidden = errors.New("不允许的 OpenD 协议")
	errOverlayOff         = errors.New("夜盘覆盖未启用")
	errOpendClosed        = errors.New("OpenD 连接已关闭")
	nyZone                = mustNY()
)

func mustNY() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}

var allowedSend = map[uint32]struct{}{
	protoInitConnect: {}, protoGetGlobalState: {}, protoKeepAlive: {},
	protoQotSub: {}, protoQotGetSubInfo: {}, protoQotGetBasicQot: {}, protoQotGetKL: {},
	protoQotRequestHistoryKL: {}, protoQotRequestHistoryKLQuota: {},
}

func canSendProto(id uint32) error {
	if id >= 2000 && id < 3000 {
		return errFutuTradeForbidden
	}
	if _, ok := allowedSend[id]; !ok {
		return errFutuProtoForbidden
	}
	return nil
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

func validateOpenDAddr(host string, port int, allowNonLocal bool) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("OpenD 主机不能为空")
	}
	if port < 1 || port > 65535 {
		return errors.New("OpenD 端口无效")
	}
	if strings.ContainsAny(host, "[]/") {
		return errors.New("OpenD 主机不能包含括号或路径")
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		if !allowNonLocal {
			return errors.New("非本机 OpenD 地址需要勾选允许")
		}
		return nil
	}
	if strings.Contains(host, ":") {
		return errors.New("OpenD 主机不能包含端口")
	}
	if !allowNonLocal {
		return errors.New("非本机 OpenD 地址需要勾选允许")
	}
	if len(host) > 253 {
		return errors.New("OpenD 主机无效")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 {
			return errors.New("OpenD 主机无效")
		}
		for i, c := range label {
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '-' && i > 0 && i < len(label)-1)
			if !ok {
				return errors.New("OpenD 主机无效")
			}
		}
	}
	return nil
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

type opendReply struct {
	proto uint32
	body  []byte
}

type opendClient struct {
	conn    net.Conn
	mu      sync.Mutex
	serial  uint32
	pending map[uint32]chan opendReply
	onKL    func(symbol, resolution string, bar futuBar)
	onQuote func(futuOvernightQuote)
	closed  bool
	stop    chan struct{}
}

func dialOpend(ctx context.Context, addr, clientID string) (*opendClient, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	client := &opendClient{conn: conn, pending: make(map[uint32]chan opendReply), stop: make(chan struct{})}
	go client.readLoop()
	s2c, err := client.call(ctx, protoInitConnect, map[string]any{"c2s": map[string]any{
		"clientVer": opendClientVer, "clientID": clientID, "recvNotify": true,
		"packetEncAlgo": packetEncNone, "pushProtoFmt": opendFmtJSON, "programmingLanguage": "Go",
	}})
	if err != nil {
		client.close()
		return nil, err
	}
	interval := 10
	if v, ok := asInt(s2c["keepAliveInterval"]); ok && v > 0 {
		interval = v
	}
	go client.keepAlive(interval)
	return client, nil
}

func (c *opendClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.stop)
	_ = c.conn.Close()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *opendClient) keepAlive(intervalSec int) {
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = c.call(ctx, protoKeepAlive, map[string]any{"c2s": map[string]any{"time": time.Now().Unix()}})
			cancel()
		}
	}
}

func (c *opendClient) call(ctx context.Context, proto uint32, req any) (map[string]any, error) {
	if err := canSendProto(proto); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan opendReply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errOpendClosed
	}
	c.serial++
	serial := c.serial
	if err := writeOpendPacket(c.conn, proto, serial, payload); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.pending[serial] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, serial)
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.stop:
		return nil, errOpendClosed
	case reply, ok := <-ch:
		if !ok {
			return nil, errOpendClosed
		}
		return parseFutuS2C(reply.body)
	}
}

func writeOpendPacket(conn net.Conn, proto, serial uint32, payload []byte) error {
	if len(payload) > maxOpendBody {
		return errors.New("OpenD 请求过大")
	}
	var hdr [opendHeaderSize]byte
	hdr[0], hdr[1] = 'F', 'T'
	binary.LittleEndian.PutUint32(hdr[2:], proto)
	hdr[6] = opendFmtJSON
	hdr[7] = 0
	binary.LittleEndian.PutUint32(hdr[8:], serial)
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(payload)))
	sum := sha1.Sum(payload)
	copy(hdr[16:36], sum[:])
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func readOpendPacket(conn net.Conn) (proto, serial uint32, body []byte, err error) {
	var hdr [opendHeaderSize]byte
	if _, err = io.ReadFull(conn, hdr[:]); err != nil {
		return 0, 0, nil, err
	}
	if hdr[0] != 'F' || hdr[1] != 'T' {
		return 0, 0, nil, errors.New("OpenD 协议头无效")
	}
	proto = binary.LittleEndian.Uint32(hdr[2:])
	serial = binary.LittleEndian.Uint32(hdr[8:])
	n := binary.LittleEndian.Uint32(hdr[12:])
	if n > maxOpendBody {
		return 0, 0, nil, errors.New("OpenD 包体过大")
	}
	body = make([]byte, n)
	_, err = io.ReadFull(conn, body)
	return proto, serial, body, err
}

func (c *opendClient) readLoop() {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		proto, serial, body, err := readOpendPacket(c.conn)
		if err != nil {
			c.close()
			return
		}
		switch proto {
		case protoQotUpdateKL:
			c.dispatchKL(body)
		case protoQotUpdateBasicQot:
			c.dispatchQuote(body)
		case protoNotify:
			// Quote-only: ignore trade-unlock and other notifications.
		default:
			c.mu.Lock()
			ch := c.pending[serial]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- opendReply{proto: proto, body: body}:
				default:
				}
			}
		}
	}
}

func parseFutuS2C(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var envelope map[string]any
	if err := dec.Decode(&envelope); err != nil {
		return nil, err
	}
	ret, _ := asInt(envelope["retType"])
	if ret != retSucceed {
		msg, _ := envelope["retMsg"].(string)
		if msg == "" {
			msg = "OpenD 请求失败"
		}
		return nil, fmt.Errorf("%s（%d）", msg, ret)
	}
	s2c, _ := envelope["s2c"].(map[string]any)
	if s2c == nil {
		s2c = map[string]any{}
	}
	return s2c, nil
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
