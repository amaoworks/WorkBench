package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func parseOrigin(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return nil, fmt.Errorf("CORS origin must be an HTTP or HTTPS origin without a path")
	}
	return u, nil
}

func parseAllowedOrigins(values []string, publicHTTPS bool) (map[string]struct{}, error) {
	origins := make(map[string]struct{}, len(values))
	for _, value := range values {
		u, err := parseOrigin(value)
		if err != nil {
			return nil, err
		}
		if publicHTTPS && u.Scheme != "https" {
			return nil, fmt.Errorf("HTTPS deployments require HTTPS CORS origins")
		}
		origins[strings.ToLower(u.Scheme+"://"+u.Host)] = struct{}{}
	}
	return origins, nil
}

func ValidateCORSOrigins(values []string, publicHTTPS bool) error {
	_, err := parseAllowedOrigins(values, publicHTTPS)
	return err
}

func (s *Service) originAllowed(value, host string) bool {
	u, err := parseOrigin(value)
	if err != nil {
		return false
	}
	scheme := "http"
	if s.publicHTTPS {
		scheme = "https"
	}
	if u.Scheme == scheme && strings.EqualFold(u.Host, host) {
		return true
	}
	_, allowed := s.allowedOrigins[strings.ToLower(u.Scheme+"://"+u.Host)]
	return allowed
}

const corsMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"
const corsHeaders = "Accept, Authorization, Content-Type, X-CSRF-Token, X-Request-ID"

func (s *Service) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.originAllowed(origin, r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_origin", "message": "origin is not allowed"})
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			method := r.Header.Get("Access-Control-Request-Method")
			if !containsToken(corsMethods, method) {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_cors_method", "message": "method is not allowed"})
				return
			}
			for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
				if header = strings.TrimSpace(header); header != "" && !containsToken(corsHeaders, header) {
					writeJSON(w, http.StatusForbidden, map[string]string{"code": "invalid_cors_header", "message": "header is not allowed"})
					return
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", corsMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containsToken(list, value string) bool {
	for _, token := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(token), value) {
			return true
		}
	}
	return false
}
