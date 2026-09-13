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

// ModuleLifecycle is optional. Modules without process resources may omit it.
type ModuleLifecycle interface {
	OnEnabledChanged(ctx context.Context, enabled bool) error
	Close()
}
