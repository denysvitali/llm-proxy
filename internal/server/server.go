// Package server implements the llm-proxy HTTP surface: Anthropic Messages,
// OpenAI Chat Completions and Responses endpoints, model routing across the
// configured backends, API-key authentication, and the dashboard.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/auth"
	"github.com/denysvitali/llm-proxy/internal/backend"
	codexbackend "github.com/denysvitali/llm-proxy/internal/backend/codex"
	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	workbuddybackend "github.com/denysvitali/llm-proxy/internal/backend/workbuddy"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/sirupsen/logrus"
)

// Server wires configuration, authentication, and backends into the proxy
// HTTP handler. Safe for concurrent use.
type Server struct {
	cfg             *config.Config
	log             logrus.FieldLogger
	auth            *auth.Store // nil disables client authentication
	backends        []backend.Backend
	byName          map[string]backend.Backend
	updates         *updateHub
	metrics         *Metrics
	stats           *Stats
	grokAuth        *grokbackend.Manager
	workBuddyAuth   *workbuddybackend.Manager
	codexAuth       *codexbackend.Manager
	zcodeAuth       *zcodebackend.Manager
	catalogs        catalogCache
	grokUsageMu     sync.Mutex
	grokUsageValue  *grokbackend.UsageView
	zcodeUsageMu    sync.Mutex
	zcodeUsagePlans []zcodebackend.PlanUsage
	zcodeUsageAt    time.Time
}

const (
	grokUsageBackendName  = "grok"
	grokUsageTTL          = time.Minute
	zcodeUsageBackendName = "zcode"
	zcodeUsageTTL         = time.Minute
)

// New builds a Server. backends must already be constructed from cfg entries
// in config order; auth may be nil for unauthenticated loopback deployments.
func New(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend) *Server {
	return newServer(cfg, log, store, backends, nil, nil, nil, nil)
}

// NewWithGrokAuth wires the xAI account session into the browser-only sign-in
// page as well as the Grok backend.
func NewWithGrokAuth(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend, grokAuth *grokbackend.Manager) *Server {
	return newServer(cfg, log, store, backends, grokAuth, nil, nil, nil)
}

// NewWithAccountAuth wires browser sign-in for subscription backends.
func NewWithAccountAuth(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend, grokAuth *grokbackend.Manager, workBuddyAuth *workbuddybackend.Manager) *Server {
	return newServer(cfg, log, store, backends, grokAuth, workBuddyAuth, nil, nil)
}

// NewWithAllAccountAuth wires browser sign-in for every subscription backend.
func NewWithAllAccountAuth(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend, grokAuth *grokbackend.Manager, workBuddyAuth *workbuddybackend.Manager, codexAuth *codexbackend.Manager, zcodeAuth *zcodebackend.Manager) *Server {
	return newServer(cfg, log, store, backends, grokAuth, workBuddyAuth, codexAuth, zcodeAuth)
}

func newServer(cfg *config.Config, log logrus.FieldLogger, store *auth.Store, backends []backend.Backend, grokAuth *grokbackend.Manager, workBuddyAuth *workbuddybackend.Manager, codexAuth *codexbackend.Manager, zcodeAuth *zcodebackend.Manager) *Server {
	if log == nil {
		log = logrus.StandardLogger()
	}
	byName := make(map[string]backend.Backend, len(backends))
	for _, b := range backends {
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
		cfg:           cfg,
		log:           log,
		auth:          store,
		backends:      backends,
		byName:        byName,
		updates:       updates,
		metrics:       metrics,
		stats:         stats,
		grokAuth:      grokAuth,
		workBuddyAuth: workBuddyAuth,
		codexAuth:     codexAuth,
		zcodeAuth:     zcodeAuth,
		catalogs:      newCatalogCache(),
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
	mux.HandleFunc("GET /api/grok/usage", s.handleGrokUsage)
	mux.HandleFunc("GET /api/updates/ws", s.handleUpdatesWebSocket)
	mux.HandleFunc("GET /api/updates/sse", s.handleUpdatesSSE)
	mux.HandleFunc("GET /login", s.grokLoginPage)
	mux.HandleFunc("POST /login", s.grokLogin)
	mux.HandleFunc("GET /login/workbuddy", s.workBuddyLoginPage)
	mux.HandleFunc("POST /login/workbuddy", s.workBuddyLogin)
	mux.HandleFunc("GET /login/codex", s.codexLoginPage)
	mux.HandleFunc("POST /login/codex", s.codexLogin)
	mux.HandleFunc("GET /login/zcode", s.zcodeLoginPage)
	mux.HandleFunc("POST /login/zcode", s.zcodeLogin)
	mux.HandleFunc("POST /login/zcode/captcha", s.zcodeCaptcha)
	mux.HandleFunc("POST /api/zcode/claim", s.zcodeClaim)
	mux.HandleFunc("GET /api/zcode/offers", s.zcodeOffers)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", s.metrics.handler())
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

// resolveWithFallbacks maps an inbound model name to a backend + upstream
// model, and reports the fallback entries attached to the route entry that
// matched (explicit route or default route; qualified IDs and catalog
// matches carry none of their own).
//
// A "<backend>/<model>" ID has highest precedence: it addresses one backend
// directly, bypassing routes, catalogs and DefaultRoute (split at the first
// "/", so nested upstream names like "nousresearch/hermes-4-70b" on backend
// "nous" work). Otherwise: explicit Routes entry, then live catalogs of
// enabled backends in config order, then DefaultRoute. ok=false means no
// route exists; callers answer 404 (model not found).
func (s *Server) resolveWithFallbacks(ctx context.Context, model string) (route, []config.FallbackRoute, bool) {
	if prefix, rest, found := strings.Cut(model, "/"); found && rest != "" {
		if b, known := s.byName[prefix]; known {
			if !s.enabled(prefix) {
				return route{}, nil, false
			}
			return route{backend: b, model: rest}, nil, true
		}
	}
	if r, ok := s.cfg.Routes[model]; ok {
		if b, known := s.byName[r.Backend]; known && s.enabled(r.Backend) {
			upstream := r.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, r.Fallbacks, true
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
			return route{backend: b, model: model}, nil, true
		}
	}
	if d := s.cfg.DefaultRoute; d.Backend != "" {
		if b, known := s.byName[d.Backend]; known && s.enabled(d.Backend) {
			upstream := d.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, d.Fallbacks, true
		}
	}
	return route{}, nil, false
}

// maxRouteChain bounds how many backends one request may visit: the primary
// route plus up to three fallbacks.
const maxRouteChain = 4

// resolveChain resolves the primary route and appends the fallbacks that
// apply to it: those on the matched route entry first, then those configured
// on the primary backend itself (so qualified IDs like "opencode/model" can
// fail over too). Fallbacks naming unknown, disabled or repeated backends are
// skipped; an empty model rewrite keeps the primary's upstream model.
func (s *Server) resolveChain(ctx context.Context, model string) ([]route, bool) {
	primary, fallbacks, ok := s.resolveWithFallbacks(ctx, model)
	if !ok {
		return nil, false
	}
	if bc, ok := s.cfg.BackendByType(primary.backend.Name()); ok {
		fallbacks = append(fallbacks, bc.Fallbacks...)
	}
	// A qualified ID pins one backend, so that backend's live catalog is
	// authoritative for what the pin may request. A model the catalog no
	// longer lists must not be forwarded: the upstream's own rejection
	// (e.g. Zen's 401 "Model x is not supported") reads as an auth failure
	// to clients, which retry it instead of failing fast. Drop the primary
	// and let its fallback chain serve the request instead; with none
	// configured the caller answers 404 like any unroutable model. A
	// catalog that cannot be fetched stays fail-open so a broken /models
	// endpoint cannot 404 known-good models.
	if upstream, pinned := qualifiedPin(model, primary.backend.Name()); pinned &&
		s.catalogLacksModel(ctx, primary.backend, upstream) {
		chain := s.appendFallbacks(nil, fallbacks, upstream)
		if len(chain) == 0 {
			return nil, false
		}
		// The pinned backend was skipped by the catalog check rather than
		// failing mid-request, but the request still moved away from it —
		// count the hand-off so fallbacks_total keeps one meaning: requests
		// the primary did not serve.
		s.metrics.fallbacks.WithLabelValues(primary.backend.Name(), chain[0].backend.Name()).Inc()
		return chain, true
	}
	return s.appendFallbacks([]route{primary}, fallbacks, primary.model), true
}

// qualifiedPin reports whether model is a qualified "<backend>/<model>" ID
// that pinned backendName (split at the first slash, mirroring
// resolveWithFallbacks), and returns the upstream model remainder.
func qualifiedPin(model, backendName string) (string, bool) {
	prefix, rest, found := strings.Cut(model, "/")
	if !found || rest == "" || prefix != backendName {
		return "", false
	}
	return rest, true
}

// catalogLacksModel reports whether the backend's live catalog loaded,
// lists anything at all, and does not list model. Fetch errors and empty
// catalogs report false so callers stay fail-open: an unreachable or
// auth-gated /models endpoint (which can legitimately answer an empty list)
// must not 404 models the backend actually serves.
func (s *Server) catalogLacksModel(ctx context.Context, b backend.Backend, model string) bool {
	models, err := s.catalog(ctx, b)
	if err != nil || len(models) == 0 {
		return false
	}
	return !hasModel(models, model)
}

// appendFallbacks appends the fallback entries that can serve the request to
// chain: unknown, disabled and duplicate backends are skipped, the
// maxRouteChain cap holds, and an empty model rewrite keeps the primary's
// upstream model.
func (s *Server) appendFallbacks(chain []route, fallbacks []config.FallbackRoute, primaryModel string) []route {
	for _, f := range fallbacks {
		if len(chain) >= maxRouteChain {
			break
		}
		b, known := s.byName[f.Backend]
		if !known || !s.enabled(f.Backend) {
			continue
		}
		duplicate := false
		for _, rt := range chain {
			if rt.backend.Name() == b.Name() {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		upstream := f.Model
		if upstream == "" {
			upstream = primaryModel
		}
		chain = append(chain, route{backend: b, model: upstream})
	}
	return chain
}

// retryBudget is the retry policy for one backend, resolved from config with
// defaults filled in.
type retryBudget struct {
	attempts   int           // extra connection-phase attempts after a transient failure
	maxBackoff time.Duration // cap on any single retry pause
}

func (s *Server) retryBudgetFor(backendName string) retryBudget {
	b := retryBudget{attempts: defaultRetryAttempts, maxBackoff: defaultRetryMaxBackoff}
	if bc, ok := s.cfg.BackendByType(backendName); ok {
		if bc.RetryAttempts > 0 {
			b.attempts = bc.RetryAttempts
		}
		if bc.RetryMaxBackoff > 0 {
			b.maxBackoff = bc.RetryMaxBackoff
		}
	}
	return b
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
	models    []string
	expires   time.Time
	fetchedAt time.Time
	lastWarn  time.Time
}

const (
	// catalogTTL is how long a fetched model list is considered fresh.
	catalogTTL = time.Minute
	// catalogMaxStaleness is the upper bound on how long a stale catalog
	// entry may be served while a backend's /models endpoint is failing (10x
	// the TTL). Beyond it the refresh error propagates so a permanently
	// broken catalog endpoint does not go unnoticed.
	catalogMaxStaleness = 10 * catalogTTL
	// catalogStaleWarnInterval throttles the "serving stale catalog" warning
	// to one line per backend per interval; without it every request past the
	// TTL logs while the endpoint is failing.
	catalogStaleWarnInterval = 5 * time.Minute
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
			age := now.Sub(cached.fetchedAt)
			if age < catalogMaxStaleness {
				if now.Sub(cached.lastWarn) >= catalogStaleWarnInterval {
					cached.lastWarn = now
					s.catalogs.mu.Lock()
					if existing, still := s.catalogs.entries[b.Name()]; still {
						existing.lastWarn = now
						s.catalogs.entries[b.Name()] = existing
					}
					s.catalogs.mu.Unlock()
					s.log.WithField("backend", b.Name()).
						WithField("age", age.Round(time.Second)).
						Warn("serving stale model catalog; backend /models is failing")
				}
				return cached.models, nil
			}
			s.log.WithField("backend", b.Name()).
				WithField("age", age.Round(time.Second)).
				Warn("stale model catalog exceeds maximum age; propagating backend error")
		}
		return nil, err
	}

	s.catalogs.mu.Lock()
	s.catalogs.entries[b.Name()] = cachedCatalog{
		models:    models,
		expires:   now.Add(catalogJitter(s.catalogs.ttl)),
		fetchedAt: now,
	}
	s.catalogs.mu.Unlock()
	return models, nil
}

// catalogJitter stretches a TTL by up to ±10% so several backends refreshed
// around the same moment do not re-fetch in lockstep forever after.
func catalogJitter(ttl time.Duration) time.Duration {
	spread := ttl / 10
	if spread <= 0 {
		return ttl
	}
	return ttl + time.Duration(rand.Int64N(int64(2*spread)+1)) - spread
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
	s.updates.close()
	return s.stats.Close()
}
