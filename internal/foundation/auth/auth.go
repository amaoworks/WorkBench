package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/justinas/nosurf"

	"workbench/internal/foundation/database/sqlc"
)

type Mode string

const (
	ModeLocal    Mode = "local"
	ModePassword Mode = "password"
)

type Config struct {
	Mode            Mode
	ListenAddress   string
	PublicHTTPS     bool
	InitialPassword string
	AllowedHosts    []string
}

type Service struct {
	db           *sql.DB
	queries      *dbsqlc.Queries
	mode         Mode
	sessions     *scs.SessionManager
	sessionStore *SessionStore
	allowedHosts map[string]struct{}
	publicHTTPS  bool
}

func New(ctx context.Context, db *sql.DB, cfg Config) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen address must include host and port: %w", err)
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	switch cfg.Mode {
	case ModeLocal:
		if !loopback {
			return nil, errors.New("local mode requires a loopback listen address")
		}
	case ModePassword:
		if !loopback && !cfg.PublicHTTPS {
			return nil, errors.New("password mode on a non-loopback address requires HTTPS")
		}
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", cfg.Mode)
	}

	store := NewSessionStore(db)
	sessions := scs.New()
	sessions.Store = store
	sessions.Lifetime = 7 * 24 * time.Hour
	sessions.IdleTimeout = 24 * time.Hour
	sessions.HashTokenInStore = true
	sessions.Cookie.Name = "workbench_session"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteStrictMode
	sessions.Cookie.Secure = cfg.PublicHTTPS
	sessions.Cookie.Persist = true

	service := &Service{
		db:      db,
		queries: dbsqlc.New(db), mode: cfg.Mode, sessions: sessions, sessionStore: store,
		allowedHosts: make(map[string]struct{}), publicHTTPS: cfg.PublicHTTPS,
	}
	for _, allowed := range cfg.AllowedHosts {
		service.allowedHosts[strings.ToLower(allowed)] = struct{}{}
	}
	if len(service.allowedHosts) == 0 {
		service.allowedHosts[strings.ToLower(cfg.ListenAddress)] = struct{}{}
		if loopback {
			_, port, _ := net.SplitHostPort(cfg.ListenAddress)
			service.allowedHosts[net.JoinHostPort("localhost", port)] = struct{}{}
			service.allowedHosts[net.JoinHostPort("127.0.0.1", port)] = struct{}{}
			service.allowedHosts[net.JoinHostPort("::1", port)] = struct{}{}
		}
	}

	if cfg.Mode == ModePassword {
		if err := service.ensureCredential(ctx, cfg.InitialPassword); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (s *Service) ensureCredential(ctx context.Context, initialPassword string) error {
	count, err := s.queries.CountAuthCredentials(ctx)
	if err != nil {
		return fmt.Errorf("inspect auth credentials: %w", err)
	}
	if count > 0 {
		return nil
	}
	if initialPassword == "" {
		return errors.New("password mode requires WORKBENCH_PASSWORD on first start")
	}
	hash, err := hashPassword(initialPassword)
	if err != nil {
		return err
	}
	err = s.queries.InsertInitialPassword(ctx, dbsqlc.InsertInitialPasswordParams{
		PasswordHash: hash, UpdatedAt: time.Now().UTC().UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("store initial password: %w", err)
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, password string) (bool, error) {
	if s.mode == ModeLocal {
		return true, nil
	}
	hash, err := s.queries.GetPasswordHash(ctx)
	if err != nil {
		return false, fmt.Errorf("load credential: %w", err)
	}
	return verifyPassword(password, hash)
}

func (s *Service) LoadAndSave(next http.Handler) http.Handler {
	return s.sessions.LoadAndSave(next)
}

func (s *Service) Require(next http.Handler) http.Handler {
	if s.mode == ModeLocal {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var version int64
		err := s.db.QueryRowContext(r.Context(), "SELECT updated_at FROM auth_credentials WHERE id = 1").Scan(&version)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "authentication_failed", "message": "authentication failed"})
			return
		}
		if !s.sessions.GetBool(r.Context(), "authenticated") || s.sessions.GetInt64(r.Context(), "credentialVersion") != version {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "authentication_required", "message": "login required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if s.mode == ModeLocal {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": "invalid login request"})
		return
	}
	// Read the hash and version together so a concurrent password change cannot
	// turn a login with the old password into a session for the new credential.
	var hash string
	var version int64
	err := s.db.QueryRowContext(r.Context(), "SELECT password_hash, updated_at FROM auth_credentials WHERE id = 1").Scan(&hash, &version)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "authentication_failed", "message": "authentication failed"})
		return
	}
	ok, err := verifyPassword(request.Password, hash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "authentication_failed", "message": "authentication failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "invalid_credentials", "message": "invalid credentials"})
		return
	}
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "session_failed", "message": "could not create session"})
		return
	}
	s.sessions.Put(r.Context(), "authenticated", true)
	s.sessions.Put(r.Context(), "credentialVersion", version)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Destroy(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "session_failed", "message": "could not destroy session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.mode == ModeLocal})
}

func (s *Service) StatusHandler(w http.ResponseWriter, r *http.Request) {
	authenticated := s.mode == ModeLocal || s.sessions.GetBool(r.Context(), "authenticated")
	if s.mode == ModePassword && authenticated {
		var version int64
		err := s.db.QueryRowContext(r.Context(), "SELECT updated_at FROM auth_credentials WHERE id = 1").Scan(&version)
		authenticated = err == nil && s.sessions.GetInt64(r.Context(), "credentialVersion") == version
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": authenticated, "mode": s.mode})
}

func (s *Service) CSRFTokenHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"token": nosurf.Token(r)})
}

func (s *Service) Security(next http.Handler) http.Handler {
	csrf := nosurf.New(next)
	csrf.SetBaseCookie(http.Cookie{
		Name: "workbench_csrf", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: s.publicHTTPS,
	})
	csrf.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "csrf_failed", "message": "CSRF validation failed"})
	}))
	return s.hostCheck(s.originCheck(csrf))
}

func (s *Service) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.allowedHosts[strings.ToLower(r.Host)]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_host", "message": "invalid host"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_origin", "message": "origin is not allowed"})
				return
			}
			if s.publicHTTPS && u.Scheme != "https" {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_origin", "message": "secure origin required"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
