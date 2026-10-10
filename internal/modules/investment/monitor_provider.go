package investment

import (
	"context"
	"errors"
	"time"
)

type monitorQuoteBatch struct {
	session     marketSession
	quotes      map[string]monitoredQuote
	authVersion string
}

type monitorQuoteProvider interface {
	Fetch(context.Context, map[string]struct{}, time.Time) (monitorQuoteBatch, bool, error)
	WithCurrent(context.Context, string, func() error) error
}

type schwabMonitorProvider struct {
	accessToken func(context.Context) (string, error)
	session     func(context.Context, string, time.Time) (marketSession, bool, error)
	quotes      func(context.Context, string, map[string]struct{}) (map[string]monitoredQuote, error)
	guard       func(context.Context, string, func() error) error
}

func (p *schwabMonitorProvider) Fetch(ctx context.Context, symbols map[string]struct{}, now time.Time) (monitorQuoteBatch, bool, error) {
	token, err := p.accessToken(ctx)
	if err != nil {
		return monitorQuoteBatch{}, false, err
	}
	session, open, err := p.session(ctx, token, now)
	if err != nil || !open {
		return monitorQuoteBatch{}, open, err
	}
	quotes, err := p.quotes(ctx, token, symbols)
	return monitorQuoteBatch{session: session, quotes: quotes, authVersion: token}, true, err
}

func (p *schwabMonitorProvider) WithCurrent(ctx context.Context, version string, commit func() error) error {
	return p.guard(ctx, version, commit)
}

// Keep the authorization check and result writes in one guarded interval.
// Callers hold monitorMu; no remote requests run while tokenMu is held here.
func (m *Module) withCurrentSchwabToken(ctx context.Context, token string, commit func() error) error {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return err
	}
	if rec.AccessToken != token || rec.ReauthorizationRequired {
		return errors.New("Schwab 授权已变更，等待下一次检查")
	}
	return commit()
}
