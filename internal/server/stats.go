package server

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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

func newStats(reg *prometheus.Registry) *Stats {
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
	}
	reg.MustRegister(st.requests, st.ttft, st.e2e, st.tokens, st.through, st.calls, st.errs)
	return st
}

// tracker accumulates one upstream request's observations until done().
type tracker struct {
	st       *Stats
	labels   []string // backend, model
	start    time.Time
	firstAt  atomic.Int64 // unix nanos of first upstream byte, 0 = none yet
	status   string
	rep      usageReport
	finished bool
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

// done records everything observed. Idempotent so a plain defer is safe on
// every early-return path.
func (t *tracker) done() {
	if t.finished {
		return
	}
	t.finished = true
	now := time.Now()

	t.st.requests.WithLabelValues(t.labels[0], t.labels[1], t.status).Inc()
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenInput).Add(float64(t.rep.input))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenOutput).Add(float64(t.rep.output))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenCacheRead).Add(float64(t.rep.cacheRead))
	t.st.tokens.WithLabelValues(t.labels[0], t.labels[1], tokenCacheWrit).Add(float64(t.rep.cacheWrite))
	if t.rep.toolCalls > 0 {
		t.st.calls.WithLabelValues(t.labels[0], t.labels[1]).Add(float64(t.rep.toolCalls))
	}

	if t.status == statusError {
		return // no latency distribution for requests without an upstream response
	}
	if first := t.firstAt.Load(); first != 0 {
		t.st.ttft.WithLabelValues(t.labels[0], t.labels[1]).Observe(time.Unix(0, first).Sub(t.start).Seconds())
	}
	t.st.e2e.WithLabelValues(t.labels[0], t.labels[1]).Observe(now.Sub(t.start).Seconds())
	// Generation window runs from the first upstream byte, so long prompts
	// and queueing do not depress measured throughput.
	if first := t.firstAt.Load(); first != 0 && t.rep.output > 0 {
		window := now.Sub(time.Unix(0, first)).Seconds()
		if window > 0.05 {
			t.st.through.WithLabelValues(t.labels[0], t.labels[1]).Observe(float64(t.rep.output) / window)
		}
	}
}

// recordInboundToolErrors attributes errored tool results carried by an
// inbound request body to the resolved backend/model. This is what makes the
// tool-call error rate meaningful: the error surfaces one turn after the
// tool call itself.
func (st *Stats) recordInboundToolErrors(body []byte, backendName, model string) {
	if n := countErroredToolResults(body); n > 0 {
		st.errs.WithLabelValues(backendName, model).Add(float64(n))
	}
}

// countErroredToolResults counts Anthropic tool_result blocks flagged
// is_error in an inbound request body. Only Anthropic-shaped requests carry
// an explicit error flag; OpenAI tool results are plain content.
func countErroredToolResults(body []byte) int {
	var probe struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &probe) != nil {
		return 0
	}
	n := 0
	for _, msg := range probe.Messages {
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
	body    io.ReadCloser
	tracker *tracker
	sse     bool
	buf     bytes.Buffer
	dropped bool
	closed  bool
}

func newSniffer(body io.ReadCloser, tr *tracker, sse bool) *sniffer {
	return &sniffer{body: body, tracker: tr, sse: sse}
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

// Finish parses whatever was retained and folds it into the tracker.
func (sn *sniffer) Finish() {
	data := sn.buf.Bytes()
	if len(data) == 0 {
		return
	}
	sn.tracker.rep.mergeMax(parseUsageReport(data, sn.sse))
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
	Backend          string      `json:"backend"`
	Model            string      `json:"model"`
	Requests         uint64      `json:"requests"`
	Successes        uint64      `json:"successes"`
	Uptime           float64     `json:"uptime"` // successful / total requests
	TTFT             Percentiles `json:"ttft_seconds"`
	E2E              Percentiles `json:"e2e_seconds"`
	Throughput       Percentiles `json:"throughput_tps"`
	InputTokens      uint64      `json:"input_tokens"`
	OutputTokens     uint64      `json:"output_tokens"`
	CacheReadTokens  uint64      `json:"cache_read_tokens"`
	CacheWriteTokens uint64      `json:"cache_write_tokens"`
	CacheRate        float64     `json:"cache_rate"` // cached input / total input
	ToolCalls        uint64      `json:"tool_calls"`
	ToolErrors       uint64      `json:"tool_errors"`
	ToolErrorRate    float64     `json:"tool_error_rate"`
}

// Percentiles carries p50/p90/p99 of one distribution in its source unit.
type Percentiles struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

func (st *Stats) snapshot() []ModelStat {
	type row struct {
		stat               ModelStat
		ttft, e2e, through *dto.Histogram
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
		s.Uptime = ratio(s.Successes, s.Requests)
		s.TTFT = percentilesOf(r.ttft)
		s.E2E = percentilesOf(r.e2e)
		s.Throughput = percentilesOf(r.through)
		s.CacheRate = ratio(s.CacheReadTokens, s.CacheReadTokens+s.InputTokens)
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
	rank := q * float64(h.GetSampleCount())
	if rank <= 0 {
		return 0
	}
	var prev, prevBound float64
	for _, b := range h.GetBucket() {
		count := float64(b.GetCumulativeCount())
		bound := b.GetUpperBound()
		if count >= rank {
			if math.IsInf(bound, 1) {
				return prevBound
			}
			if count == prev {
				return bound
			}
			share := (rank - prev) / (count - prev)
			return prevBound + (bound-prevBound)*share
		}
		prev, prevBound = count, bound
	}
	return h.GetSampleSum() / float64(h.GetSampleCount()) // rank past the last finite bucket
}

// handleStats serves GET /stats with the per-model summary as JSON.
func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": s.stats.snapshot()})
}
