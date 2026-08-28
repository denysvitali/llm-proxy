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
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
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
	return buildBackendsWithTokenSources(cfg, grokbackend.NewManager(cfg.GrokAuthFile), workbuddybackend.NewSession(cfg.WorkBuddyAuthFile))
}

func buildBackendsWithTokenSources(cfg *config.Config, grokTokens, workBuddyTokens backend.TokenSource) ([]backend.Backend, error) {
	out := make([]backend.Backend, 0, len(cfg.Backends))
	for _, bc := range cfg.EnabledBackends() {
		b, err := backend.New(bc.Type, backend.Options{
			BaseURL:     bc.BaseURL,
			APIKey:      bc.ResolveKey(os.Getenv),
			TokenSource: tokensForBackend(bc.Type, grokTokens, workBuddyTokens),
			FreeOnly:    bc.FreeOnly,
		})
		if err != nil {
			return nil, fmt.Errorf("backends: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

func tokensForBackend(name string, grokTokens, workBuddyTokens backend.TokenSource) backend.TokenSource {
	if name == "grok" {
		return grokTokens
	}
	if name == "workbuddy" {
		return workBuddyTokens
	}
	return nil
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
	}

	grokTokens := grokbackend.NewManager(cfg.GrokAuthFile)
	workBuddyTokens := workbuddybackend.NewManager(cfg.WorkBuddyAuthFile)
	backends, err := buildBackendsWithTokenSources(cfg, grokTokens, workBuddyTokens)
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

	srv := server.NewWithAccountAuth(cfg, log, store, backends, grokTokens, workBuddyTokens)
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

func backendNames(bs []backend.Backend) []string {
	names := make([]string, 0, len(bs))
	for _, b := range bs {
		names = append(names, b.Name())
	}
	return names
}
