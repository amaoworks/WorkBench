package news

import (
	"context"
	"sync"
	"workbench/internal/contracts"
)

// workLocks serialize work for one entity without making unrelated operations
// wait for network requests. Waiting honors cancellation, including module stop.
type workLocks struct {
	mu      sync.Mutex
	entries map[string]*workLock
}

type workLock struct {
	token chan struct{}
	refs  int
}

func (w *workLocks) acquire(ctx context.Context, key string) (func(), error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return func() {}, err
	}

	key = contracts.UserID(ctx) + ":" + key
	w.mu.Lock()
	if w.entries == nil {
		w.entries = make(map[string]*workLock)
	}
	entry := w.entries[key]
	if entry == nil {
		entry = &workLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		w.entries[key] = entry
	}
	entry.refs++
	w.mu.Unlock()
	drop := func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(w.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-entry.token:
		release := func() { entry.token <- struct{}{}; drop() }
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}
}
