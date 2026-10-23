package investment

import (
	"context"
	"errors"
	"sync"
	"time"
)

const futuLease = time.Minute

type subKey struct {
	symbol string
	sub    int
}

type browserSubKey struct {
	symbol     string
	resolution string
}

// browserCommand binds browser references to an online WebSocket. The owner
// mutex also serializes disconnect cleanup with commands already in flight.
func (g *futuGateway) browserCommand(ctx context.Context, command, owner, generation, symbol, resolution string) (string, bool, error) {
	g.mu.Lock()
	var client *futuWSClient
	for candidate := range g.clients {
		if candidate.owner == owner && candidate.generation == generation && generation == g.generationTokenLocked() {
			client = candidate
			break
		}
	}
	current := g.generationTokenLocked()
	g.mu.Unlock()
	if client == nil {
		return current, false, nil
	}

	client.opMu.Lock()
	defer client.opMu.Unlock()
	if !g.browserOnline(client) {
		return g.currentGeneration(), false, nil
	}
	if _, _, ok := futuKLTypes(resolution); !ok && resolution != "" {
		return g.currentGeneration(), true, nil
	}
	key := browserSubKey{symbol: symbol, resolution: resolution}
	_, held := client.subs[key]
	if (command == "ADD" && held) || (command == "UNSUB" && !held) {
		return g.currentGeneration(), true, nil
	}
	var applied bool
	var err error
	if command == "ADD" {
		current, applied, err = g.addSub(ctx, symbol, resolution, generation)
		if applied && err == nil {
			client.subs[key] = struct{}{}
		}
	} else {
		current, applied, err = g.releaseSub(ctx, symbol, resolution, generation)
		if applied && err == nil {
			delete(client.subs, key)
		}
	}
	if !g.browserOnline(client) {
		return g.currentGeneration(), false, nil
	}
	return current, applied, err
}

func (g *futuGateway) browserOnline(client *futuWSClient) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, online := g.clients[client]
	return online && !g.closed && client.opend == g.client && client.generation == g.generationTokenLocked()
}

func (g *futuGateway) releaseBrowserSub(client *futuWSClient, key browserSubKey) {
	_, subType, ok := futuKLTypes(key.resolution)
	if !ok && key.resolution != "" {
		return
	}
	if key.resolution != "" {
		_ = g.release(context.Background(), client.opend, key.symbol, subType)
	}
	_ = g.release(context.Background(), client.opend, key.symbol, subTypeBasic)
}

type subLease struct {
	count      int
	since      time.Time
	generation uint64
	idle       *time.Timer
}

type subOperationKey struct {
	lease      subKey
	generation uint64
}

type subOperation struct {
	mu    sync.Mutex
	users int
}

func (g *futuGateway) lockSubOperation(key subKey, generation uint64) func() {
	opKey := subOperationKey{lease: key, generation: generation}
	g.mu.Lock()
	if g.subOps == nil {
		g.subOps = make(map[subOperationKey]*subOperation)
	}
	op := g.subOps[opKey]
	if op == nil {
		op = &subOperation{}
		g.subOps[opKey] = op
	}
	op.users++
	g.mu.Unlock()

	op.mu.Lock()
	return func() {
		op.mu.Unlock()
		g.mu.Lock()
		op.users--
		if op.users == 0 && g.subOps[opKey] == op {
			delete(g.subOps, opKey)
		}
		g.mu.Unlock()
	}
}

func (g *futuGateway) currentGeneration() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.generationTokenLocked()
}

// commandClient gates the underlying lease operations by connection generation.
// Browser commands additionally require an online owner via browserCommand.
// Internal bootstrap ADDs may omit a token; unversioned UNSUBs are no-ops.
func (g *futuGateway) commandClient(ctx context.Context, requestGeneration string, allowBootstrap bool) (*opendClient, string, bool, error) {
	g.mu.Lock()
	current := g.generationTokenLocked()
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return nil, current, false, errors.New("投资已关闭")
	}
	if requestGeneration == "" && !allowBootstrap {
		return nil, current, false, nil
	}
	if requestGeneration != "" && requestGeneration != current {
		return nil, current, false, nil
	}

	client, err := g.ensureOverlay(ctx)
	if err != nil {
		return nil, g.currentGeneration(), false, err
	}
	current, err = g.generationForClient(client)
	if err != nil {
		return nil, g.currentGeneration(), false, nil
	}
	if requestGeneration != "" && requestGeneration != current {
		return nil, current, false, nil
	}
	return client, current, true, nil
}

func (g *futuGateway) commandGenerationAfter(client *opendClient) (string, bool) {
	generation, err := g.generationForClient(client)
	if err != nil {
		return g.currentGeneration(), false
	}
	return generation, true
}

func (g *futuGateway) addSub(ctx context.Context, symbol, resolution, requestGeneration string) (string, bool, error) {
	_, subType, ok := futuKLTypes(resolution)
	if !ok && resolution != "" {
		return g.currentGeneration(), true, nil
	}
	client, current, applied, err := g.commandClient(ctx, requestGeneration, true)
	if err != nil || !applied {
		return current, applied, err
	}
	if err := g.hold(ctx, client, symbol, subTypeBasic, true); err != nil {
		if generation, stillCurrent := g.commandGenerationAfter(client); !stillCurrent {
			return generation, false, nil
		}
		return "", false, err
	}
	if resolution == "" {
		// A quote listener needs BASIC only, without consuming a K-line lease.
		// Read the initial snapshot even if another tab already holds BASIC.
		quote, available, err := client.getOvernightQuote(ctx, symbol)
		if err != nil {
			_ = g.release(ctx, client, symbol, subTypeBasic)
			if generation, stillCurrent := g.commandGenerationAfter(client); !stillCurrent {
				return generation, false, nil
			}
			return "", false, err
		}
		generation, stillCurrent := g.commandGenerationAfter(client)
		if stillCurrent && available {
			g.broadcastQuoteUpdate(quote, true)
		}
		return generation, stillCurrent, nil
	}
	if err := g.hold(ctx, client, symbol, subType, true); err != nil {
		_ = g.release(ctx, client, symbol, subTypeBasic)
		if generation, stillCurrent := g.commandGenerationAfter(client); !stillCurrent {
			return generation, false, nil
		}
		return "", false, err
	}
	generation, stillCurrent := g.commandGenerationAfter(client)
	return generation, stillCurrent, nil
}

func (g *futuGateway) releaseSub(ctx context.Context, symbol, resolution, requestGeneration string) (string, bool, error) {
	_, subType, ok := futuKLTypes(resolution)
	if !ok && resolution != "" {
		return g.currentGeneration(), true, nil
	}
	client, current, applied, err := g.commandClient(ctx, requestGeneration, false)
	if err != nil || !applied {
		return current, applied, err
	}
	if resolution != "" {
		if err := g.release(ctx, client, symbol, subType); err != nil {
			if generation, stillCurrent := g.commandGenerationAfter(client); !stillCurrent {
				return generation, false, nil
			}
			return "", false, err
		}
	}
	if err := g.release(ctx, client, symbol, subTypeBasic); err != nil {
		if generation, stillCurrent := g.commandGenerationAfter(client); !stillCurrent {
			return generation, false, nil
		}
		return "", false, err
	}
	generation, stillCurrent := g.commandGenerationAfter(client)
	return generation, stillCurrent, nil
}

func (g *futuGateway) hold(ctx context.Context, client *opendClient, symbol string, sub int, subscribe bool) error {
	key := subKey{symbol: symbol, sub: sub}
	g.mu.Lock()
	if g.closed || g.client != client {
		g.mu.Unlock()
		return errors.New("富途连接代次已变更")
	}
	generation := g.generation
	g.mu.Unlock()
	unlockOperation := g.lockSubOperation(key, generation)
	defer unlockOperation()

	g.mu.Lock()
	if g.closed || g.client != client || g.generation != generation {
		g.mu.Unlock()
		return errors.New("富途连接代次已变更")
	}
	lease := g.refs[key]
	if lease == nil || lease.generation != generation {
		if lease != nil && lease.idle != nil {
			lease.idle.Stop()
		}
		lease = &subLease{since: time.Now(), generation: generation}
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
		if err := client.subscribe(ctx, symbol, []int{sub}, true); err != nil {
			g.mu.Lock()
			active := !g.closed && g.client == client && g.generation == generation && g.refs[key] == lease
			if active && lease.count > 0 {
				lease.count--
				if lease.count == 0 {
					delete(g.refs, key)
				}
			}
			g.mu.Unlock()
			if !active {
				return errors.New("富途连接代次已变更")
			}
			return err
		}
	}
	g.mu.Lock()
	active := !g.closed && g.client == client && g.generation == generation && g.refs[key] == lease
	g.mu.Unlock()
	if !active {
		return errors.New("富途连接代次已变更")
	}
	return nil
}

func (g *futuGateway) release(_ context.Context, client *opendClient, symbol string, sub int) error {
	key := subKey{symbol: symbol, sub: sub}
	g.mu.Lock()
	if g.closed || g.client != client {
		g.mu.Unlock()
		return errors.New("富途连接代次已变更")
	}
	generation := g.generation
	g.mu.Unlock()
	unlockOperation := g.lockSubOperation(key, generation)
	defer unlockOperation()

	g.mu.Lock()
	if g.closed || g.client != client || g.generation != generation {
		g.mu.Unlock()
		return errors.New("富途连接代次已变更")
	}
	lease := g.refs[key]
	if lease == nil {
		g.mu.Unlock()
		return nil
	}
	if lease.generation != generation {
		g.mu.Unlock()
		return errors.New("富途连接代次已变更")
	}
	if lease.count > 0 {
		lease.count--
	}
	if lease.count > 0 {
		g.mu.Unlock()
		return nil
	}
	wait := futuLease - time.Since(lease.since)
	if wait < futuLease {
		wait = futuLease
	}
	if wait < 0 {
		wait = 0
	}
	lease.idle = time.AfterFunc(wait, func() {
		g.expireSub(client, generation, key, lease, symbol, sub)
	})
	g.mu.Unlock()
	return nil
}

func (g *futuGateway) expireSub(client *opendClient, generation uint64, key subKey, lease *subLease, symbol string, sub int) {
	unlockOperation := g.lockSubOperation(key, generation)
	defer unlockOperation()

	g.mu.Lock()
	if g.closed || g.client != client || g.generation != generation || g.refs[key] != lease || lease.generation != generation || lease.count > 0 {
		g.mu.Unlock()
		return
	}
	delete(g.refs, key)
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = client.subscribe(ctx, symbol, []int{sub}, false)
}
