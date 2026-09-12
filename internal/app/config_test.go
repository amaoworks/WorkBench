package app

import (
	"testing"
	"time"

	"workbench/internal/foundation/auth"
)

func TestPublicURLValidation(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"", true}, {"https://workbench.example.com", true}, {"https://workbench.example.com:8443/", true},
		{"http://workbench.example.com", false}, {"https://", false}, {"https://user:pass@example.com", false},
		{"https://example.com/workbench", false}, {"https://example.com/?token=secret", false},
		{"https://example.com?", false}, {"https://example.com/#fragment", false}, {"https://example.com:bad", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			cfg := Config{ListenAddress: "127.0.0.1:8080", DataPath: "data.db", AuthMode: auth.ModePassword, ShutdownGrace: time.Second, PublicURL: tc.url}
			if err := cfg.Validate(); (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid = %v", err, tc.valid)
			}
		})
	}
	cfg := Config{ListenAddress: "127.0.0.1:8080", DataPath: "data.db", AuthMode: auth.ModeLocal, ShutdownGrace: time.Second, PublicURL: "https://example.com"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("proxy deployment accepted local authentication")
	}
}
