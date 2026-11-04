package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/justinas/nosurf"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
)

type Mode string

const (
	ModeLocal    Mode = "local"
	ModePassword Mode = "password"
)

type Config struct {
	Mode              Mode
	ListenAddress     string
	PublicHTTPS       bool
	AllowInsecureHTTP bool
	InitialPassword   string
	AllowedHosts      []string
	CORSAllowOrigins  []string
	Logger            *slog.Logger
}

type Service struct {
	db             *sql.DB
	queries        *dbsqlc.Queries
	mode           Mode
	sessions       *scs.SessionManager
	sessionStore   *SessionStore
	allowedHosts   map[string]struct{}
	publicHTTPS    bool
	allowedOrigins map[string]struct{}
	cookieSameSite http.SameSite
	passwordLimit  *passwordLimit
	tokenMu        sync.Mutex
	userDisabled   func(string)
	now            func() time.Time
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
		if !loopback && !cfg.PublicHTTPS && !cfg.AllowInsecureHTTP {
			return nil, errors.New("password mode on a non-loopback address requires an HTTPS public URL behind a reverse proxy")
		}
	default:
		return nil, fmt.Errorf("unsupported auth mode %q", cfg.Mode)
	}

	origins, err := parseAllowedOrigins(cfg.CORSAllowOrigins, cfg.PublicHTTPS)
	if err != nil {
		return nil, err
	}
	store := NewSessionStore(db)
	sessions := scs.New()
	sessions.Store = store
	store.sessions = sessions
	sessions.Lifetime = 7 * 24 * time.Hour
	sessions.IdleTimeout = 24 * time.Hour
	sessions.HashTokenInStore = true
	sessions.Cookie.Name = "workbench_session"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteStrictMode
	if cfg.PublicHTTPS && len(origins) > 0 {
		sessions.Cookie.SameSite = http.SameSiteNoneMode
	}
	sessions.Cookie.Secure = cfg.PublicHTTPS
	sessions.Cookie.Persist = true
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default().With("component", "auth")
	}
	sessions.ErrorFunc = func(w http.ResponseWriter, _ *http.Request, _ error) {
		logger.Error("session persistence failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "session_failed", "message": "could not persist session"})
	}

	service := &Service{
		db:      db,
		queries: dbsqlc.New(db), mode: cfg.Mode, sessions: sessions, sessionStore: store,
		allowedHosts: make(map[string]struct{}), publicHTTPS: cfg.PublicHTTPS,
		allowedOrigins: origins, cookieSameSite: sessions.Cookie.SameSite,
		passwordLimit: newPasswordLimit(),
		now:           time.Now,
	}
	for _, allowed := range cfg.AllowedHosts {
		service.allowedHosts[strings.ToLower(allowed)] = struct{}{}
	}
	if len(service.allowedHosts) == 0 {
		service.allowedHosts[strings.ToLower(cfg.ListenAddress)] = struct{}{}
		if loopback || cfg.AllowInsecureHTTP {
			_, port, _ := net.SplitHostPort(cfg.ListenAddress)
			service.allowedHosts[net.JoinHostPort("localhost", port)] = struct{}{}
			service.allowedHosts[net.JoinHostPort("127.0.0.1", port)] = struct{}{}
			service.allowedHosts[net.JoinHostPort("::1", port)] = struct{}{}
		}
	}

	if err := service.ensureCredential(ctx, cfg.InitialPassword, logger); err != nil {
		return nil, err
	}
	if cfg.Mode == ModeLocal {
		if _, err := service.localUser(ctx); err != nil {
			return nil, errors.New("local mode requires a single active owner administrator; use password mode for multiple users")
		}
	}
	if cfg.Mode == ModePassword {
		if err := service.ensureSigningKey(ctx); err != nil {
			return nil, fmt.Errorf("initialize signing key: %w", err)
		}
	}
	return service, nil
}

func (s *Service) ensureCredential(ctx context.Context, initialPassword string, logger *slog.Logger) error {
	owner, err := s.queries.GetAuthUser(ctx, contracts.LegacyUserID)
	if err == nil && (owner.PasswordHash != "" || s.mode == ModeLocal) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect auth users: %w", err)
	}
	exists := err == nil
	if s.mode == ModeLocal {
		now := s.now().UnixMilli()
		return s.queries.InsertAuthUser(ctx, dbsqlc.InsertAuthUserParams{ID: contracts.LegacyUserID, Username: "admin", DisplayName: "管理员", Role: "admin", Status: "active", CredentialVersion: now, CreatedAt: now, UpdatedAt: now})
	}
	generated := initialPassword == ""
	if generated {
		initialPassword, err = generateInitialPassword()
		if err != nil {
			return err
		}
	}
	hash, err := hashPassword(initialPassword)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if exists {
		err = s.queries.InitializeOwnerPassword(ctx, dbsqlc.InitializeOwnerPasswordParams{PasswordHash: hash, CredentialVersion: now, UpdatedAt: now})
	} else {
		err = s.queries.InsertAuthUser(ctx, dbsqlc.InsertAuthUserParams{ID: contracts.LegacyUserID, Username: "admin", DisplayName: "管理员", PasswordHash: hash, Role: "admin", Status: "active", CredentialVersion: now, MustChangePassword: 1, CreatedAt: now, UpdatedAt: now})
	}
	if err != nil {
		return fmt.Errorf("store initial password: %w", err)
	}
	if generated {
		// Bootstrap credentials remain visible even with an error-only threshold.
		record := slog.NewRecord(time.Now(), slog.LevelWarn, "initial login password generated; change it after signing in", 0)
		record.Add("initialPassword", initialPassword)
		_ = logger.Handler().Handle(ctx, record)
	}
	return nil
}

func (s *Service) Authenticate(ctx context.Context, password string) (bool, error) {
	if s.mode == ModeLocal {
		return true, nil
	}
	if _, err := s.passwordLimit.acquire(false); err != nil {
		return false, err
	}
	defer s.passwordLimit.release()
	user, err := s.queries.GetAuthUser(ctx, contracts.LegacyUserID)
	if err != nil {
		return false, fmt.Errorf("load credential: %w", err)
	}
	if user.Status != "active" {
		return false, nil
	}
	return verifyPassword(password, user.PasswordHash)
}

func (s *Service) LoadAndSave(next http.Handler) http.Handler {
	save := s.sessions.LoadAndSave(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if DeviceSessionID(r) != "" || nativeTokenEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}
		save.ServeHTTP(w, r)
	})
}

// Load exposes the current session to the handler without committing idle-timeout
// refreshes or writing Set-Cookie. Hashed static assets use this so session
// cookies cannot defeat immutable browser caching.
func (s *Service) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if DeviceSessionID(r) != "" {
			next.ServeHTTP(w, r)
			return
		}
		var token string
		cookie, err := r.Cookie(s.sessions.Cookie.Name)
		if err == nil {
			token = cookie.Value
		}
		ctx, err := s.sessions.Load(r.Context(), token)
		if err != nil {
			s.sessions.ErrorFunc(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.requestUser(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "authentication_required", "message": "login required"})
			return
		}
		expectedUser := r.Header.Get("X-Workbench-User")
		socketUser := r.URL.Query().Get("workbenchUser")
		if (expectedUser != "" && expectedUser != user.ID) || (socketUser != "" && socketUser != user.ID) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusConflict, map[string]string{"code": "account_changed", "message": "登录用户已变更，请重新加载"})
			return
		}
		changingPassword := r.Method == http.MethodPut && r.URL.Path == "/api/settings/password"
		revokingDevice := r.Method == http.MethodPost && (r.URL.Path == "/api/auth/token/revoke" || r.URL.Path == "/api/auth/logout" || r.URL.Path == "/api/auth/logout-all")
		if s.mode == ModePassword && user.MustChangePassword != 0 && !changingPassword && !revokingDevice {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "password_change_required", "message": "首次登录请修改密码"})
			return
		}
		s.serveAuthenticated(w, s.principalRequest(r, user), next, user.CredentialVersion)
	})
}

func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mode == ModeLocal {
		user, err := s.localUser(r.Context())
		if err != nil {
			invalidToken(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "mustChangePassword": false, "user": viewUser(user)})
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": "invalid login request"})
		return
	}
	if retryAfter, err := s.acquireLogin(r, request.Username); err != nil {
		writePasswordLimit(w, retryAfter, err)
		return
	}
	defer s.passwordLimit.release()
	// Read the hash and version together so a concurrent password change cannot
	// turn a login with the old password into a session for the new credential.
	credential, ok, err := s.verifyLogin(r.Context(), request.Username, request.Password)
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
	s.sessions.Put(r.Context(), "credentialVersion", credential.CredentialVersion)
	s.sessions.Put(r.Context(), "userID", credential.ID)
	s.sessions.Remove(r.Context(), "deviceSessionID")
	if err := s.trackBrowser(r); err != nil {
		_ = s.sessions.Destroy(r.Context())
		tokenFailed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "mustChangePassword": credential.MustChangePassword != 0, "user": viewUser(credential)})
}

func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if DeviceSessionID(r) != "" {
		s.RevokeTokenHandler(w, r)
		return
	}
	if cookie, err := r.Cookie(s.sessions.Cookie.Name); err == nil {
		if err := s.queries.RevokeBrowserSession(r.Context(), dbsqlc.RevokeBrowserSessionParams{TokenHash: tokenHash(cookie.Value), RevokedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}}); err != nil {
			tokenFailed(w)
			return
		}
	}
	if err := s.sessions.Destroy(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "session_failed", "message": "could not destroy session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": s.mode == ModeLocal, "mustChangePassword": false})
}

func (s *Service) StatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, err := s.requestUser(r)
	response := map[string]any{"authenticated": err == nil, "mode": s.mode, "mustChangePassword": err == nil && s.mode == ModePassword && user.MustChangePassword != 0}
	if err == nil {
		response["user"] = viewUser(user)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) CSRFTokenHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"token": nosurf.Token(r)})
}

func (s *Service) Security(next http.Handler) http.Handler {
	csrf := nosurf.New(next)
	// HTTPS terminates at the reverse proxy. Use the configured public origin,
	// never client-controlled Forwarded/X-Forwarded-* headers or the backend TLS state.
	csrf.SetIsTLSFunc(func(_ *http.Request) bool { return s.publicHTTPS })
	csrf.SetIsAllowedOriginFunc(func(origin *url.URL) bool {
		_, allowed := s.allowedOrigins[strings.ToLower(origin.Scheme+"://"+origin.Host)]
		return allowed
	})
	csrf.SetBaseCookie(http.Cookie{
		Name: "workbench_csrf", Path: "/", HttpOnly: true,
		SameSite: s.cookieSameSite, Secure: s.publicHTTPS,
	})
	csrf.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "csrf_failed", "message": "CSRF validation failed"})
	}))
	return s.hostCheck(s.cors(s.originCheck(s.tokenSecurity(next, csrf))))
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
			if !s.originAllowed(origin, r.Host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_origin", "message": "origin is not allowed"})
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
