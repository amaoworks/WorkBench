package contracts

import (
	"context"
	"net/http"
)

// RouteClass distinguishes public callbacks from login-protected extra routes.
// Ordinary business APIs still go through ModuleRegistrar.Handle and module gating.
type RouteClass string

const (
	RoutePublicCallback RouteClass = "public_callback"
	RouteProtectedAsset RouteClass = "protected_asset"
)

type ProvidedRoute struct {
	Method  string
	Pattern string
	Handler http.Handler
	Class   RouteClass
}

// ModuleRouteProvider declares extra HTTP routes that are not module API prefixes.
// Public callbacks must be explicit; they are never implied by ordinary business registration.
type ModuleRouteProvider interface {
	Routes() []ProvidedRoute
}

// SessionPolicy controls session handling for a module-owned request. ReadOnly
// loads the session without refreshing its idle expiry. WrapResponse may adjust
// response headers without bypassing authentication or module gating.
type SessionPolicy struct {
	ReadOnly     bool
	WrapResponse func(http.ResponseWriter) http.ResponseWriter
}

type ModuleSessionPolicyProvider interface {
	SessionPolicy(*http.Request) SessionPolicy
}

// ModuleStartup runs after HTTP serving has started. Implementations must return
// promptly and bind any asynchronous work to ctx, which ends when Run stops.
type ModuleStartup interface {
	OnStarted(ctx context.Context)
}

// ModuleLifecycle is optional. Modules without process resources may omit it.
// The registry restores persisted intent after migrations, serializes changes per
// module, and closes resources on initialization failure and shutdown. Callbacks
// must honor ctx and support retrying the same intent after a partial failure.
// A failed callback leaves the actual state unknown and all business gates closed.
// Close must release resources even when initialization or a callback failed.
type ModuleLifecycle interface {
	OnEnabledChanged(ctx context.Context, enabled bool) error
	Close()
}
