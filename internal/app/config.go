package app

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"workbench/internal/foundation/auth"
	"workbench/internal/foundation/logging"
)

type Config struct {
	ListenAddress string
	DataPath      string
	AuthMode      auth.Mode
	Password      string
	PublicURL     string
	AllowedHosts  []string
	OpenAIAPIKey  string
	OpenAIBaseURL string
	OpenAIModel   string
	ShutdownGrace time.Duration
	LogLevel      string
}

func DefaultConfig() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, errors.New("could not determine user home directory")
	}
	return Config{
		ListenAddress: "127.0.0.1:8080",
		DataPath:      filepath.Join(home, ".workbench", "data.db"),
		AuthMode:      auth.ModeLocal,
		OpenAIModel:   "gpt-5.2",
		ShutdownGrace: 10 * time.Second,
		LogLevel:      "info",
	}, nil
}

// PublicHTTPS describes the browser-facing origin, never the HTTP listener.
func (c Config) PublicHTTPS() bool {
	return c.PublicURL != ""
}

func (c Config) Validate() error {
	if c.LogLevel != "" {
		if _, err := logging.ParseLevel(c.LogLevel); err != nil {
			return err
		}
	}
	if c.ListenAddress == "" || c.DataPath == "" {
		return errors.New("listen address and data path are required")
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
			return errors.New("public URL must be an HTTPS origin, for example https://workbench.example.com (no subpath, credentials, query or fragment)")
		}
		if c.AuthMode != auth.ModePassword {
			return errors.New("public URL requires password authentication")
		}
	}
	if c.ShutdownGrace <= 0 {
		return errors.New("shutdown grace period must be positive")
	}
	return nil
}
