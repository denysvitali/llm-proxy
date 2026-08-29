package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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

	// recentMu protects recent. It is a bounded ring of the latest upstream
	// failures for the dashboard's "recent errors" view; older entries roll
	// off instead of growing without bound.
	recentMu   sync.Mutex
	recent     []UpstreamErrorEvent
	inspected  []InspectedRequest
	requestSeq atomic.Uint64

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

// successStatus reports whether the recorded outcome was a 2xx reply that
// did not carry an error body.
func (t *tracker) successStatus() bool {
	return !t.bodyFail && t.status != statusError && strings.HasPrefix(t.status, "2")
}

// done records everything observed. Idempotent so a plain defer is safe on
// every early-return path.
func (t *tracker) done() {
	if t.finished {
		return
	}
	t.finished = true
	now := time.Now()
	t.request.At = now
	t.request.Status = t.status
	t.request.Error = t.errMsg
	t.st.recordRequest(t.request)

	t.st.requests.WithLabelValues(t.labels[0], t.labels[1], t.status).Inc()
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenInput).Add(float64(t.rep.input))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenOutput).Add(float64(t.rep.output))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenCacheRead).Add(float64(t.rep.cacheRead))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenCacheWrit).Add(float64(t.rep.cacheWrite))
	if t.rep.toolCalls > 0 {
		t.st.calls.WithLabelValues(t.labels[0], t.labels[1]).Add(float64(t.rep.toolCalls))
	}

	success := t.successStatus()
	if !success {
		t.st.recordFailure(t.labels[0], t.labels[1], t.status, t.errMsg, t.request.ID)
	}
	if t.status == statusError || t.bodyFail {
		// Record the request — lifetime counters and current bucket — without
		// a latency distribution and without a success credit: either nothing
		// came back at all, or the 200 carried an error object.
		t.st.record(t.labels[0], t.labels[1], false, 0, 0, 0, t.rep)
		t.st.updates.notify()
		return // no latency distribution for requests without a usable response
	}
	var ttft, e2e, throughput float64
	if first := t.firstAt.Load(); first != 0 {
		ttft = time.Unix(0, first).Sub(t.start).Seconds()
		t.st.ttft.WithLabelValues(t.labels[0], t.labels[1]).Observe(ttft)
		e2e = now.Sub(t.start).Seconds()
		t.st.e2e.WithLabelValues(t.labels[0], t.labels[1]).Observe(e2e)
		// Generation window runs from the first upstream byte, so long prompts
		// and queueing do not depress measured throughput.
		if t.rep.output > 0 {
			window := now.Sub(time.Unix(0, first)).Seconds()
			if window > 0.05 {
				throughput = float64(t.rep.output) / window
				t.st.through.WithLabelValues(t.labels[0], t.labels[1]).Observe(throughput)
			}
		}
	}
	t.st.record(t.labels[0], t.labels[1], success, ttft, e2e, throughput, t.rep)
	t.st.updates.notify()
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
		st.errs.WithLabelValues(backendName, model).Add(float64(n))
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

// ---------------------------------------------------------------------------
// Body sniffing

// sniffCap bounds how much of an upstream body is retained for parsing. The
// client always receives every byte; past the cap only the parsing copy
// stops growing (usage stats are lost for that response).
const sniffCap = 8 << 20

// sniffer forwards upstream bytes verbatim while keeping a bounded copy so
// usage/tool-call stats can be parsed at Finish.
type sniffer struct {
	body          io.ReadCloser
	tracker       *tracker
	sse           bool
	attemptStatus string // HTTP status of the attempt this body belongs to
	buf           bytes.Buffer
	dropped       bool
	closed        bool
}

// newSniffer wraps one attempt's body. The status is frozen at creation:
// several attempts may share a tracker across retries, and a deferred Finish
// must judge the body it held, not whatever status the tracker carries by
// the time Finish runs.
func newSniffer(body io.ReadCloser, tr *tracker, sse bool, status int) *sniffer {
	attemptStatus := statusError
	if status > 0 {
		attemptStatus = strconv.Itoa(status)
	}
	return &sniffer{body: body, tracker: tr, sse: sse, attemptStatus: attemptStatus}
}

func (sn *sniffer) Read(p []byte) (int, error) {
	n, err := sn.body.Read(p)
	if n > 0 {
		sn.tracker.noteFirstByte()
		if !sn.dropped {
			if int64(sn.buf.Len())+int64(n) > sniffCap {
				sn.dropped = true
				sn.buf.Reset()
			} else {
				sn.buf.Write(p[:n])
			}
		}
	}
	return n, err
}

// Close passes through; idempotent because relay helpers and forward paths
// may both defer it.
func (sn *sniffer) Close() error {
	if sn.closed {
		return nil
	}
	sn.closed = true
	return sn.body.Close()
}

// Finish parses whatever was retained and folds it into the tracker. Two
// post-processing rules apply to the retained body:
//
//   - An upstream reply that is not a 2xx records why: its body's error
//     message becomes the tracker's failure summary. Later successful attempts
//     leave earlier messages alone, so a recovered retry keeps the reason of
//     the attempt that failed.
//   - A 2xx reply whose JSON body carries a top-level "error" object (the
//     gateway/quota shape relayNativeBuffered rejects) marks the request as a
//     failure so uptime stays honest.
func (sn *sniffer) Finish() {
	data := sn.buf.Bytes()
	if len(data) == 0 {
		return
	}
	sn.tracker.rep.mergeMax(parseUsageReport(data, sn.sse))
	success := sn.attemptStatus != statusError && strings.HasPrefix(sn.attemptStatus, "2")
	if success && !sn.sse && errorShapedBody(data) != nil {
		// Gateway-200: the exchange will answer 502 for this. Mark the whole
		// request failed so uptime counts it, and keep the body's message.
		sn.tracker.bodyFail = true
		if msg := truncateMessage(upstreamErrorSummary(data)); msg != "" {
			sn.tracker.errMsg = msg
		}
		return
	}
	if !success {
		if msg := truncateMessage(upstreamErrorSummary(data)); msg != "" {
			sn.tracker.errMsg = msg
		}
	}
}

// ---------------------------------------------------------------------------
// Wire parsing

// usageReport is the stat-bearing content extracted from one upstream
// response (or folded over a stream).
type usageReport struct {
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
	toolCalls  int64
}

// mergeMax folds another report in, keeping the high-water mark per token
// field. Providers repeat growing cumulative values across stream events
// (Anthropic message_delta re-sends output totals), so max is both the right
// fold and idempotent for values that arrive once.
func (r *usageReport) mergeMax(o usageReport) {
	r.input = maxI(r.input, o.input)
	r.output = maxI(r.output, o.output)
	r.cacheRead = maxI(r.cacheRead, o.cacheRead)
	r.cacheWrite = maxI(r.cacheWrite, o.cacheWrite)
	r.toolCalls = maxI(r.toolCalls, o.toolCalls)
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// wireUsageFields unions the usage shapes of Anthropic Messages, OpenAI Chat
// Completions, and the OpenAI Responses API.
type wireUsageFields struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	PromptTokens             int64 `json:"prompt_tokens"`
	CompletionTokens         int64 `json:"completion_tokens"`
	PromptTokensDetails      *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// wireTyped is any {type: ...} discriminator: content blocks, Responses
// output items, streamed item announcements.
type wireTyped struct {
	Type string `json:"type"`
}

type wireToolRef struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
}

type wireDelta struct {
	ToolCalls []wireToolRef `json:"tool_calls"`
}

type wireChoice struct {
	Delta   *wireDelta `json:"delta"`   // streaming chunks
	Message *wireDelta `json:"message"` // non-streaming
}

// wireDoc unions the event/response envelopes that can carry usage or tool
// calls across the three dialects. Everything is optional; unknown shapes
// contribute nothing.
type wireDoc struct {
	Type    string           `json:"type"`
	Usage   *wireUsageFields `json:"usage"` // Anthropic root, chat chunk, Responses root
	Message *struct {
		Usage *wireUsageFields `json:"usage"` // Anthropic message_start
	} `json:"message"`
	Response *struct {
		Usage  *wireUsageFields `json:"usage"`
		Output []wireTyped      `json:"output"`
	} `json:"response"`
	Output       []wireTyped  `json:"output"` // Responses non-stream: items sit at the top level
	Choices      []wireChoice `json:"choices"`
	Content      []wireTyped  `json:"content"`       // Anthropic non-stream content blocks
	ContentBlock *wireTyped   `json:"content_block"` // Anthropic content_block_start
	Item         *wireTyped   `json:"item"`          // Responses response.output_item.added
}

// parseUsageReport extracts usage and tool-call counts from a full response
// body, splitting SSE frames when sse is true.
func parseUsageReport(data []byte, sse bool) usageReport {
	var rep usageReport
	seenTools := map[string]bool{}
	for _, doc := range iterWireDocs(data, sse) {
		var u *wireUsageFields
		switch {
		case doc.Usage != nil:
			u = doc.Usage
		case doc.Message != nil:
			u = doc.Message.Usage
		case doc.Response != nil:
			u = doc.Response.Usage
		}
		if u != nil {
			var part usageReport
			part.input = maxI(u.InputTokens, u.PromptTokens)
			part.output = maxI(u.OutputTokens, u.CompletionTokens)
			part.cacheRead = u.CacheReadInputTokens
			part.cacheWrite = u.CacheCreationInputTokens
			if u.PromptTokensDetails != nil {
				part.cacheRead = maxI(part.cacheRead, u.PromptTokensDetails.CachedTokens)
			}
			if u.InputTokensDetails != nil {
				part.cacheRead = maxI(part.cacheRead, u.InputTokensDetails.CachedTokens)
			}
			rep.mergeMax(part)
		}

		// Anthropic stream: each tool_use opens with a content_block_start.
		if doc.ContentBlock != nil && doc.ContentBlock.Type == "tool_use" {
			rep.toolCalls++
		}
		// Anthropic non-stream: tool_use blocks live in content.
		for _, b := range doc.Content {
			if b.Type == "tool_use" {
				rep.toolCalls++
			}
		}
		// Responses stream: each function call announces its output item.
		if doc.Item != nil && doc.Item.Type == "function_call" {
			rep.toolCalls++
		}
		// Responses non-stream: function_call items sit in the output array
		// (top level on the response object, nested in response.completed).
		outputs := doc.Output
		if doc.Response != nil {
			outputs = append(outputs, doc.Response.Output...)
		}
		if !sse {
			for _, item := range outputs {
				if item.Type == "function_call" {
					rep.toolCalls++
				}
			}
		}
		// OpenAI chat: stream deltas fragment tool calls — the index is the
		// stable identity across fragments; non-stream messages list them
		// complete, keyed by id.
		for _, ch := range doc.Choices {
			if ch.Delta != nil {
				for _, tc := range ch.Delta.ToolCalls {
					key := "idx:" + strconv.Itoa(tc.Index)
					if !seenTools[key] {
						seenTools[key] = true
						rep.toolCalls++
					}
				}
			}
			if ch.Message != nil {
				for _, tc := range ch.Message.ToolCalls {
					key := tc.ID
					if key == "" {
						key = "msg:" + strconv.Itoa(len(seenTools))
					}
					if !seenTools[key] {
						seenTools[key] = true
						rep.toolCalls++
					}
				}
			}
		}
	}
	return rep
}

// iterWireDocs yields each JSON document in the body: SSE data payloads when
// sse, otherwise the whole body as one document. Undecodable payloads are
// skipped silently — sniffing must never affect the proxied response.
func iterWireDocs(data []byte, sse bool) []wireDoc {
	if !sse {
		var doc wireDoc
		if err := json.Unmarshal(data, &doc); err != nil || !doc.hasSignal() {
			return nil
		}
		return []wireDoc{doc}
	}
	var docs []wireDoc
	for _, payload := range sseDataPayloads(data) {
		var doc wireDoc
		if json.Unmarshal(payload, &doc) == nil {
			docs = append(docs, doc)
		}
	}
	return docs
}

// hasSignal keeps bare documents (error bodies, unknown envelopes) out of the
// stats.
func (d *wireDoc) hasSignal() bool {
	return d.Usage != nil || d.Message != nil || d.Response != nil ||
		len(d.Choices) > 0 || len(d.Content) > 0 || d.ContentBlock != nil || d.Item != nil
}

// sseDataPayloads splits an SSE byte stream into the concatenated data lines
// of each event.
func sseDataPayloads(data []byte) [][]byte {
	var out [][]byte
	var cur bytes.Buffer
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, append([]byte(nil), bytes.TrimSpace(cur.Bytes())...))
			cur.Reset()
		}
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			flush()
			continue
		}
		if field, value, ok := bytes.Cut(line, []byte(":")); ok && string(field) == "data" {
			cur.Write(bytes.TrimPrefix(value, []byte(" ")))
			cur.WriteByte('\n')
		}
	}
	flush()
	return out
}

// ---------------------------------------------------------------------------
// Aggregation for /stats and the dashboard

// ModelStat is one backend/model row of the stats summary. Latency fields
// are seconds, throughput tokens/second.
type ModelStat struct {
	Backend          string            `json:"backend"`
	Model            string            `json:"model"`
	Requests         uint64            `json:"requests"`
	Successes        uint64            `json:"successes"`
	Uptime           float64           `json:"uptime"` // successful / total requests
	TTFT             Percentiles       `json:"ttft_seconds"`
	E2E              Percentiles       `json:"e2e_seconds"`
	Throughput       Percentiles       `json:"throughput_tps"`
	InputTokens      uint64            `json:"input_tokens"`
	OutputTokens     uint64            `json:"output_tokens"`
	CacheReadTokens  uint64            `json:"cache_read_tokens"`
	CacheWriteTokens uint64            `json:"cache_write_tokens"`
	CacheRate        float64           `json:"cache_rate"` // cached input / total input
	ToolCalls        uint64            `json:"tool_calls"`
	ToolErrors       uint64            `json:"tool_errors"`
	ToolErrorRate    float64           `json:"tool_error_rate"`
	StatusCodes      map[string]uint64 `json:"status_codes,omitempty"` // non-2xx replies by HTTP status
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

// InspectedRequest is one bounded, recent upstream attempt available to the
// admin dashboard. Bodies are retained in memory only and are never persisted.
type InspectedRequest struct {
	ID              string          `json:"id"`
	At              time.Time       `json:"at"`
	ProxyRequestID  string          `json:"proxy_request_id,omitempty"`
	Backend         string          `json:"backend"`
	Model           string          `json:"model"`
	Kind            string          `json:"kind,omitempty"`
	Status          string          `json:"status"`
	Error           string          `json:"error,omitempty"`
	ClientRequest   json.RawMessage `json:"client_request,omitempty"`
	UpstreamRequest json.RawMessage `json:"upstream_request,omitempty"`
}

// maxRecentErrors caps the shared ring of recent upstream failures.
const maxRecentErrors = 50

// recordFailure counts one non-2xx (or transport-failed) request into the
// per-model status counters, its current bucket, Redis, and the recent ring.
func (st *Stats) recordFailure(backend, model, status, message, requestID string) {
	if message == "" && status != "" {
		message = "upstream returned status " + status
	}
	st.statusesCountInMemory(backend, model, status)

	// modelFor creates on first use: a failure can arrive before record() has
	// ever run for this pair (it would otherwise be lost).
	key := backend + "\x00" + model
	ms := st.modelFor(key)
	ms.mu.Lock()
	b := ms.bucketForLocked(time.Now().Unix() / 300)
	b.StatusCodes[status]++
	ms.mu.Unlock()
	st.recordRedisStatus(backend, model, status)

	ev := UpstreamErrorEvent{
		At:        time.Now(),
		Backend:   backend,
		Model:     model,
		Status:    status,
		Message:   message,
		RequestID: requestID,
	}
	st.recentMu.Lock()
	st.recent = append(st.recent, ev)
	if n := len(st.recent); n > maxRecentErrors {
		st.recent = st.recent[n-maxRecentErrors:]
	}
	st.recentMu.Unlock()
}

const maxInspectedRequestBody = 1 << 20

func boundedJSON(body []byte) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	if len(body) > maxInspectedRequestBody {
		return json.RawMessage(strconv.Quote(fmt.Sprintf("request omitted: %d bytes exceeds 1 MiB inspection limit", len(body))))
	}
	return append(json.RawMessage(nil), body...)
}

func (st *Stats) inspect(tr *tracker, proxyID, kind string, clientBody, upstreamBody []byte) {
	seq := st.requestSeq.Add(1)
	tr.request = InspectedRequest{
		ID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), seq), ProxyRequestID: proxyID,
		Backend: tr.labels[0], Model: tr.labels[1], Kind: kind,
		ClientRequest: boundedJSON(clientBody), UpstreamRequest: boundedJSON(upstreamBody),
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
		r := st.inspected[i]
		r.ClientRequest = nil
		r.UpstreamRequest = nil
		out = append(out, r)
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

// statusesCountInMemory bumps the Prometheus status counter.
func (st *Stats) statusesCountInMemory(backend, model, status string) {
	if status == "" {
		status = statusError
	}
	st.statuses.WithLabelValues(backend, model, status).Inc()
}

// recordRedisStatus mirrors a non-2xx count into the shared Redis hash so
// multi-replica dashboards see every replica's failures.
func (st *Stats) recordRedisStatus(backend, model, status string) {
	if st.redis == nil || status == "" || status == statusError {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = st.redis.recordStatus(ctx, backend, model, status, st.cfg.RetentionDays)
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

// Percentiles carries p50/p90/p99 of one distribution in its source unit.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

// snapshot aggregates the per-model summary. With persistence enabled it
// sums all retained buckets ("all recorded history"); otherwise it falls back
// to the Prometheus-backed counters (current behavior, no regression).
func (st *Stats) snapshot() []ModelStat {
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if snapshot, err := st.redis.snapshot(ctx, st.cfg.RetentionDays); err == nil {
			return snapshot
		}
	}
	if st.cfg.PersistFile == "" {
		return st.snapshotFromPrometheus()
	}
	return st.snapshotFromBuckets()
}

func (st *Stats) snapshotFromPrometheus() []ModelStat {
	type row struct {
		stat               ModelStat
		ttft, e2e, through *dto.Histogram
		body200Failures    uint64 // 2xx replies whose body was an error object
	}
	rows := map[string]*row{}

	get := func(backendName, model string) *row {
		key := backendName + "\x00" + model
		r, ok := rows[key]
		if !ok {
			r = &row{}
			r.stat.Backend = backendName
			r.stat.Model = model
			rows[key] = r
		}
		return r
	}

	collectInto := func(vec *prometheus.MetricVec, fn func(labels []string, m *dto.Metric)) {
		ch := make(chan prometheus.Metric, 64)
		go func() {
			vec.Collect(ch)
			close(ch)
		}()
		for m := range ch {
			dm := &dto.Metric{}
			if m.Write(dm) != nil || len(dm.Label) < 2 {
				continue
			}
			labels := make([]string, len(dm.Label))
			for i, l := range dm.Label {
				labels[i] = l.GetValue()
			}
			fn(labels, dm)
		}
	}

	collectInto(st.requests.MetricVec, func(labels []string, m *dto.Metric) {
		v := m.GetCounter().GetValue()
		r := get(labels[0], labels[1])
		r.stat.Requests += uint64(v)
		if strings.HasPrefix(labels[2], "2") {
			r.stat.Successes += uint64(v)
		}
	})
	collectInto(st.tokens.MetricVec, func(labels []string, m *dto.Metric) {
		v := uint64(m.GetCounter().GetValue())
		r := get(labels[0], labels[1])
		switch labels[2] {
		case tokenInput:
			r.stat.InputTokens += v
		case tokenOutput:
			r.stat.OutputTokens += v
		case tokenCacheRead:
			r.stat.CacheReadTokens += v
		case tokenCacheWrit:
			r.stat.CacheWriteTokens += v
		}
	})
	collectInto(st.calls.MetricVec, func(labels []string, m *dto.Metric) {
		get(labels[0], labels[1]).stat.ToolCalls += uint64(m.GetCounter().GetValue())
	})
	collectInto(st.errs.MetricVec, func(labels []string, m *dto.Metric) {
		get(labels[0], labels[1]).stat.ToolErrors += uint64(m.GetCounter().GetValue())
	})
	collectInto(st.statuses.MetricVec, func(labels []string, m *dto.Metric) {
		v := uint64(m.GetCounter().GetValue())
		r := get(labels[0], labels[1])
		if strings.HasPrefix(labels[2], "2") {
			// Body-failures (HTTP 200 carrying an error object) were recorded
			// under the same status label as real successes; net them out of
			// the successes count so uptime stays honest.
			r.body200Failures += v
		}
		if r.stat.StatusCodes == nil {
			r.stat.StatusCodes = map[string]uint64{}
		}
		r.stat.StatusCodes[labels[2]] += uint64(m.GetCounter().GetValue())
	})
	hist := func(vec *prometheus.HistogramVec, dst func(*row) **dto.Histogram) {
		collectInto(vec.MetricVec, func(labels []string, m *dto.Metric) {
			if hm := m.GetHistogram(); hm != nil {
				p := dst(get(labels[0], labels[1]))
				*p = hm
			}
		})
	}
	hist(st.ttft, func(r *row) **dto.Histogram { return &r.ttft })
	hist(st.e2e, func(r *row) **dto.Histogram { return &r.e2e })
	hist(st.through, func(r *row) **dto.Histogram { return &r.through })

	out := make([]ModelStat, 0, len(rows))
	for _, r := range rows {
		s := r.stat
		if s.Successes >= r.body200Failures {
			s.Successes -= r.body200Failures
		} else {
			s.Successes = 0
		}
		s.Uptime = ratio(s.Successes, s.Requests)
		s.TTFT = percentilesOf(r.ttft)
		s.E2E = percentilesOf(r.e2e)
		s.Throughput = percentilesOf(r.through)
		s.CacheRate = ratio(s.CacheReadTokens, s.InputTokens)
		s.ToolErrorRate = ratio(s.ToolErrors, s.ToolCalls)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Backend != out[j].Backend {
			return out[i].Backend < out[j].Backend
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func ratio(num, den uint64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// percentilesOf derives p50/p90/p99 from a Prometheus histogram's cumulative
// buckets; histograms with no samples yield zeros.
func percentilesOf(h *dto.Histogram) Percentiles {
	if h == nil || h.GetSampleCount() == 0 {
		return Percentiles{}
	}
	return Percentiles{
		P50: bucketQuantile(0.50, h),
		P90: bucketQuantile(0.90, h),
		P99: bucketQuantile(0.99, h),
	}
}

// bucketQuantile interpolates q inside the histogram's cumulative buckets,
// mirroring Prometheus's histogram_quantile: upper bound of the bucket where
// the rank falls, linearly interpolated against the previous bound.
func bucketQuantile(q float64, h *dto.Histogram) float64 {
	if h == nil || h.GetSampleCount() == 0 {
		return 0
	}
	buckets := h.GetBucket()
	edges := make([]float64, len(buckets))
	counts := make([]uint64, len(buckets))
	total := h.GetSampleCount()
	for i, b := range buckets {
		edges[i] = b.GetUpperBound()
		counts[i] = b.GetCumulativeCount()
	}
	// If the rank falls beyond the last explicit bucket, the remaining mass
	// sits in the implicit +Inf bucket: fall back to the mean (HEAD behavior).
	if q*float64(total) > float64(counts[len(counts)-1]) {
		return h.GetSampleSum() / float64(total)
	}
	// Prometheus dto buckets are cumulative; histogramQuantile expects
	// per-bucket counts.
	for i := len(counts) - 1; i > 0; i-- {
		counts[i] -= counts[i-1]
	}
	return histogramQuantile(q, edges, counts, h.GetSampleSum())
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

// record folds one completed upstream request into the in-memory model.
// success tells whether the request actually served the client — 2xx without
// an error object — so gateway-200 error bodies never inflate uptime.
func (st *Stats) record(backend, model string, success bool, ttft, e2e, throughput float64, rep usageReport) {
	key := backend + "\x00" + model
	ms := st.modelFor(key)

	ms.mu.Lock()
	ms.requests++
	if success {
		ms.successes++
	}
	ms.tokensIn += uint64(rep.input)
	ms.tokensOut += uint64(rep.output)
	ms.cacheRead += uint64(rep.cacheRead)
	ms.toolCalls += uint64(rep.toolCalls)

	win := time.Now().Unix() / 300
	b := ms.bucketForLocked(win)
	b.Requests++
	if success {
		b.Successes++
	}
	if ttft > 0 {
		b.TTFTBuckets[histIndex(ttftEdges, ttft)]++
	}
	if e2e > 0 {
		b.E2EBuckets[histIndex(e2eEdges, e2e)]++
	}
	if throughput > 0 {
		b.ThroughputBuckets[histIndex(tpsEdges, throughput)]++
	}
	b.TokensIn += uint64(rep.input)
	b.TokensOut += uint64(rep.output)
	b.CacheRead += uint64(rep.cacheRead)
	b.ToolCalls += uint64(rep.toolCalls)
	ms.mu.Unlock()

	st.maybeEvict(ms)
	st.recordRedis(backend, model, success, ttft, e2e, throughput, rep)
	st.updates.notify()
}

func (st *Stats) recordRedis(backend, model string, success bool, ttft, e2e, throughput float64, rep usageReport) {
	if st.redis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = st.redis.record(ctx, backend, model, success, ttft, e2e, throughput, rep, st.cfg.RetentionDays)
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
	ms.toolErrors += uint64(n)
	b := ms.bucketForLocked(time.Now().Unix() / 300)
	b.ToolErrors += uint64(n)
	ms.mu.Unlock()
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = st.redis.recordToolErrors(ctx, backend, model, n, st.cfg.RetentionDays)
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
	ms = &modelStats{buckets: map[int64]*bucket{}}
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

func (st *Stats) maybeEvict(ms *modelStats) {
	if st.cfg.RetentionDays <= 0 {
		return
	}
	nowWin := time.Now().Unix() / 300
	if ms.lastEvict == nowWin {
		return
	}
	ms.lastEvict = nowWin
	cutoff := nowWin - int64(st.cfg.RetentionDays)*24*12
	for win := range ms.buckets {
		if win < cutoff {
			delete(ms.buckets, win)
		}
	}
}

func (st *Stats) startPersist() {
	st.stopCh = make(chan struct{})
	ticker := time.NewTicker(st.cfg.PersistInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				st.persist()
			case <-st.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Close stops the background persistence goroutine and flushes once.
func (st *Stats) Close() error {
	if st.stopCh != nil {
		st.stopOnce.Do(func() {
			close(st.stopCh)
		})
		st.persist()
	}
	if st.redis != nil {
		st.redis.close()
	}
	return nil
}

// snapshotForPersist builds the versioned on-disk snapshot. Callers must not
// mutate the returned value.
type statsSnapshot struct {
	Version int                      `json:"version"`
	SavedAt time.Time                `json:"saved_at"`
	Models  map[string]modelSnapshot `json:"models"`
}

type modelSnapshot struct {
	Requests   uint64            `json:"requests"`
	Successes  uint64            `json:"successes"`
	TokensIn   uint64            `json:"tokens_in"`
	TokensOut  uint64            `json:"tokens_out"`
	CacheRead  uint64            `json:"cache_read"`
	ToolCalls  uint64            `json:"tool_calls"`
	ToolErrors uint64            `json:"tool_errors"`
	Statuses   map[string]uint64 `json:"status_codes,omitempty"`
	Buckets    []bucket          `json:"buckets"`
}

func (st *Stats) snapshotForPersist() *statsSnapshot {
	snap := &statsSnapshot{
		Version: 1,
		SavedAt: time.Now().UTC(),
		Models:  make(map[string]modelSnapshot),
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	for key, ms := range st.models {
		ms.mu.Lock()
		buckets := make([]bucket, 0, len(ms.buckets))
		for _, b := range ms.buckets {
			buckets = append(buckets, *b)
		}
		ms.mu.Unlock()
		lifetimeStatuses := map[string]uint64{}
		for _, b := range ms.buckets {
			for status, n := range b.StatusCodes {
				if n > 0 {
					lifetimeStatuses[status] += n
				}
			}
		}
		snap.Models[key] = modelSnapshot{
			Requests:   ms.requests,
			Successes:  ms.successes,
			TokensIn:   ms.tokensIn,
			TokensOut:  ms.tokensOut,
			CacheRead:  ms.cacheRead,
			ToolCalls:  ms.toolCalls,
			ToolErrors: ms.toolErrors,
			Statuses:   nonEmpty(lifetimeStatuses),
			Buckets:    buckets,
		}
	}
	return snap
}

// persist flushes the in-memory model to PersistFile atomically (write temp in
// the same directory, then rename), mirroring auth.Store's pattern.
func (st *Stats) persist() {
	snap := st.snapshotForPersist()
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return
	}
	path := st.cfg.PersistFile
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".stats-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return
	}
}

// load restores a previously saved snapshot into st, dropping expired buckets.
// Cumulative counters are set (not added to): the in-memory model starts from
// the snapshot's values.
func (st *Stats) load(path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap statsSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	cutoff := int64(0)
	if st.cfg.RetentionDays > 0 {
		cutoff = time.Now().Unix()/300 - int64(st.cfg.RetentionDays)*24*12
	}
	for key, ms := range snap.Models {
		m := &modelStats{
			requests:   ms.Requests,
			successes:  ms.Successes,
			tokensIn:   ms.TokensIn,
			tokensOut:  ms.TokensOut,
			cacheRead:  ms.CacheRead,
			toolCalls:  ms.ToolCalls,
			toolErrors: ms.ToolErrors,
			buckets:    make(map[int64]*bucket, len(ms.Buckets)),
		}
		for _, b := range ms.Buckets {
			win := b.WindowStart.Unix() / 300
			if cutoff > 0 && win < cutoff {
				continue
			}
			// Re-home the histogram buckets to the current edge counts in case
			// the edges changed between snapshot versions.
			b.TTFTBuckets = msBucketsOf(b.TTFTBuckets, ttftEdges)
			b.E2EBuckets = msBucketsOf(b.E2EBuckets, e2eEdges)
			b.ThroughputBuckets = msBucketsOf(b.ThroughputBuckets, tpsEdges)
			if b.StatusCodes == nil {
				b.StatusCodes = map[string]uint64{}
			}
			m.buckets[win] = &b
		}
		st.models[key] = m
	}
	return nil
}

// msBucketsOf returns src sliced to the number of edges, zero-padded if src
// is shorter (tolerates snapshots taken with a different edge set).
func msBucketsOf(src []uint64, edges []float64) []uint64 {
	n := len(edges)
	if len(src) >= n {
		return src[:n]
	}
	out := make([]uint64, n)
	copy(out, src)
	return out
}

// histogramQuantile computes q from per-bucket counts (NOT cumulative) and
// fixed edges, linearly interpolating inside the rank's bucket the way
// Prometheus's histogram_quantile does. If the rank falls beyond the last
// explicit edge, the remaining mass sits in the implicit +Inf bucket and the
// mean is returned instead of 0 (sum must be supplied for that case).
func histogramQuantile(q float64, edges []float64, counts []uint64, sum float64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	rank := q * float64(total)
	if rank <= 0 {
		return 0
	}
	var prev, prevBound float64
	for i, c := range counts {
		cum := prev + float64(c)
		bound := edges[i]
		if cum >= rank {
			if math.IsInf(bound, 1) {
				return prevBound
			}
			if cum == prev {
				return bound
			}
			share := (rank - prev) / (cum - prev)
			return prevBound + (bound-prevBound)*share
		}
		prev = cum
		prevBound = bound
	}
	// Rank sits in the implicit +Inf bucket; fall back to the mean.
	if total > 0 {
		return sum / float64(total)
	}
	return 0
}

// percentilesFromCounts derives p50/p90/p99 from per-bucket counts.
func percentilesFromCounts(counts []uint64, edges []float64) Percentiles {
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return Percentiles{}
	}
	return Percentiles{
		P50: histogramQuantile(0.50, edges, counts, 0),
		P90: histogramQuantile(0.90, edges, counts, 0),
		P99: histogramQuantile(0.99, edges, counts, 0),
	}
}

func (st *Stats) snapshotFromBuckets() []ModelStat {
	type row struct {
		stat           ModelStat
		ttft, e2e, tps []uint64
		statuses       map[string]uint64
	}
	rows := map[string]*row{}
	st.mu.RLock()
	defer st.mu.RUnlock()
	for key, ms := range st.models {
		backend, model, ok := strings.Cut(key, "\x00")
		if !ok {
			continue
		}
		r := &row{
			stat:     ModelStat{Backend: backend, Model: model},
			ttft:     make([]uint64, len(ttftEdges)),
			e2e:      make([]uint64, len(e2eEdges)),
			tps:      make([]uint64, len(tpsEdges)),
			statuses: map[string]uint64{},
		}
		ms.mu.Lock()
		// Lifetime counters already cover every event the buckets hold —
		// record() folds each request into both — so the summary reads them
		// directly. Buckets contribute only the latency/throughput histograms
		// (they are not kept as lifetime fields); adding their counters too
		// would double every total.
		r.stat.Requests = ms.requests
		r.stat.Successes = ms.successes
		r.stat.InputTokens = ms.tokensIn
		r.stat.OutputTokens = ms.tokensOut
		r.stat.CacheReadTokens = ms.cacheRead
		r.stat.ToolCalls = ms.toolCalls
		r.stat.ToolErrors = ms.toolErrors
		for _, b := range ms.buckets {
			for i := range b.TTFTBuckets {
				r.ttft[i] += b.TTFTBuckets[i]
			}
			for i := range b.E2EBuckets {
				r.e2e[i] += b.E2EBuckets[i]
			}
			for i := range b.ThroughputBuckets {
				r.tps[i] += b.ThroughputBuckets[i]
			}
			for status, n := range b.StatusCodes {
				if n > 0 {
					r.statuses[status] += n
				}
			}
		}
		ms.mu.Unlock()
		rows[key] = r
	}
	out := make([]ModelStat, 0, len(rows))
	for _, r := range rows {
		s := r.stat
		s.Uptime = ratio(s.Successes, s.Requests)
		s.TTFT = percentilesFromCounts(r.ttft, ttftEdges)
		s.E2E = percentilesFromCounts(r.e2e, e2eEdges)
		s.Throughput = percentilesFromCounts(r.tps, tpsEdges)
		s.CacheRate = ratio(s.CacheReadTokens, s.InputTokens)
		s.ToolErrorRate = ratio(s.ToolErrors, s.ToolCalls)
		s.StatusCodes = nonEmpty(r.statuses)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Backend != out[j].Backend {
			return out[i].Backend < out[j].Backend
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// nonEmpty returns the map as-is when it holds entries, nil otherwise, so
// JSON payloads omit rather than show "{}" for healthy models.
func nonEmpty(m map[string]uint64) map[string]uint64 {
	if len(m) == 0 {
		return nil
	}
	return m
}

// ---------------------------------------------------------------------------
// Time-series API

// point is one timestamped value in a time series.
type point struct {
	TS    string  `json:"ts"`
	Value float64 `json:"value"`
}

// seriesSet holds the fleet-wide series returned by GET /api/stats.
type seriesSet struct {
	Requests      []point `json:"requests"`
	SuccessRate   []point `json:"success_rate"`
	TTFTP50       []point `json:"ttft_p50"`
	E2EP50        []point `json:"e2e_p50"`
	ThroughputP50 []point `json:"throughput_p50"`
	TokensIn      []point `json:"tokens_in"`
	TokensOut     []point `json:"tokens_out"`
	ToolCalls     []point `json:"tool_calls"`
	ToolErrors    []point `json:"tool_errors"`
}

// scopedSeriesSet carries the same fleet-wide series, restricted to one
// optional backend (and optionally model). An empty Model selects every model
// in the backend.
type scopedSeriesSet struct {
	Series seriesSet `json:"series"`
	Models []string  `json:"models"`
}

var seriesRangeBuckets = map[string]time.Duration{
	"1h":  3 * time.Minute,
	"6h":  15 * time.Minute,
	"24h": time.Hour,
	"7d":  6 * time.Hour,
}

var seriesRangePoints = map[string]int{
	"1h":  20,
	"6h":  24,
	"24h": 24,
	"7d":  28,
}

// seriesAt aggregates the in-memory buckets into fleet-wide series for the
// given range, as of now. The current 5-minute bucket is always included,
// even when it began before the requested range. An unknown range returns an
// error.
func (st *Stats) seriesAt(rng string, now time.Time) (seriesSet, []string, error) {
	series, models, err := st.seriesAtScope(rng, now, "", "")
	if err != nil {
		return seriesSet{}, nil, err
	}
	return series.Series, models, nil
}

// seriesAtScope aggregates the same buckets as seriesAt while filtering rows
// by backend and model. Empty selectors mean "all"; this is the shared core
// for fleet, provider, and model histories.
func (st *Stats) seriesAtScope(rng string, now time.Time, backendName, modelName string) (scopedSeriesSet, []string, error) {
	if st.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if series, models, err := st.seriesAtScopeRedis(ctx, rng, now, backendName, modelName); err == nil {
			return series, models, nil
		}
	}
	dur, ok := seriesRangeBuckets[rng]
	if !ok {
		return scopedSeriesSet{}, nil, fmt.Errorf("unknown range %q; supported ranges: 1h, 6h, 24h, 7d", rng)
	}
	n := seriesRangePoints[rng]
	current := now.Truncate(5 * time.Minute)
	end := current.Add(dur)
	start := end.Add(-time.Duration(n) * dur)

	type agg struct {
		requests, successes, tokensIn, tokensOut, cacheRead, toolCalls uint64
		toolErrors                                                     uint64
		ttft, e2e, tps                                                 []uint64
	}
	aggs := make([]*agg, n)
	for i := range aggs {
		aggs[i] = &agg{
			ttft: make([]uint64, len(ttftEdges)),
			e2e:  make([]uint64, len(e2eEdges)),
			tps:  make([]uint64, len(tpsEdges)),
		}
	}
	modelSet := make(map[string]struct{})

	st.mu.RLock()
	defer st.mu.RUnlock()
	for key, ms := range st.models {
		rowBackend, rowModel, ok := strings.Cut(key, "\x00")
		if !ok {
			continue
		}
		if (backendName != "" && rowBackend != backendName) ||
			(modelName != "" && rowModel != modelName) {
			continue
		}
		ms.mu.Lock()
		for win, b := range ms.buckets {
			bStart := time.Unix(win*300, 0).UTC()
			if bStart.Before(start) || !bStart.Before(end) {
				continue
			}
			idx := int(bStart.Sub(start) / dur)
			if idx < 0 || idx >= n {
				continue
			}
			a := aggs[idx]
			a.requests += b.Requests
			a.successes += b.Successes
			a.tokensIn += b.TokensIn
			a.tokensOut += b.TokensOut
			a.cacheRead += b.CacheRead
			a.toolCalls += b.ToolCalls
			a.toolErrors += b.ToolErrors
			for i := range b.TTFTBuckets {
				a.ttft[i] += b.TTFTBuckets[i]
			}
			for i := range b.E2EBuckets {
				a.e2e[i] += b.E2EBuckets[i]
			}
			for i := range b.ThroughputBuckets {
				a.tps[i] += b.ThroughputBuckets[i]
			}
		}
		if len(ms.buckets) > 0 {
			modelSet[rowModel] = struct{}{}
		}
		ms.mu.Unlock()
	}

	scopedModels := make([]string, 0, len(modelSet))
	for model := range modelSet {
		scopedModels = append(scopedModels, model)
	}
	sort.Strings(scopedModels)

	series := seriesSet{
		Requests:      make([]point, 0, n),
		SuccessRate:   make([]point, 0, n),
		TTFTP50:       make([]point, 0, n),
		E2EP50:        make([]point, 0, n),
		ThroughputP50: make([]point, 0, n),
		TokensIn:      make([]point, 0, n),
		TokensOut:     make([]point, 0, n),
		ToolCalls:     make([]point, 0, n),
		ToolErrors:    make([]point, 0, n),
	}
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(i) * dur).Format(time.RFC3339)
		a := aggs[i]
		series.Requests = append(series.Requests, point{TS: ts, Value: float64(a.requests)})
		series.SuccessRate = append(series.SuccessRate, point{TS: ts, Value: ratio(a.successes, a.requests)})
		series.TokensIn = append(series.TokensIn, point{TS: ts, Value: float64(a.tokensIn)})
		series.TokensOut = append(series.TokensOut, point{TS: ts, Value: float64(a.tokensOut)})
		series.ToolCalls = append(series.ToolCalls, point{TS: ts, Value: float64(a.toolCalls)})
		series.ToolErrors = append(series.ToolErrors, point{TS: ts, Value: float64(a.toolErrors)})
		series.TTFTP50 = append(series.TTFTP50, point{TS: ts, Value: histogramQuantile(0.5, ttftEdges, a.ttft, 0)})
		series.E2EP50 = append(series.E2EP50, point{TS: ts, Value: histogramQuantile(0.5, e2eEdges, a.e2e, 0)})
		series.ThroughputP50 = append(series.ThroughputP50, point{TS: ts, Value: histogramQuantile(0.5, tpsEdges, a.tps, 0)})
	}
	return scopedSeriesSet{Series: series, Models: scopedModels}, scopedModels, nil
}

// handleStats serves GET /stats with the per-model summary as JSON. With
// persistence enabled this aggregates all retained buckets ("all recorded
// history"); otherwise it falls back to the Prometheus-backed counters.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": s.stats.snapshot()})
}

// handleStatsErrors serves GET /api/stats/errors with the most recent
// upstream failures (newest first), for the dashboard's error feed.
func (s *Server) handleStatsErrors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"errors": s.stats.RecentUpstreamErrors()})
}

func (s *Server) handleRequests(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"requests": s.stats.RecentRequests()})
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	req, ok := s.stats.Request(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// handleStatsBackendSeries serves GET /api/stats/backends/{backend}?range=...
// with that provider's aggregate history. Omitting model returns provider-wide
// data; supplying it narrows to a single upstream model.
func (s *Server) handleStatsBackendSeries(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "missing required query parameter \"range\"; supported values: 1h, 6h, 24h, 7d",
		})
		return
	}
	backendName := r.PathValue("backend")
	result, _, err := s.stats.seriesAtScope(rng, time.Now(), backendName, r.PathValue("model"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleStatsSeries serves GET /api/stats?range=1h|6h|24h|7d with fleet-wide
// time series. Unknown or missing ranges answer with 400 JSON.
func (s *Server) handleStatsSeries(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing required query parameter \"range\"; supported values: 1h, 6h, 24h, 7d"})
		return
	}
	series, models, err := s.stats.seriesAt(rng, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "series": series})
}
