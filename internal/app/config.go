package app

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"workbench/internal/foundation/auth"
)

type Config struct {
	ListenAddress string
	DataPath      string
	AuthMode      auth.Mode
	Password      string
	TLSCertFile   string
	TLSKeyFile    string
	AllowedHosts  []string
	OpenAIAPIKey  string
	OpenAIBaseURL string
	OpenAIModel   string
	ShutdownGrace time.Duration
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
	}, nil
}

func (c Config) HTTPS() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

func (c Config) Validate() error {
	if c.ListenAddress == "" || c.DataPath == "" {
		return errors.New("listen address and data path are required")
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("TLS certificate and key must be configured together")
	}
	if c.ShutdownGrace <= 0 {
		return errors.New("shutdown grace period must be positive")
	}
	return nil
}
