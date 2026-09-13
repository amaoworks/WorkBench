package investment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const (
	maxFutuSettingsBytes = 16 << 10
	futuKlineTimeout     = 25 * time.Second
	futuLease            = time.Minute
)

type futuRecord struct {
	Host          string
	Port          int
	Enabled       bool
	AllowNonLocal bool
	LastError     string
	UpdatedAt     time.Time
}

type futuSettingsView struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Enabled       bool   `json:"enabled"`
	AllowNonLocal bool   `json:"allowNonLocal"`
	Connected     bool   `json:"connected"`
	QotLogined    bool   `json:"qotLogined"`
	LastError     string `json:"lastError"`
	SubUsed       int    `json:"subUsed,omitempty"`
	SubRemain     int    `json:"subRemain,omitempty"`
	HistoryRemain int    `json:"historyRemain,omitempty"`
}

type futuSettingsInput struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Enabled       bool   `json:"enabled"`
	AllowNonLocal bool   `json:"allowNonLocal"`
}

func (m *Module) loadFutu(ctx context.Context) (futuRecord, error) {
	row, err := m.queries.GetFutu(ctx)
	if err != nil {
		return futuRecord{}, err
	}
	return futuRecord{
		Host: row.Host, Port: int(row.Port), Enabled: row.Enabled != 0,
		AllowNonLocal: row.AllowNonLocal != 0, LastError: row.LastError, UpdatedAt: unixMilli(row.UpdatedAt),
	}, nil
}

func (m *Module) storeFutu(ctx context.Context, rec futuRecord) error {
	enabled, allow := int64(0), int64(0)
	if rec.Enabled {
		enabled = 1
	}
	if rec.AllowNonLocal {
		allow = 1
	}
	return m.queries.UpsertFutu(ctx, investmentsqlc.UpsertFutuParams{
		Host: rec.Host, Port: int64(rec.Port), Enabled: enabled, AllowNonLocal: allow,
		LastError: rec.LastError, UpdatedAt: m.now().UTC().UnixMilli(),
	})
}

func (m *Module) getFutu(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途配置")
		return
	}
	view := futuSettingsView{
		Host: rec.Host, Port: rec.Port, Enabled: rec.Enabled, AllowNonLocal: rec.AllowNonLocal, LastError: rec.LastError,
	}
	if rec.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if client, err := m.futu.ensure(ctx); err == nil {
			view.Connected = true
			view.QotLogined, _ = client.globalState(ctx)
			view.SubUsed, view.SubRemain, _ = client.subInfo(ctx)
			view.HistoryRemain, _, _ = client.historyQuota(ctx)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, view)
}

func (m *Module) saveFutu(w http.ResponseWriter, r *http.Request) {
	var input futuSettingsInput
	if httpapi.Decode(w, r, &input, maxFutuSettingsBytes) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的富途配置")
		return
	}
	if err := validateOpenDAddr(input.Host, input.Port, input.AllowNonLocal); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rec := futuRecord{Host: strings.TrimSpace(input.Host), Port: input.Port, Enabled: input.Enabled, AllowNonLocal: input.AllowNonLocal}
	if err := m.storeFutu(r.Context(), rec); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法保存富途配置")
		return
	}
	m.futu.reset()
	if rec.Enabled {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		if _, err := m.futu.ensure(ctx); err != nil {
			_ = m.storeFutu(r.Context(), futuRecord{Host: rec.Host, Port: rec.Port, Enabled: rec.Enabled, AllowNonLocal: rec.AllowNonLocal, LastError: err.Error()})
		}
	}
	m.getFutu(w, r)
}

func (m *Module) disconnectFutu(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途配置")
		return
	}
	rec.Enabled = false
	rec.LastError = ""
	if err := m.storeFutu(r.Context(), rec); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法保存富途配置")
		return
	}
	m.futu.reset()
	httpapi.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *Module) getFutuKline(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	resolution := r.URL.Query().Get("resolution")
	if symbol == "" {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "缺少标的")
		return
	}
	if _, _, ok := futuKLTypes(resolution); !ok {
		httpapi.Write(w, http.StatusOK, map[string]any{"candles": []futuBar{}})
		return
	}
	from, to, err := parseKlineRange(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), futuKlineTimeout)
	defer cancel()
	bars, err := m.futu.kline(ctx, symbol, resolution, from, to)
	if err != nil {
		if errors.Is(err, errOverlayOff) {
			httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
			return
		}
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, map[string]any{"candles": bars})
}

func parseKlineRange(fromS, toS string) (fromMs, toMs int64, err error) {
	if fromS == "" && toS == "" {
		return 0, 0, nil
	}
	if fromS != "" {
		from, convErr := strconv.ParseInt(fromS, 10, 64)
		if convErr != nil {
			return 0, 0, errors.New("from 无效")
		}
		fromMs = from * 1000
	}
	if toS != "" {
		to, convErr := strconv.ParseInt(toS, 10, 64)
		if convErr != nil {
			return 0, 0, errors.New("to 无效")
		}
		toMs = to * 1000
	}
	return fromMs, toMs, nil
}

func (m *Module) serveFutuStream(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil || !rec.Enabled {
		httpapi.Error(w, http.StatusConflict, "futu_disabled", "夜盘覆盖未启用")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &futuWSClient{conn: conn, send: make(chan []byte, 64)}
	if err := m.futu.addWS(r.Context(), client); err != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "OpenD unavailable"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	go client.writePump()
	defer m.futu.removeWS(client)
	conn.SetReadLimit(4096)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *Module) futuCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Command    string `json:"command"`
		Symbol     string `json:"symbol"`
		Resolution string `json:"resolution"`
	}
	if httpapi.Decode(w, r, &input, 16<<10) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的订阅命令")
		return
	}
	symbol := strings.TrimSpace(input.Symbol)
	command := strings.ToUpper(input.Command)
	if symbol == "" || (command != "ADD" && command != "UNSUB") {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "请使用 ADD 或 UNSUB 并提供标的")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var err error
	if command == "ADD" {
		err = m.futu.addSub(ctx, symbol, input.Resolution)
	} else {
		err = m.futu.releaseSub(ctx, symbol, input.Resolution)
	}
	if err != nil {
		if errors.Is(err, errOverlayOff) {
			httpapi.Error(w, http.StatusConflict, "futu_disabled", err.Error())
			return
		}
		httpapi.Error(w, http.StatusServiceUnavailable, "futu_unavailable", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"success": true})
}

type futuWSClient struct {
	conn *websocket.Conn
	send chan []byte
}

func (c *futuWSClient) writePump() {
	defer c.conn.Close()
	for message := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}

type subKey struct {
	symbol string
	sub    int
}

type subLease struct {
	count int
	since time.Time
	idle  *time.Timer
}

type futuGateway struct {
	module     *Module
	connectMu  sync.Mutex
	mu         sync.Mutex
	client     *opendClient
	generation uint64
	closed     bool
	refs       map[subKey]*subLease
	clients    map[*futuWSClient]struct{}
}

func newFutuGateway(module *Module) *futuGateway {
	return &futuGateway{module: module, refs: make(map[subKey]*subLease), clients: make(map[*futuWSClient]struct{})}
}

func (g *futuGateway) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeLocked()
}

func (g *futuGateway) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	g.closeLocked()
}

func (g *futuGateway) closeLocked() {
	g.generation++
	if g.client != nil {
		g.client.close()
		g.client = nil
	}
	for _, lease := range g.refs {
		if lease.idle != nil {
			lease.idle.Stop()
		}
	}
	clear(g.refs)
	for client := range g.clients {
		g.removeLocked(client)
	}
}

func (g *futuGateway) removeLocked(client *futuWSClient) {
	if _, ok := g.clients[client]; ok {
		delete(g.clients, client)
		close(client.send)
	}
	_ = client.conn.Close()
}

func (g *futuGateway) ensure(ctx context.Context) (*opendClient, error) {
	g.connectMu.Lock()
	defer g.connectMu.Unlock()
	rec, err := g.module.loadFutu(ctx)
	if err != nil {
		return nil, err
	}
	if !rec.Enabled {
		return nil, errOverlayOff
	}
	if err := validateOpenDAddr(rec.Host, rec.Port, rec.AllowNonLocal); err != nil {
		return nil, err
	}
	g.mu.Lock()
	existing, closed := g.client, g.closed
	generation := g.generation
	g.mu.Unlock()
	if closed {
		return nil, errors.New("投资已关闭")
	}
	if existing != nil {
		return existing, nil
	}
	id, err := randomHex(8)
	if err != nil {
		return nil, err
	}
	client, err := dialOpend(ctx, net.JoinHostPort(rec.Host, strconv.Itoa(rec.Port)), "workbench-"+id)
	if err != nil {
		_ = g.module.storeFutu(ctx, futuRecord{Host: rec.Host, Port: rec.Port, Enabled: rec.Enabled, AllowNonLocal: rec.AllowNonLocal, LastError: err.Error()})
		return nil, err
	}
	client.onKL = g.broadcastKL
	client.onQuote = g.broadcastQuote
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.generation != generation {
		client.close()
		return nil, errors.New("富途配置已变更，请重试")
	}
	g.client = client
	g.module.logger.Info("Futu OpenD connected")
	_ = g.module.storeFutu(ctx, futuRecord{Host: rec.Host, Port: rec.Port, Enabled: rec.Enabled, AllowNonLocal: rec.AllowNonLocal, LastError: ""})
	return client, nil
}

func (g *futuGateway) addWS(ctx context.Context, client *futuWSClient) error {
	if _, err := g.ensure(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client == nil || g.closed {
		return errors.New("OpenD 连接已断开")
	}
	g.clients[client] = struct{}{}
	client.send <- []byte(`{"stream":{"status":"ready"}}`)
	return nil
}

func (g *futuGateway) removeWS(client *futuWSClient) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.clients[client]; !ok {
		_ = client.conn.Close()
		return
	}
	g.removeLocked(client)
}

func (g *futuGateway) broadcast(payload []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for client := range g.clients {
		select {
		case client.send <- payload:
		default:
		}
	}
}

func (g *futuGateway) broadcastKL(symbol, resolution string, bar futuBar) {
	payload, _ := json.Marshal(map[string]any{"data": []any{map[string]any{
		"service": "FUTU_KL", "symbol": symbol, "resolution": resolution, "bar": bar,
	}}})
	g.broadcast(payload)
	_ = g.module.queries.UpsertFutuBar(context.Background(), investmentsqlc.UpsertFutuBarParams{
		Symbol: symbol, Resolution: resolution, TimeMs: bar.Time, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
	})
}

func (g *futuGateway) broadcastQuote(q futuOvernightQuote) {
	payload, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"service": "FUTU_QUOTE", "symbol": q.Symbol, "quote": q}}})
	g.broadcast(payload)
}

func (g *futuGateway) addSub(ctx context.Context, symbol, resolution string) error {
	klType, subType, ok := futuKLTypes(resolution)
	if !ok {
		return nil
	}
	client, err := g.ensure(ctx)
	if err != nil {
		return err
	}
	_ = klType
	if err := g.hold(ctx, client, symbol, subTypeBasic, true); err != nil {
		return err
	}
	return g.hold(ctx, client, symbol, subType, true)
}

func (g *futuGateway) releaseSub(ctx context.Context, symbol, resolution string) error {
	_, subType, ok := futuKLTypes(resolution)
	if !ok {
		return nil
	}
	client, err := g.ensure(ctx)
	if err != nil {
		return err
	}
	g.release(ctx, client, symbol, subType)
	g.release(ctx, client, symbol, subTypeBasic)
	return nil
}

func (g *futuGateway) hold(ctx context.Context, client *opendClient, symbol string, sub int, subscribe bool) error {
	key := subKey{symbol: symbol, sub: sub}
	g.mu.Lock()
	lease := g.refs[key]
	if lease == nil {
		lease = &subLease{since: time.Now()}
		g.refs[key] = lease
	}
	if lease.idle != nil {
		lease.idle.Stop()
		lease.idle = nil
	}
	first := lease.count == 0
	lease.count++
	g.mu.Unlock()
	if first && subscribe {
		return client.subscribe(ctx, symbol, []int{sub}, true)
	}
	return nil
}

func (g *futuGateway) release(_ context.Context, client *opendClient, symbol string, sub int) {
	key := subKey{symbol: symbol, sub: sub}
	g.mu.Lock()
	lease := g.refs[key]
	if lease == nil {
		g.mu.Unlock()
		return
	}
	if lease.count > 0 {
		lease.count--
	}
	if lease.count > 0 {
		g.mu.Unlock()
		return
	}
	wait := futuLease - time.Since(lease.since)
	if wait < futuLease {
		wait = futuLease
	}
	if wait < 0 {
		wait = 0
	}
	lease.idle = time.AfterFunc(wait, func() {
		g.mu.Lock()
		cur := g.refs[key]
		if cur == nil || cur.count > 0 {
			g.mu.Unlock()
			return
		}
		delete(g.refs, key)
		g.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = client.subscribe(ctx, symbol, []int{sub}, false)
	})
	g.mu.Unlock()
}

func (g *futuGateway) kline(ctx context.Context, symbol, resolution string, fromMs, toMs int64) ([]futuBar, error) {
	klType, subType, ok := futuKLTypes(resolution)
	if !ok {
		return []futuBar{}, nil
	}
	client, err := g.ensure(ctx)
	if err != nil {
		return nil, err
	}
	if err := g.hold(ctx, client, symbol, subTypeBasic, true); err != nil {
		return nil, err
	}
	if err := g.hold(ctx, client, symbol, subType, true); err != nil {
		return nil, err
	}
	g.release(ctx, client, symbol, subType)
	g.release(ctx, client, symbol, subTypeBasic)

	bars, err := client.getKL(ctx, symbol, klType, 1000)
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
		bars, err = client.getKL(ctx, symbol, klType, 1000)
		if err != nil {
			return nil, err
		}
	}
	if err := g.storeBars(ctx, symbol, resolution, bars); err != nil {
		return nil, err
	}
	if err := g.maybeHistory(ctx, client, symbol, resolution, klType, fromMs); err != nil {
		g.module.logger.Warn("Futu history fallback skipped", "errorType", fmt.Sprintf("%T", err))
	}
	return g.loadBars(ctx, symbol, resolution, fromMs, toMs)
}

func (g *futuGateway) maybeHistory(ctx context.Context, client *opendClient, symbol, resolution string, klType int, fromMs int64) error {
	if fromMs <= 0 {
		return nil
	}
	oldest, err := g.module.queries.OldestFutuBarTime(ctx, investmentsqlc.OldestFutuBarTimeParams{Symbol: symbol, Resolution: resolution})
	if err == nil && oldest <= fromMs {
		return nil
	}
	remain, used, err := client.historyQuota(ctx)
	if err != nil {
		return err
	}
	if remain <= 0 {
		if _, ok := used[symbol]; !ok {
			return errors.New("历史 K 线额度不足")
		}
	}
	begin := time.UnixMilli(fromMs).In(nyZone).Format("2006-01-02")
	end := time.Now().In(nyZone).Format("2006-01-02")
	hist, err := client.requestHistoryKL(ctx, symbol, klType, begin, end)
	if err != nil {
		return err
	}
	return g.storeBars(ctx, symbol, resolution, hist)
}

func (g *futuGateway) storeBars(ctx context.Context, symbol, resolution string, bars []futuBar) error {
	for _, bar := range bars {
		if err := g.module.queries.UpsertFutuBar(ctx, investmentsqlc.UpsertFutuBarParams{
			Symbol: symbol, Resolution: resolution, TimeMs: bar.Time, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (g *futuGateway) loadBars(ctx context.Context, symbol, resolution string, fromMs, toMs int64) ([]futuBar, error) {
	if toMs == 0 {
		toMs = time.Now().UnixMilli() + int64(time.Hour/time.Millisecond)
	}
	rows, err := g.module.queries.ListFutuBars(ctx, investmentsqlc.ListFutuBarsParams{
		Symbol: symbol, Resolution: resolution, TimeMs: fromMs, TimeMs_2: toMs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]futuBar, 0, len(rows))
	for _, row := range rows {
		out = append(out, futuBar{Time: row.TimeMs, Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, Volume: row.Volume})
	}
	return out, nil
}
