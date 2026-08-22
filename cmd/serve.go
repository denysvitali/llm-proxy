package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/grok"
	"github.com/denysvitali/llm-proxy/internal/backend/opencode"
	"github.com/denysvitali/llm-proxy/internal/backend/venice"
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

// buildBackends constructs the enabled backends in configuration order.
func buildBackends(cfg *config.Config) []backend.Backend {
	var out []backend.Backend
	for _, bc := range cfg.EnabledBackends() {
		key := bc.ResolveKey(os.Getenv)
		switch bc.Type {
		case "venice":
			out = append(out, venice.New(bc.BaseURL, key))
		case "opencode":
			out = append(out, opencode.New(bc.BaseURL, key))
		case "grok":
			out = append(out, grok.New(bc.BaseURL, key))
		}
	}
	return out
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

	backends := buildBackends(cfg)
	if len(backends) == 0 {
		log.Warn("no backends configured; only health/dashboard endpoints will work")
	}
	srv := server.New(cfg, log, store, backends)

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
