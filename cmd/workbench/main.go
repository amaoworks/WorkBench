package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"workbench/internal/app"
	"workbench/internal/foundation/auth"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type options struct {
	config      app.Config
	version     bool
	healthcheck bool
}

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("workbench failed", "service", "workbench", "component", "app", "error", err)
		os.Exit(1)
	}
}

func parseOptions(args []string, getenv func(string) string, output io.Writer) (options, error) {
	config, err := app.DefaultConfig()
	if err != nil {
		return options{}, err
	}
	envDefault := func(key, fallback string) string {
		if value := getenv(key); value != "" {
			return value
		}
		return fallback
	}
	opt := options{config: config}
	flags := flag.NewFlagSet("workbench", flag.ContinueOnError)
	flags.SetOutput(output)
	var mode string
	flags.StringVar(&opt.config.ListenAddress, "listen", envDefault("WORKBENCH_LISTEN", config.ListenAddress), "HTTP listen address")
	flags.StringVar(&opt.config.DataPath, "data", envDefault("WORKBENCH_DATA", config.DataPath), "SQLite database path")
	flags.StringVar(&opt.config.FutuConfigDir, "futu-config-dir", getenv("WORKBENCH_FUTU_CONFIG_DIR"), "shared directory for managed Futu OpenD login configuration")
	flags.StringVar(&opt.config.FutuRuntimeDir, "futu-runtime-dir", getenv("WORKBENCH_FUTU_RUNTIME_DIR"), "private OpenD runtime directory (defaults beside the database)")
	flags.StringVar(&opt.config.FutuOpenDBinary, "futu-opend-binary", getenv("WORKBENCH_FUTU_OPEND_BINARY"), "installed OpenD executable (otherwise downloaded on first enable)")
	flags.StringVar(&opt.config.FutuOpenDAddress, "futu-opend-address", envDefault("WORKBENCH_FUTU_OPEND_ADDRESS", "127.0.0.1:11111"), "Futu OpenD TCP host:port (deployment configuration)")
	allowFutuNonLocal, err := strconv.ParseBool(envDefault("WORKBENCH_FUTU_ALLOW_NON_LOCAL", "false"))
	if err != nil {
		return options{}, errors.New("WORKBENCH_FUTU_ALLOW_NON_LOCAL must be true or false")
	}
	flags.BoolVar(&opt.config.FutuAllowNonLocal, "futu-allow-non-local", allowFutuNonLocal, "allow a non-loopback Futu OpenD host")
	flags.StringVar(&mode, "auth", envDefault("WORKBENCH_AUTH", string(config.AuthMode)), "authentication mode: local or password")
	flags.StringVar(&opt.config.PublicURL, "public-url", getenv("WORKBENCH_PUBLIC_URL"), "browser-facing HTTPS origin served by a reverse proxy")
	flags.StringVar(&opt.config.LogLevel, "log-level", envDefault("WORKBENCH_LOG_LEVEL", config.LogLevel), "initial log level: debug, info, warn or error (saved settings take precedence)")
	flags.BoolVar(&opt.version, "version", false, "print version and exit")
	flags.BoolVar(&opt.healthcheck, "healthcheck", false, "check the running HTTP service and exit")
	flags.Func("allowed-host", "additional allowed HTTP Host header (repeatable)", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("allowed host cannot be empty")
		}
		opt.config.AllowedHosts = append(opt.config.AllowedHosts, value)
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected positional arguments; use -help for usage")
	}
	opt.config.AuthMode = auth.Mode(mode)
	opt.config.Password = getenv("WORKBENCH_PASSWORD")
	opt.config.OpenAIAPIKey = getenv("OPENAI_API_KEY")
	opt.config.OpenAIBaseURL = getenv("OPENAI_BASE_URL")
	opt.config.OpenAIModel = envDefault("OPENAI_MODEL", config.OpenAIModel)
	if len(opt.config.AllowedHosts) == 0 {
		for _, host := range strings.Split(getenv("WORKBENCH_ALLOWED_HOSTS"), ",") {
			if host = strings.TrimSpace(host); host != "" {
				opt.config.AllowedHosts = append(opt.config.AllowedHosts, host)
			}
		}
	}
	return opt, nil
}

func run() error {
	opt, err := parseOptions(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if opt.version {
		fmt.Printf("workbench %s (commit %s, built %s)\n", version, commit, buildDate)
		return nil
	}
	if err := opt.config.Validate(); err != nil {
		return err
	}
	if opt.healthcheck {
		return healthcheck(opt.config)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, opt.config, logger)
	if err != nil {
		return err
	}
	defer application.Close()
	return application.Run(ctx)
}

// Probe HTTP directly without opening the database or needing curl in the image.
func healthcheck(config app.Config) error {
	host, port, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return fmt.Errorf("healthcheck listen address: %w", err)
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/health/ready", nil)
	if err != nil {
		return err
	}
	if config.PublicURL != "" {
		origin, err := url.Parse(config.PublicURL)
		if err != nil {
			return err
		}
		req.Host = origin.Host
	} else if len(config.AllowedHosts) != 0 {
		req.Host = config.AllowedHosts[0]
	}
	client := &http.Client{
		Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: HTTP %d", response.StatusCode)
	}
	return nil
}
