package investment

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

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

type futuGateway struct {
	module     *Module
	connectMu  sync.Mutex
	mu         sync.Mutex
	client     *opendClient
	instanceID string
	generation uint64
	closed     bool
	refs       map[subKey]*subLease
	subOps     map[subOperationKey]*subOperation
	clients    map[*futuWSClient]struct{}
}

func newFutuGateway(module *Module) *futuGateway {
	instanceID, err := randomHex(16)
	if err != nil {
		instanceID = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return &futuGateway{module: module, instanceID: instanceID, refs: make(map[subKey]*subLease), subOps: make(map[subOperationKey]*subOperation), clients: make(map[*futuWSClient]struct{})}
}

func (g *futuGateway) generationTokenLocked() string {
	return g.instanceID + ":" + strconv.FormatUint(g.generation, 10)
}

func (g *futuGateway) generationForClient(client *opendClient) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.client != client {
		return "", errors.New("富途连接代次已变更")
	}
	return g.generationTokenLocked(), nil
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
	return g.ensureConnection(ctx, false)
}

func (g *futuGateway) ensureOverlay(ctx context.Context) (*opendClient, error) {
	return g.ensureConnection(ctx, true)
}

func (g *futuGateway) ensureConnection(ctx context.Context, requireOvernight bool) (*opendClient, error) {
	g.connectMu.Lock()
	defer g.connectMu.Unlock()
	if !g.module.moduleEnabled {
		return nil, errors.New("投资模块未启用")
	}
	rec, err := g.module.loadFutu(ctx)
	if err != nil {
		return nil, err
	}
	if !rec.Enabled {
		if requireOvernight {
			return nil, errOverlayOff
		}
		return nil, errors.New("富途牛牛未启用")
	}
	if requireOvernight && !rec.OvernightEnabled {
		return nil, errOverlayOff
	}
	if g.module.opend != nil {
		if state, _ := g.module.opend.status(); state != "running" {
			return nil, errors.New("富途牛牛服务尚未启动")
		}
	}
	if err := validateOpenDAddr(rec.Host, rec.Port, rec.AllowNonLocal); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.client != nil {
		select {
		case <-g.client.stop:
			g.closeLocked()
		default:
		}
	}
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
		return nil, err
	}
	client.onKL = g.broadcastKL
	client.onQuote = g.broadcastQuote
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.generation != generation {
		client.close()
		return nil, errors.New("富途牛牛配置已变更，请重试")
	}
	g.client = client
	go func() {
		<-client.stop
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.client == client {
			g.closeLocked()
		}
	}()
	g.module.logger.Info("富途牛牛OpenD connected")
	return client, nil
}

func (g *futuGateway) addWS(ctx context.Context, client *futuWSClient) error {
	if _, err := g.ensureOverlay(ctx); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.client == nil || g.closed {
		return errors.New("OpenD 连接已断开")
	}
	g.clients[client] = struct{}{}
	ready, _ := json.Marshal(map[string]any{"stream": map[string]any{
		"status": "ready", "generation": g.generationTokenLocked(),
	}})
	client.send <- ready
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
	g.broadcastQuoteUpdate(q, false)
}

func (g *futuGateway) broadcastQuoteUpdate(q futuOvernightQuote, snapshot bool) {
	payload, _ := json.Marshal(map[string]any{"data": []any{map[string]any{"service": "FUTU_QUOTE", "symbol": q.Symbol, "quote": q, "snapshot": snapshot}}})
	g.broadcast(payload)
}

var errOverlayOff = errors.New("夜盘覆盖未启用")
