// Package server implements the llm-proxy HTTP surface: Anthropic Messages,
// OpenAI Chat Completions and Responses endpoints, model routing across the
// configured backends, API-key authentication, and the dashboard.
package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	stats := newStats(metrics.reg, cfg.Stats)
	if err := stats.load(cfg.Stats.PersistFile); err != nil {
		log.WithError(err).Warn("stats persistence load failed; starting with empty stats")
	}
	return &Server{
		cfg:      cfg,
		log:      log,
		auth:     store,
		backends: backends,
		byName:   byName,
		metrics:  metrics,
		stats:    stats,
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
	mux.HandleFunc("GET /api/stats", s.handleStatsSeries)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", s.metrics.handler())
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /{$}", s.handleSPA)
	mux.HandleFunc("GET /{path...}", s.handleSPA)
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
// snapshots: a dash followed by exactly eight ASCII digits forming a valid
// calendar date).
func hasModel(models []string, want string) bool {
	for _, m := range models {
		if m == want {
			return true
		}
		if len(m) > 9 && m[len(m)-9] == '-' && m[:len(m)-9] == want && isDatedSnapshotSuffix(m[len(m)-8:]) {
			return true
		}
	}
	return false
}

// isDatedSnapshotSuffix reports whether s is exactly eight ASCII digits that
// parse as a valid YYYYMMDD date (e.g. 20250514).
func isDatedSnapshotSuffix(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < 8; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := time.Parse("20060102", s)
	return err == nil
}

type cachedCatalog struct {
	models  []string
	expires time.Time
}

const (
	// catalogTTL is how long a fetched model list is considered fresh.
	catalogTTL = time.Minute
	// catalogMaxStaleness is the upper bound on how long a stale catalog
	// entry may be served while a backend's /models endpoint is failing (10x
	// the TTL). Beyond it the refresh error propagates so a permanently
	// broken catalog endpoint does not go unnoticed.
	catalogMaxStaleness = 10 * catalogTTL
)

// catalogCache memoizes backend model lists for catalogTTL so request routing
// does not hammer provider /models endpoints. Once an entry is older than
// catalogMaxStaleness it is refreshed even if the backend is failing.
type catalogCache struct {
	mu      sync.Mutex
	entries map[string]cachedCatalog
	ttl     time.Duration
}

func newCatalogCache() catalogCache {
	return catalogCache{entries: map[string]cachedCatalog{}, ttl: catalogTTL}
}

func (s *Server) catalog(ctx context.Context, b backend.Backend) ([]string, error) {
	s.catalogs.mu.Lock()
	cached, ok := s.catalogs.entries[b.Name()]
	s.catalogs.mu.Unlock()

	now := time.Now()
	if ok && now.Before(cached.expires) {
		return cached.models, nil
	}

	models, err := b.Models(ctx)
	if err != nil {
		if ok {
			// Age is measured from when the entry was last refreshed.
			age := now.Sub(cached.expires.Add(-s.catalogs.ttl))
			if age < catalogMaxStaleness {
				s.log.WithField("backend", b.Name()).
					WithField("age", age.Round(time.Second)).
					Warn("serving stale model catalog; backend /models is failing")
				return cached.models, nil
			}
			s.log.WithField("backend", b.Name()).
				WithField("age", age.Round(time.Second)).
				Warn("stale model catalog exceeds maximum age; propagating backend error")
		}
		return nil, err
	}

	s.catalogs.mu.Lock()
	s.catalogs.entries[b.Name()] = cachedCatalog{models: models, expires: now.Add(s.catalogs.ttl)}
	s.catalogs.mu.Unlock()
	return models, nil
}

// rewriteModel replaces only the top-level "model" string value of a JSON
// object, preserving every other byte exactly as the client sent it: key
// order, whitespace, indentation, string escapes and nested values are all
// left untouched.
func rewriteModel(body []byte, model string) ([]byte, error) {
	// Validate that the body is a JSON object before mutating anything.
	var probe interface{}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, err
	}
	if _, ok := probe.(map[string]interface{}); !ok {
		return nil, fmt.Errorf("body is not a JSON object")
	}

	quoted, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	return spliceModelValue(body, quoted)
}

// spliceModelValue walks body byte-by-byte, tracking JSON depth, to find the
// top-level "model" key and replace its string value with quoted. Everything
// outside that value is copied verbatim.
func spliceModelValue(body, quoted []byte) ([]byte, error) {
	n := len(body)
	depth := 0
	for i := 0; i < n; {
		switch body[i] {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
		case '[':
			depth++
			i++
		case ']':
			depth--
			i++
		case '"':
			key, end, err := parseJSONString(body, i)
			if err != nil {
				return nil, err
			}
			// A string immediately followed by ':' is an object key.
			j := end
			for j < n && isJSONSpace(body[j]) {
				j++
			}
			if j < n && body[j] == ':' && depth == 1 && key == "model" {
				// Skip whitespace after ':' to reach the value.
				k := j + 1
				for k < n && isJSONSpace(body[k]) {
					k++
				}
				if k >= n || body[k] != '"' {
					return nil, fmt.Errorf(`"model" value is not a string`)
				}
				_, vend, err := parseJSONString(body, k)
				if err != nil {
					return nil, err
				}
				out := make([]byte, 0, len(body)-(vend-k)+len(quoted))
				out = append(out, body[:k]...)
				out = append(out, quoted...)
				out = append(out, body[vend:]...)
				return out, nil
			}
			i = end
		default:
			// Number, literal or whitespace: the validation pass already
			// guaranteed these are well-formed, so a byte-wise walk is safe.
			i++
		}
	}
	return nil, fmt.Errorf(`no top-level "model" key found`)
}

// parseJSONString parses the JSON string beginning at body[i] (which must be
// '"') and returns its decoded contents together with the exclusive end
// offset. It handles all JSON escapes, including \uXXXX surrogate pairs.
func parseJSONString(body []byte, i int) (string, int, error) {
	if i >= len(body) || body[i] != '"' {
		return "", 0, fmt.Errorf("expected string")
	}
	i++ // skip opening quote
	var sb strings.Builder
	for i < len(body) {
		c := body[i]
		switch c {
		case '"':
			return sb.String(), i + 1, nil
		case '\\':
			i++
			if i >= len(body) {
				return "", 0, fmt.Errorf("unterminated string escape")
			}
			switch body[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				sb.WriteByte(body[i])
			case 'u':
				if i+4 >= len(body) {
					return "", 0, fmt.Errorf("invalid unicode escape")
				}
				r := hexToRune(body[i+1 : i+5])
				if r < 0 {
					return "", 0, fmt.Errorf("invalid unicode escape")
				}
				i += 4
				// Decode an optional low surrogate to form a supplementary
				// code point (\uXXXX\uXXXX pair).
				if r >= 0xD800 && r <= 0xDBFF && i+6 < len(body) && body[i+1] == '\\' && body[i+2] == 'u' {
					if lo := hexToRune(body[i+3 : i+7]); lo >= 0xDC00 && lo <= 0xDFFF {
						r = 0x10000 + ((r - 0xD800) << 10) + (lo - 0xDC00)
						i += 6
					}
				}
				sb.WriteRune(r)
			default:
				return "", 0, fmt.Errorf("invalid escape")
			}
			i++
		default:
			sb.WriteByte(c)
			i++
		}
	}
	return "", 0, fmt.Errorf("unterminated string")
}

// hexToRune converts exactly four ASCII hex digits to a rune, or returns -1.
func hexToRune(h []byte) rune {
	if len(h) != 4 {
		return -1
	}
	var r rune
	for _, c := range h {
		r *= 16
		switch {
		case '0' <= c && c <= '9':
			r += rune(c - '0')
		case 'a' <= c && c <= 'f':
			r += rune(c - 'a' + 10)
		case 'A' <= c && c <= 'F':
			r += rune(c - 'A' + 10)
		default:
			return -1
		}
	}
	return r
}

func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
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

// Close shuts down the stats persistence layer, flushing once more.
func (s *Server) Close() error {
	return s.stats.Close()
}
