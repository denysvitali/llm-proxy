// Package server implements the llm-proxy HTTP surface: Anthropic Messages,
// OpenAI Chat Completions and Responses endpoints, model routing across the
// configured backends, API-key authentication, and the dashboard.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/sirupsen/logrus"
)

// Server wires configuration, authentication, and backends into the proxy
// HTTP handler. Safe for concurrent use.
type Server struct {
	cfg      *config.Config
	log      logrus.FieldLogger
	auth     *auth.Store // nil disables client authentication
	backends []backend.Backend
	byName   map[string]backend.Backend
	metrics  *Metrics
	stats    *Stats
	catalogs catalogCache
}

// New builds a Server. backends must already be constructed from cfg entries
// in config order; auth may be nil for unauthenticated loopback deployments.
func New(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend) *Server {
	if log == nil {
		log = logrus.StandardLogger()
	}
	byName := make(map[string]backend.Backend, len(backends))
	for _, b := range backends {
		byName[b.Name()] = b
	}
	cfg.Defaults()
	metrics := newMetrics()
	return &Server{
		cfg:      cfg,
		log:      log,
		auth:     store,
		backends: backends,
		byName:   byName,
		metrics:  metrics,
		stats:    newStats(metrics.reg),
		catalogs: newCatalogCache(),
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
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", s.metrics.handler())
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	return s.withMiddleware(mux)
}

// route is a resolved routing decision: which backend receives the request
// and under which upstream model name.
type route struct {
	backend backend.Backend
	model   string
}

// resolve maps an inbound model name to a backend + upstream model.
// A "<backend>/<model>" ID has highest precedence: it addresses one backend
// directly, bypassing routes, catalogs and DefaultRoute (split at the first
// "/", so nested upstream names like "nousresearch/hermes-4-70b" on backend
// "nous" work). Otherwise: explicit Routes entry, then live catalogs of
// enabled backends in config order, then DefaultRoute. ok=false means no
// route exists; callers answer 404 (model not found).
func (s *Server) resolve(ctx context.Context, model string) (route, bool) {
	if prefix, rest, found := strings.Cut(model, "/"); found && rest != "" {
		if b, known := s.byName[prefix]; known {
			if !s.enabled(prefix) {
				return route{}, false
			}
			return route{backend: b, model: rest}, true
		}
	}
	if r, ok := s.cfg.Routes[model]; ok {
		if b, known := s.byName[r.Backend]; known && s.enabled(r.Backend) {
			upstream := r.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, true
		}
	}
	for _, b := range s.backends {
		if !s.enabled(b.Name()) {
			continue
		}
		models, err := s.catalog(ctx, b)
		if err != nil {
			s.log.WithError(err).WithField("backend", b.Name()).Warn("catalog fetch failed")
			continue
		}
		if hasModel(models, model) {
			return route{backend: b, model: model}, true
		}
	}
	if d := s.cfg.DefaultRoute; d.Backend != "" {
		if b, known := s.byName[d.Backend]; known && s.enabled(d.Backend) {
			upstream := d.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, true
		}
	}
	return route{}, false
}

func (s *Server) enabled(name string) bool {
	bc, ok := s.cfg.BackendByType(name)
	if !ok {
		return false
	}
	return bc.IsEnabled()
}

// hasModel reports whether the list contains an exact match, ignoring a
// trailing -YYYYMMDD date suffix on catalog entries (Anthropic-style dated
// snapshots).
func hasModel(models []string, want string) bool {
	for _, m := range models {
		if m == want {
			return true
		}
		if len(m) > 9 && m[len(m)-9] == '-' && m[:len(m)-9] == want {
			return true
		}
	}
	return false
}

type cachedCatalog struct {
	models  []string
	expires time.Time
}

// catalogCache memoizes backend model lists for a minute so request routing
// does not hammer provider /models endpoints.
type catalogCache struct {
	mu      sync.Mutex
	entries map[string]cachedCatalog
	ttl     time.Duration
}

func newCatalogCache() catalogCache {
	return catalogCache{entries: map[string]cachedCatalog{}, ttl: time.Minute}
}

func (s *Server) catalog(ctx context.Context, b backend.Backend) ([]string, error) {
	s.catalogs.mu.Lock()
	cached, ok := s.catalogs.entries[b.Name()]
	s.catalogs.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.models, nil
	}
	models, err := b.Models(ctx)
	if err != nil {
		if ok {
			// Serve stale rather than failing routing entirely.
			return cached.models, nil
		}
		return nil, err
	}
	s.catalogs.mu.Lock()
	s.catalogs.entries[b.Name()] = cachedCatalog{models: models, expires: time.Now().Add(s.catalogs.ttl)}
	s.catalogs.mu.Unlock()
	return models, nil
}

// rewriteModel replaces only the "model" field of a JSON object, preserving
// every other byte-semantic exactly as the client sent it.
func rewriteModel(body []byte, model string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	fields["model"] = encoded
	return json.Marshal(fields)
}

// readBody enforces the configured maximum request body size.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := readAll(r.Body, s.cfg.Server.MaxBodyBytes)
	if err != nil || bodyTooLarge(body, s.cfg.Server.MaxBodyBytes) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "invalid_request_error", "request body too large")
		return nil, false
	}
	return body, true
}

// sortedRoutes returns route keys deterministically for the dashboard.
func (s *Server) sortedRoutes() []string {
	keys := make([]string, 0, len(s.cfg.Routes))
	for k := range s.cfg.Routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
