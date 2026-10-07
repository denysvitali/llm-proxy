package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Stats records per-backend, per-model traffic so the proxy can answer the
// OpenRouter-style questions — is this model up, how fast, how often do its
// tool calls fail — from either Prometheus (/metrics) or the JSON summary
// (/stats) and dashboard.
//
// All collectors register on the shared proxy registry owned by Metrics.
type Stats struct {
	requests *prometheus.CounterVec   // {backend,model,status}
	ttft     *prometheus.HistogramVec // {backend,model}: send -> first upstream byte
	e2e      *prometheus.HistogramVec // {backend,model}: send -> response fully delivered
	tokens   *prometheus.CounterVec   // {backend,model,type=input|output|cache_read|cache_write}
	through  *prometheus.HistogramVec // {backend,model}: output tokens/sec of the generation window
	calls    *prometheus.CounterVec   // {backend,model}: tool calls observed in upstream responses
	errs     *prometheus.CounterVec   // {backend,model}: errored tool results seen in inbound requests
	statuses *prometheus.CounterVec   // {backend,model,status}: non-2xx upstream replies, status label = code or "error"

	updates *updateHub

	mu     sync.RWMutex // protects models
	models map[string]*modelStats
	cfg    config.StatsConfig

	// recentMu protects the local failure and inspection rings. It retains
	// the latest upstream
	// failures for the dashboard's "recent errors" view; older entries roll
	// off instead of growing without bound.
	recentMu  sync.Mutex
	recent    []UpstreamErrorEvent
	inspected []InspectedRequest

	stopCh   chan struct{}
	stopOnce sync.Once

	redis        *redisStats
	redisInitErr error
}

// Token-type label values for llm_proxy_model_tokens_total.
const (
	tokenInput     = "input"
	tokenOutput    = "output"
	tokenCacheRead = "cache_read"
	tokenCacheWrit = "cache_write"
)

// statusError labels requests that never reached an upstream HTTP response.
const statusError = "error"

func newStats(reg *prometheus.Registry, cfg config.StatsConfig) *Stats {
	if cfg.RedisURL != "" {
		// Redis is the shared source of truth. Keeping a local JSON writer active
		// would make the file a competing, incomplete history in an HA rollout.
		cfg.PersistFile = ""
	}
	st := &Stats{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_model_requests_total",
			Help: "Upstream model requests, by backend, model, and upstream HTTP status ('error' for transport failures).",
		}, []string{"backend", "model", "status"}),
		ttft: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "llm_proxy_model_ttft_seconds",
			Help:    "Time from dispatch to the first upstream response byte.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 17), // 1ms .. ~65s
		}, []string{"backend", "model"}),
		e2e: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "llm_proxy_model_e2e_seconds",
			Help:    "End-to-end request duration, dispatch to last byte delivered.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 19), // 10ms .. ~73min
		}, []string{"backend", "model"}),
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_model_tokens_total",
			Help: "Tokens reported by upstream usage, by kind.",
		}, []string{"backend", "model", "type"}),
		through: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "llm_proxy_model_output_tokens_per_second",
			Help:    "Output-token throughput per completion (output tokens over the generation window).",
			Buckets: []float64{1, 2, 5, 10, 20, 30, 50, 75, 100, 150, 200, 300, 500},
		}, []string{"backend", "model"}),
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_model_tool_calls_total",
			Help: "Tool calls observed in upstream model responses.",
		}, []string{"backend", "model"}),
		errs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_model_tool_errors_total",
			Help: "Errored tool results (is_error) seen in later inbound requests.",
		}, []string{"backend", "model"}),
		statuses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_proxy_model_upstream_status_total",
			Help: "Non-2xx upstream replies by HTTP status; 'error' labels transport failures without an upstream status.",
		}, []string{"backend", "model", "status"}),
		models:  make(map[string]*modelStats),
		cfg:     cfg,
		updates: newUpdateHub(),
	}
	if cfg.RedisURL != "" {
		st.redis, st.redisInitErr = newRedisStats(cfg.RedisURL, cfg.RedisKeyPrefix)
	}
	reg.MustRegister(st.requests, st.ttft, st.e2e, st.tokens, st.through, st.calls, st.errs, st.statuses)
	if st.cfg.PersistFile != "" && st.cfg.PersistInterval > 0 {
		st.startPersist()
	}
	return st
}

// tracker accumulates one upstream request's observations until done().
type tracker struct {
	st       *Stats
	labels   []string // backend, model
	start    time.Time
	firstAt  atomic.Int64 // unix nanos of first upstream byte, 0 = none yet
	status   string
	errMsg   string // bounded summary of the upstream failure when status != 2xx
	bodyFail bool   // a 2xx reply turned out to carry an error object
	rep      usageReport
	finished bool
	request  InspectedRequest
}

// markBodyFailure flags an HTTP-success reply whose body is an error object
// (fronting gateways and quota-limited providers answer that way). The
// exchange still failed — the client gets an error — so stats must not count
// it as a success.
func (t *tracker) markBodyFailure() {
	t.bodyFail = true
}

// track starts recording a dispatched upstream request.
func (st *Stats) track(backendName, model string) *tracker {
	return &tracker{
		st:     st,
		labels: []string{backendName, model},
		start:  time.Now(),
		status: statusError,
	}
}

// noteUpstreamError records why a non-2xx upstream reply (or transport
// failure) happened. The message is trimmed to an upstream error summary so
// the dashboard can show the provider's own words.
func (t *tracker) noteUpstreamError(body []byte) {
	t.errMsg = truncateMessage(upstreamErrorSummary(body))
}

// noteTransportError records a failure that never produced an upstream HTTP
// response (dial error, timeout, context cancellation mid-dispatch).
func (t *tracker) noteTransportError(err error) {
	if err == nil {
		return
	}
	t.errMsg = truncateMessage(fmt.Sprintf("%v", err))
}

// noteFirstByte stamps the TTFT moment; effective only for the first byte.
func (t *tracker) noteFirstByte() {
	t.firstAt.CompareAndSwap(0, time.Now().UnixNano())
}

// setUpstreamStatus records the upstream HTTP status as the outcome.
func (t *tracker) setUpstreamStatus(status int) {
	if status != 0 {
		t.status = strconv.Itoa(status)
	}
}

// done freezes the observations once, so every storage adapter sees the same
// completion timestamp, outcome, usage, and latency values.
func (t *tracker) done() {
	if t.finished {
		return
	}
	t.finished = true
	a := completedAttempt{
		at: time.Now(), backend: t.labels[0], model: t.labels[1],
		status: t.status, success: !t.bodyFail && strings.HasPrefix(t.status, "2"),
		message: t.errMsg, usage: t.rep, request: t.request,
	}
	if first := t.firstAt.Load(); first != 0 && t.status != statusError && !t.bodyFail {
		a.ttft = time.Unix(0, first).Sub(t.start).Seconds()
		a.e2e = a.at.Sub(t.start).Seconds()
		// Exclude prompt processing and queueing from the generation window.
		if window := a.at.Sub(time.Unix(0, first)).Seconds(); t.rep.output > 0 && window > 0.05 {
			a.throughput = float64(t.rep.output) / window
		}
	}
	t.st.recordAttempt(a)
}

// recordInboundToolErrors attributes errored tool results carried by an
// inbound request body to the resolved backend/model. This is what makes the
// tool-call error rate meaningful: the error surfaces one turn after the
// tool call itself. Only blocks in the LAST message are counted: agent loops
// replay the full history each turn, so earlier messages are re-sends whose
// errors were already counted on the turn they first appeared. Counting the
// whole body would multiply one real error by the number of subsequent turns
// (3 errors replayed over 10 turns → a "3000%" error rate).
func (st *Stats) recordInboundToolErrors(body []byte, backendName, model string) {
	if n := countErroredToolResults(body, true); n > 0 {
		st.recordToolErrors(backendName, model, int64(n))
	}
}

// countErroredToolResults counts Anthropic tool_result blocks flagged
// is_error in an inbound request body. Only Anthropic-shaped requests carry
// an explicit error flag; OpenAI tool results are plain content. With
// lastMessageOnly, only the final message is scanned — inbound agent-loop
// requests replay prior turns, so scanning everything would recount errors
// that already surfaced.
func countErroredToolResults(body []byte, lastMessageOnly bool) int {
	var probe struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &probe) != nil {
		return 0
	}
	messages := probe.Messages
	if lastMessageOnly && len(messages) > 0 {
		messages = messages[len(messages)-1:]
	}
	n := 0
	for _, msg := range messages {
		var blocks []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
		}
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue // string content carries no tool results
		}
		for _, b := range blocks {
			if b.Type == "tool_result" && b.IsError {
				n++
			}
		}
	}
	return n
}

// UpstreamErrorEvent is one recent upstream failure for the dashboard's error
// feed: which backend/model failed, with what status and what the upstream
// (or transport) said about it.
type UpstreamErrorEvent struct {
	At        time.Time `json:"at"`
	Backend   string    `json:"backend"`
	Model     string    `json:"model"`
	Status    string    `json:"status"` // HTTP code as text; "error" = no response at all
	Message   string    `json:"message,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}

// InspectedRequest is one recent upstream attempt available to the admin
// dashboard. Request bodies are deliberately not retained: prompts, tool
// inputs, credentials, and other client data must not become dashboard data.
type InspectedRequest struct {
	ID             string    `json:"id"`
	At             time.Time `json:"at"`
	ProxyRequestID string    `json:"proxy_request_id,omitempty"`
	Backend        string    `json:"backend"`
	Model          string    `json:"model"`
	Kind           string    `json:"kind,omitempty"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
}

// maxRecentErrors caps each recent failure or inspection feed.
const maxRecentErrors = 50

func (st *Stats) inspect(tr *tracker, proxyID, kind string) {
	tr.request = InspectedRequest{
		ID: rand.Text(), ProxyRequestID: proxyID,
		Backend: tr.labels[0], Model: tr.labels[1], Kind: kind,
	}
}

func (st *Stats) recordRequest(req InspectedRequest) {
	if req.ID == "" {
		return
	}
	st.recentMu.Lock()
	defer st.recentMu.Unlock()
	st.inspected = append(st.inspected, req)
	if n := len(st.inspected); n > maxRecentErrors {
		st.inspected = st.inspected[n-maxRecentErrors:]
	}
}

func (st *Stats) RecentRequests() []InspectedRequest {
	st.recentMu.Lock()
	defer st.recentMu.Unlock()
	out := make([]InspectedRequest, 0, len(st.inspected))
	for i := len(st.inspected) - 1; i >= 0; i-- {
		out = append(out, st.inspected[i])
	}
	return out
}

func (st *Stats) Request(id string) (InspectedRequest, bool) {
	st.recentMu.Lock()
	defer st.recentMu.Unlock()
	for i := len(st.inspected) - 1; i >= 0; i-- {
		if st.inspected[i].ID == id {
			return st.inspected[i], true
		}
	}
	return InspectedRequest{}, false
}

// RecentUpstreamErrors returns a copy of the newest-first recent failure ring.
func (st *Stats) RecentUpstreamErrors() []UpstreamErrorEvent {
	st.recentMu.Lock()
	defer st.recentMu.Unlock()
	out := make([]UpstreamErrorEvent, 0, len(st.recent))
	for i := len(st.recent) - 1; i >= 0; i-- {
		out = append(out, st.recent[i])
	}
	return out
}

// ---------------------------------------------------------------------------
// Time-bucketed in-memory model (persistence-backed)

// ttftEdges and tpsEdges are the fixed histogram edges used by the
// time-bucketed latency / throughput distributions. e2e reuses the ttft
// edges (same latency distribution, different measurement window).
var ttftEdges = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 60, math.Inf(1)}
var e2eEdges = ttftEdges
var tpsEdges = []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, math.Inf(1)}

// bucket holds the counters and fixed-edge histograms for one 5-minute window.
type bucket struct {
	WindowStart       time.Time         `json:"window_start"`
	Requests          uint64            `json:"requests"`
	Successes         uint64            `json:"successes"`
	TTFTBuckets       []uint64          `json:"ttft_buckets"`
	E2EBuckets        []uint64          `json:"e2e_buckets"`
	ThroughputBuckets []uint64          `json:"throughput_buckets"`
	TokensIn          uint64            `json:"tokens_in"`
	TokensOut         uint64            `json:"tokens_out"`
	CacheRead         uint64            `json:"cache_read"`
	CacheWrite        uint64            `json:"cache_write"`
	ToolCalls         uint64            `json:"tool_calls"`
	ToolErrors        uint64            `json:"tool_errors"`
	StatusCodes       map[string]uint64 `json:"status_codes,omitempty"` // non-2xx replies by HTTP status
}

// modelStats holds the cumulative counters and 5-minute bucket ring for one
// (backend, model) pair. The key is backend\x00model, matching the registry.
type modelStats struct {
	mu         sync.Mutex
	requests   uint64
	successes  uint64
	tokensIn   uint64
	tokensOut  uint64
	cacheRead  uint64
	cacheWrite uint64
	statuses   map[string]uint64
	toolCalls  uint64
	toolErrors uint64
	buckets    map[int64]*bucket
	lastEvict  int64 // last window index that ran retention eviction
}

// histIndex returns the histogram bucket index for v given ascending edges
// ending in +Inf.
func histIndex(edges []float64, v float64) int {
	for i, e := range edges {
		if v <= e {
			return i
		}
	}
	return len(edges) - 1
}

// recordToolErrors attributes errored tool results to the current bucket of
// the given backend/model (the turn in which the error surfaces).
func (st *Stats) recordToolErrors(backend, model string, n int64) {
	if n <= 0 {
		return
	}
	key := backend + "\x00" + model
	ms := st.modelFor(key)
	ms.mu.Lock()
	st.errs.WithLabelValues(backend, model).Add(float64(n))
	now := time.Now()
	ms.toolErrors += uint64(n)
	b := ms.bucketForLocked(now.Unix() / 300)
	b.ToolErrors += uint64(n)
	st.evictLocked(ms, now)
	ms.mu.Unlock()
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = st.redis.recordToolErrors(ctx, backend, model, n, now, st.cfg.RetentionDays)
		cancel()
	}
	st.updates.notify()
}

func (st *Stats) modelFor(key string) *modelStats {
	st.mu.RLock()
	ms, ok := st.models[key]
	st.mu.RUnlock()
	if ok {
		return ms
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if ms, ok = st.models[key]; ok {
		return ms
	}
	ms = &modelStats{buckets: map[int64]*bucket{}, statuses: map[string]uint64{}}
	st.models[key] = ms
	return ms
}

func (ms *modelStats) bucketForLocked(win int64) *bucket {
	b, ok := ms.buckets[win]
	if !ok {
		b = &bucket{
			WindowStart:       time.Unix(win*300, 0).UTC(),
			TTFTBuckets:       make([]uint64, len(ttftEdges)),
			E2EBuckets:        make([]uint64, len(e2eEdges)),
			ThroughputBuckets: make([]uint64, len(tpsEdges)),
			StatusCodes:       map[string]uint64{},
		}
		ms.buckets[win] = b
	}
	if b.StatusCodes == nil { // snapshots from older versions may lack the map
		b.StatusCodes = map[string]uint64{}
	}
	return b
}

// evictLocked must be called with ms.mu held. Lifetime counters survive eviction.
func (st *Stats) evictLocked(ms *modelStats, now time.Time) {
	if st.cfg.RetentionDays <= 0 {
		return
	}
	nowWin := now.Unix() / 300
	if ms.lastEvict == nowWin {
		return
	}
	ms.lastEvict = nowWin
	cutoff := retentionCutoff(now, st.cfg.RetentionDays)
	for win := range ms.buckets {
		if win < cutoff {
			delete(ms.buckets, win)
		}
	}
}
