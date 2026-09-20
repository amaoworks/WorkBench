package investment

import (
	"context"
	"time"
)

const futuLease = time.Minute

type subKey struct {
	symbol string
	sub    int
}

type subLease struct {
	count int
	since time.Time
	idle  *time.Timer
}

func (g *futuGateway) addSub(ctx context.Context, symbol, resolution string) error {
	klType, subType, ok := futuKLTypes(resolution)
	if !ok {
		return nil
	}
	client, err := g.ensureOverlay(ctx)
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
	client, err := g.ensureOverlay(ctx)
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
