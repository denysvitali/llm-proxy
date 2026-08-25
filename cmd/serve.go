package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/denysvitali/llm-proxy/internal/server"
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
	out := make([]backend.Backend, 0, len(cfg.Backends))
	for _, bc := range cfg.EnabledBackends() {
		b, err := backend.New(bc.Type, backend.Options{
			BaseURL:  bc.BaseURL,
			APIKey:   bc.ResolveKey(os.Getenv),
			FreeOnly: bc.FreeOnly,
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

	var store *auth.Store
	if cfg.Auth.File != "" {
		store, err = auth.NewStore(cfg.Auth.File)
		if err != nil {
			return err
		}
	}

	backends, err := buildBackends(cfg)
	if err != nil {
		return err
	}
	if len(backends) == 0 {
		log.Warn("no backends configured; only health/dashboard endpoints will work")
	}
	srv := server.New(cfg, log, store, backends)
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
