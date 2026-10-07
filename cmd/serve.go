package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
	codexbackend "github.com/denysvitali/llm-proxy/internal/backend/codex"
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	minimaxcodebackend "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/denysvitali/llm-proxy/internal/server"
	"github.com/denysvitali/llm-proxy/internal/tracing"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	serveListen string
	serveConfig string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the proxy HTTP server",
	RunE: func(c *cobra.Command, args []string) error {
		if serveConfig != "" {
			_ = os.Setenv("LLM_PROXY_CONFIG", serveConfig)
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if serveListen != "" {
			cfg.Server.Listen = serveListen
		}
		return runServe(cfg)
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveListen, "listen", "", "listen address (overrides config/env)")
	serveCmd.Flags().StringVar(&serveConfig, "config", "", "path to config.yaml")
}

// buildBackends constructs the enabled backends in configuration order via
// the backend registry.
func buildBackends(cfg *config.Config) ([]backend.Backend, error) {
	return buildBackendsWithAccounts(cfg, accountProviders(cfg, nil))
}

// accountProviders creates one manager per provider. The same instances supply
// backend tokens and browser account routes when running the server.
func accountProviders(cfg *config.Config, captchaStore *zcodebackend.ValkeyCaptchaStore) server.AccountProviders {
	return server.AccountProviders{
		Grok:        grokbackend.NewManager(cfg.GrokAuthFile),
		WorkBuddy:   workbuddybackend.NewManager(cfg.WorkBuddyAuthFile),
		Codex:       codexbackend.NewManager(cfg.CodexAuthFile),
		ZCode:       zcodebackend.NewManagerWithCaptchaStore(cfg.ZCodeAuthFile, captchaStore),
		MiniMaxCode: minimaxcodebackend.NewManager(cfg.MiniMaxCodeAuthFile),
	}
}

func buildBackendsWithAccounts(cfg *config.Config, accounts server.AccountProviders) ([]backend.Backend, error) {
	tokenSources := accounts.TokenSources()
	out := make([]backend.Backend, 0, len(cfg.Backends))
	for _, bc := range cfg.EnabledBackends() {
		b, err := backend.New(bc.Type, backend.Options{
			BaseURL:     bc.BaseURL,
			APIKey:      bc.ResolveKey(os.Getenv),
			TokenSource: tokenSources[bc.Type],
			FreeOnly:    bc.FreeOnly,
		})
		if err != nil {
			return nil, fmt.Errorf("backends: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

func runServe(cfg *config.Config) error {
	log := logrus.New()
	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		return err
	}
	log.SetLevel(level)
	if cfg.LogFormat == "json" {
		log.SetFormatter(&logrus.JSONFormatter{})
	}
	for _, bc := range cfg.Backends {
		if strings.EqualFold(bc.Type, "grok") && (bc.APIKeyEnv != "" || bc.APIKey != "") {
			log.WithField("backend", "grok").Warn("legacy Grok API-key configuration is ignored; sign in from the dashboard")
		}
	}

	var store *auth.Store
	if cfg.Auth.File != "" {
		store, err = auth.NewStore(cfg.Auth.File)
		if err != nil {
			return err
		}
		// Watch the key store so `llm-proxy keys create` / revocations take
		// effect without a restart.
		stopReload := store.StartAutoReload(2 * time.Second)
		defer stopReload()
	}

	var zcodeCaptchaStore *zcodebackend.ValkeyCaptchaStore
	if cfg.Stats.RedisURL != "" && zcodeEnabled(cfg) {
		zcodeCaptchaStore, err = zcodebackend.NewValkeyCaptchaStore(cfg.Stats.RedisURL, cfg.Stats.RedisKeyPrefix)
		if err != nil {
			return err
		}
		defer func() { _ = zcodeCaptchaStore.Close() }()
	}
	accounts := accountProviders(cfg, zcodeCaptchaStore)
	backends, err := buildBackendsWithAccounts(cfg, accounts)
	if err != nil {
		return err
	}
	if len(backends) == 0 {
		log.Warn("no backends configured; only health and dashboard API endpoints will work")
	}

	// OTel tracing activates only when OTEL_* environment variables point at
	// a collector; otherwise the global no-op tracer stays in place.
	shutdownTracing, err := tracing.Setup(context.Background(), "llm-proxy")
	if err != nil {
		log.WithError(err).Warn("tracing setup failed; continuing without spans")
	} else if shutdownTracing != nil {
		defer func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = shutdownTracing(flushCtx)
		}()
	}

	srv := server.NewWithDependencies(server.Dependencies{
		Config: cfg, Logger: log, Auth: store, Backends: backends, Accounts: accounts,
	})
	defer func() { _ = srv.Close() }()

	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: streaming responses must stay open as long as the
		// upstream sends events.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if minimaxAutoCheckinEnabled(cfg) {
		checkinCtx, cancelCheckin := context.WithCancel(ctx)
		checkinDone := make(chan struct{})
		go func() {
			defer close(checkinDone)
			accounts.MiniMaxCode.RunAutoCheckin(checkinCtx, log)
		}()
		defer func() {
			cancelCheckin()
			<-checkinDone
		}()
	}
	errCh := make(chan error, 1)
	go func() {
		log.WithField("listen", cfg.Server.Listen).
			WithField("backends", backendNames(backends)).
			Info("llm-proxy listening")
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func minimaxAutoCheckinEnabled(cfg *config.Config) bool {
	if cfg.MiniMaxCodeAutoCheckin != nil && !*cfg.MiniMaxCodeAutoCheckin {
		return false
	}
	for _, bc := range cfg.EnabledBackends() {
		if bc.Type == "minimax-code" {
			return true
		}
	}
	return false
}

func zcodeEnabled(cfg *config.Config) bool {
	for _, bc := range cfg.EnabledBackends() {
		if strings.EqualFold(bc.Type, "zcode") {
			return true
		}
	}
	return false
}

func backendNames(bs []backend.Backend) []string {
	names := make([]string, 0, len(bs))
	for _, b := range bs {
		names = append(names, b.Name())
	}
	return names
}
