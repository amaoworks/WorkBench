package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"workbench/internal/app"
	"workbench/internal/foundation/auth"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	config, err := app.DefaultConfig()
	if err != nil {
		return err
	}
	var mode string
	flag.StringVar(&config.ListenAddress, "listen", config.ListenAddress, "HTTP listen address")
	flag.StringVar(&config.DataPath, "data", config.DataPath, "SQLite database path")
	flag.StringVar(&mode, "auth", string(config.AuthMode), "authentication mode: local or password")
	flag.StringVar(&config.TLSCertFile, "tls-cert", "", "TLS certificate file")
	flag.StringVar(&config.TLSKeyFile, "tls-key", "", "TLS private key file")
	flag.Func("allowed-host", "allowed HTTP Host header (repeatable; required for wildcard/public listeners)", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("allowed host cannot be empty")
		}
		config.AllowedHosts = append(config.AllowedHosts, value)
		return nil
	})
	flag.Parse()
	config.AuthMode = auth.Mode(mode)
	config.Password = os.Getenv("WORKBENCH_PASSWORD")
	config.OpenAIAPIKey = os.Getenv("OPENAI_API_KEY")
	config.OpenAIBaseURL = os.Getenv("OPENAI_BASE_URL")
	if hosts := os.Getenv("WORKBENCH_ALLOWED_HOSTS"); hosts != "" && len(config.AllowedHosts) == 0 {
		for _, host := range strings.Split(hosts, ",") {
			if host = strings.TrimSpace(host); host != "" {
				config.AllowedHosts = append(config.AllowedHosts, host)
			}
		}
	}
	if model := os.Getenv("OPENAI_MODEL"); model != "" {
		config.OpenAIModel = model
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, config, logger)
	if err != nil {
		return err
	}
	defer application.Close()
	return application.Run(ctx)
}
