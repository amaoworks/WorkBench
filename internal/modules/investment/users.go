package investment

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

// personalHTTP selects a connection before entering business code. Locks,
// account caches and Schwab streams belong to this connection, never the root.
func (m *Module) personalHTTP(handler func(*Module, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := m.requireUser(r.Context())
		if err != nil {
			httpapi.Error(w, 401, "authentication_required", "请登录")
			return
		}
		child := m.userModule(id)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// Upgraded sockets are not closed by http.Server or session middleware.
		if r.Header.Get("Upgrade") != "" && m.deps.Users != nil {
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						active, err := m.deps.Users.IsUserActive(ctx, id)
						if err != nil || !active {
							m.RevokeUser(id)
							cancel()
							return
						}
					}
				}
			}()
		}
		handler(child, w, r.WithContext(ctx))
	}
}

func (m *Module) requireUser(ctx context.Context) (string, error) {
	id, err := contracts.RequireUserID(ctx)
	if err != nil {
		return "", err
	}
	if m.deps.Users != nil {
		active, err := m.deps.Users.IsUserActive(ctx, id)
		if err != nil {
			return "", err
		}
		if !active {
			return "", contracts.ErrUnauthenticated
		}
	}
	return id, nil
}

func (m *Module) userModule(id string) *Module {
	root := m
	if m.root != nil {
		root = m.root
	}
	root.usersMu.Lock()
	defer root.usersMu.Unlock()
	if child := root.users[id]; child != nil {
		return child
	}
	root.monitorMu.Lock()
	enabled := root.monitorEnabled
	root.monitorMu.Unlock()
	child := &Module{deps: root.deps, root: root, userID: id, terminalAssets: root.terminalAssets, queries: root.queries, now: root.now, httpClient: root.httpClient,
		schwabAPI: root.schwabAPI, tvOrigin: root.tvOrigin, tvProxy: root.tvProxy, oauthSessions: make(map[string]mobileOAuthSession), futu: root.futu, opend: root.opend,
		logger: root.logger, monitorEnabled: enabled, marketLocation: root.marketLocation}
	child.streamer = newStreamer(streamerDeps{credentials: child.streamerCredentials, origin: func() string { return child.schwabAPI }, logger: child.logger})
	child.monitorQuotes = &schwabMonitorProvider{accessToken: child.ensureAccessToken, session: child.regularSession, quotes: child.fetchMonitorQuotes, guard: child.withCurrentSchwabToken}
	root.users[id] = child
	return child
}

// RevokeUser is called when authentication revokes a user's access.
func (m *Module) RevokeUser(id string) {
	root := m
	if m.root != nil {
		root = m.root
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := root.queries.InvalidateSchwabOAuth(ctx, id); err != nil {
		root.logger.Error("could not invalidate pending Schwab authorization", "userId", id)
	}
	root.usersMu.Lock()
	child := root.users[id]
	delete(root.users, id)
	root.usersMu.Unlock()
	if child != nil {
		child.monitorMu.Lock()
		child.monitorEnabled = false
		if child.netWorthCancel != nil {
			child.netWorthCancel()
		}
		child.monitorMu.Unlock()
		child.streamer.close()
	}
	root.futu.mu.Lock()
	for client := range root.futu.clients {
		if client.userID == id {
			root.futu.removeLocked(client)
			_ = client.conn.Close()
		}
	}
	root.futu.mu.Unlock()
}

func (m *Module) forEachUser(ctx context.Context, fn func(*Module, context.Context) error) error {
	if m.deps.Users == nil {
		return errors.New("investment user directory is required for background work")
	}
	ids, err := m.deps.Users.ActiveUserIDs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	var mu sync.Mutex
	var workers sync.WaitGroup
	slots := make(chan struct{}, 4)
dispatch:
	for _, id := range ids {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		workers.Go(func() {
			defer func() { <-slots }()
			ownerCtx := contracts.WithPrincipal(ctx, contracts.Principal{UserID: id, Role: contracts.RoleUser})
			if _, err := m.requireUser(ownerCtx); err != nil {
				return
			}
			if err := fn(m.userModule(id), ownerCtx); err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	return errors.Join(failures...)
}

func (m *Module) personalTool(handler func(*Module, context.Context, contracts.ToolCall) (contracts.ToolResult, error)) contracts.ToolHandler {
	return func(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
		id, err := m.requireUser(ctx)
		if err != nil {
			return contracts.ToolResult{}, err
		}
		return handler(m.userModule(id), ctx, call)
	}
}

func (m *Module) ResetPersonalStreams() {
	root := m
	if m.root != nil {
		root = m.root
	}
	root.streamer.reset()
	root.usersMu.Lock()
	defer root.usersMu.Unlock()
	for _, child := range root.users {
		child.streamer.reset()
	}
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !contracts.IsAdmin(r.Context()) {
		httpapi.Error(w, 403, "admin_required", "需要管理员权限")
		return false
	}
	return true
}
