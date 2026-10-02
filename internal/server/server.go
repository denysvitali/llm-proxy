// Package server implements the llm-proxy HTTP surface: Anthropic Messages,
// OpenAI Chat Completions and Responses endpoints, model routing across the
// configured backends, API-key authentication, and the dashboard.
package server

import (
	"net/http"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/sirupsen/logrus"
)

// Server wires configuration, authentication, and backends into the proxy
// HTTP handler. Safe for concurrent use.
type Server struct {
	cfg              *config.Config
	log              logrus.FieldLogger
	auth             *auth.Store // nil disables client authentication
	backends         []backend.Backend
	byName           map[string]backend.Backend
	updates          *updateHub
	metrics          *Metrics
	stats            *Stats
	accounts         AccountProviders
	accountProviders map[string]*accountProvider
	catalogs         catalogCache
	grokUsageMu      sync.Mutex
	grokUsageValue   *grokbackend.UsageView
	zcodeUsageMu     sync.Mutex
	zcodeUsagePlans  []zcodebackend.PlanUsage
	zcodeUsageAt     time.Time
	zcodeQuotaMu     sync.Mutex
	zcodeQuotaValue  zcodebackend.PlanQuota
	zcodeQuotaAt     time.Time
}

const (
	grokUsageBackendName  = "grok"
	grokUsageTTL          = time.Minute
	zcodeUsageBackendName = "zcode"
	zcodeUsageTTL         = time.Minute
)

// Dependencies names the components required to assemble a Server. Backends
// must already be constructed in configuration order; Auth may be nil for
// unauthenticated loopback deployments.
type Dependencies struct {
	Config   *config.Config
	Logger   logrus.FieldLogger
	Auth     *auth.Store
	Backends []backend.Backend
	Accounts AccountProviders
}

// New builds a Server without subscription account integrations.
func New(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend) *Server {
	return NewWithDependencies(Dependencies{Config: cfg, Logger: log, Auth: store, Backends: backends})
}

// NewWithDependencies builds a Server with named account integrations.
func NewWithDependencies(deps Dependencies) *Server {
	cfg, log := deps.Config, deps.Logger
	if cfg == nil {
		cfg = &config.Config{}
	}
	if log == nil {
		log = logrus.StandardLogger()
	}
	byName := make(map[string]backend.Backend, len(deps.Backends))
	for _, b := range deps.Backends {
		byName[b.Name()] = b
	}
	cfg.Defaults()
	metrics := newMetrics()
	updates := newUpdateHub()
	stats := newStats(metrics.reg, cfg.Stats)
	stats.updates = updates
	if stats.redisInitErr != nil {
		log.WithError(stats.redisInitErr).Warn("shared stats initialization failed; using process-local stats")
	} else if stats.redis != nil {
		stats.redis.startUpdates(updates.notify)
	} else if err := stats.load(cfg.Stats.PersistFile); err != nil {
		log.WithError(err).Warn("stats persistence load failed; starting with empty stats")
	}
	return &Server{
		cfg:              cfg,
		log:              log,
		auth:             deps.Auth,
		backends:         deps.Backends,
		byName:           byName,
		updates:          updates,
		metrics:          metrics,
		stats:            stats,
		accounts:         deps.Accounts,
		accountProviders: deps.Accounts.providers(),
		catalogs:         newCatalogCache(),
	}
}

// Handler returns the full proxy HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /api/stats", s.handleStatsSeries)
	mux.HandleFunc("GET /api/stats/errors", s.handleStatsErrors)
	mux.HandleFunc("GET /api/requests", s.handleRequests)
	mux.HandleFunc("GET /api/requests/{id}", s.handleRequest)
	mux.HandleFunc("GET /api/stats/backends/{backend}", s.handleStatsBackendSeries)
	mux.HandleFunc("GET /api/stats/backends/{backend}/{model}", s.handleStatsBackendSeries)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/updates/ws", s.handleUpdatesWebSocket)
	mux.HandleFunc("GET /api/updates/sse", s.handleUpdatesSSE)
	for _, provider := range s.accountProviders {
		provider.registerRoutes(s, mux)
	}
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", s.metrics.handler())
	mux.HandleFunc("GET /{$}", s.handleSPA)
	mux.HandleFunc("GET /{path...}", s.handleSPA)
	return s.withMiddleware(mux)
}

// Close shuts down the stats persistence layer, flushing once more.
func (s *Server) Close() error {
	s.updates.close()
	return s.stats.Close()
}
