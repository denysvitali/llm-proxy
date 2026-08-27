package server

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// ---------------------------------------------------------------------------
// Wire parsing

func TestParseUsageReportAnthropicStream(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":40,"cache_creation_input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Rome\"}"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":17}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	rep := parseUsageReport([]byte(sse), true)
	if rep.input != 100 || rep.output != 17 || rep.cacheRead != 40 || rep.cacheWrite != 5 {
		t.Fatalf("usage = %+v", rep)
	}
	if rep.toolCalls != 1 {
		t.Fatalf("toolCalls = %d, want 1", rep.toolCalls)
	}
}

func TestParseUsageReportOpenAIChatStream(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c1","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"f","arguments":""}}]}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`,
		``,
		`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"g","arguments":""}}]}}]}`,
		``,
		`data: {"id":"c1","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":25}}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	rep := parseUsageReport([]byte(sse), true)
	if rep.input != 50 || rep.output != 9 || rep.cacheRead != 25 {
		t.Fatalf("usage = %+v", rep)
	}
	if rep.toolCalls != 2 { // fragmented call_a counted once via id, call_b once
		t.Fatalf("toolCalls = %d, want 2", rep.toolCalls)
	}
}

func TestParseUsageReportResponsesStream(t *testing.T) {
	sse := strings.Join([]string{
		`event: response.output_item.added`,
		`data: {"type":"response.output_item.added","item":{"type":"function_call","name":"f"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":30,"output_tokens":6,"input_tokens_details":{"cached_tokens":12}},"output":[{"type":"function_call"},{"type":"message"}]}}`,
		``,
	}, "\n")

	rep := parseUsageReport([]byte(sse), true)
	if rep.input != 30 || rep.output != 6 || rep.cacheRead != 12 {
		t.Fatalf("usage = %+v", rep)
	}
	if rep.toolCalls != 1 { // output array in completed must not double-count the added item
		t.Fatalf("toolCalls = %d, want 1", rep.toolCalls)
	}
}

func TestParseUsageReportJSONDocuments(t *testing.T) {
	anthropic := `{"content":[{"type":"text"},{"type":"tool_use","id":"t1"}],"usage":{"input_tokens":11,"output_tokens":4,"cache_read_input_tokens":3}}`
	rep := parseUsageReport([]byte(anthropic), false)
	if rep.input != 11 || rep.output != 4 || rep.cacheRead != 3 || rep.toolCalls != 1 {
		t.Fatalf("anthropic usage = %+v", rep)
	}

	chat := `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`
	rep = parseUsageReport([]byte(chat), false)
	if rep.input != 8 || rep.output != 2 || rep.toolCalls != 2 {
		t.Fatalf("chat usage = %+v", rep)
	}

	responses := `{"object":"response","output":[{"type":"function_call"},{"type":"function_call"}],"usage":{"input_tokens":7,"output_tokens":3}}`
	rep = parseUsageReport([]byte(responses), false)
	if rep.input != 7 || rep.output != 3 || rep.toolCalls != 2 {
		t.Fatalf("responses usage = %+v", rep)
	}

	errBody := `{"error":{"message":"overloaded"}}`
	if rep := parseUsageReport([]byte(errBody), false); rep != (usageReport{}) {
		t.Fatalf("error body produced stats: %+v", rep)
	}
}

func TestSSEDataPayloads(t *testing.T) {
	data := "event: a\r\ndata: {\"x\":1}\r\n\r\ndata: {\"y\":\ndata: 2}\n\n: comment\n\n"
	got := sseDataPayloads([]byte(data))
	if len(got) != 2 {
		t.Fatalf("payloads = %q", got)
	}
	if string(got[0]) != `{"x":1}` {
		t.Fatalf("first payload = %q", got[0])
	}
	if string(got[1]) != "{\"y\":\n2}" {
		t.Fatalf("second payload = %q", got[1])
	}
}

func TestCountErroredToolResults(t *testing.T) {
	body := `{"model":"m","messages":[
		{"role":"user","content":"hi"},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true},
			{"type":"tool_result","tool_use_id":"t2"},
			{"type":"text","text":"also saw an error"}]}]}`
	if n := countErroredToolResults([]byte(body), false); n != 1 {
		t.Fatalf("n = %d, want 1", n)
	}
	if n := countErroredToolResults([]byte(`{"messages":[{"role":"user","content":"plain"}]}`), false); n != 0 {
		t.Fatalf("string content counted: %d", n)
	}
}

// An agent loop replays its full history every turn; the same errored
// tool_result must be counted only on the turn it first appears (the turn
// whose last message carries it), not once per subsequent turn.
func TestCountErroredToolResultsLastMessageOnly(t *testing.T) {
	body := `{"model":"m","messages":[
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true}]},
		{"role":"assistant","content":[{"type":"text","text":"retrying"}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true},
			{"type":"tool_result","tool_use_id":"t2","is_error":true}]}]}`
	if n := countErroredToolResults([]byte(body), true); n != 2 {
		t.Fatalf("n = %d, want 2 (last message only)", n)
	}
	if n := countErroredToolResults([]byte(body), false); n != 3 {
		t.Fatalf("n = %d, want 3 (all messages)", n)
	}
}

// ---------------------------------------------------------------------------
// End-to-end through /v1/messages

// slowBodyReader widens the generation window so throughput lands above the
// recorder's minimum-meaningful-window threshold even in an in-process test.
type slowBodyReader struct {
	r   io.Reader
	dly time.Duration
}

func (b *slowBodyReader) Read(p []byte) (int, error) {
	time.Sleep(b.dly)
	return b.r.Read(p)
}

func TestStatsEndToEndAnthropicNative(t *testing.T) {
	streamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":10}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"t"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":5}}`,
		``,
	}, "\n")
	fb := &msgFakeBackend{
		supported:   map[backend.Kind]bool{backend.KindAnthropic: true},
		status:      http.StatusOK,
		contentType: "text/event-stream",
		body:        streamBody,
	}
	// slowNext makes exactly one later response body stream slowly so the
	// throughput observation clears the recorder's minimum window.
	wrapped := &slowOnceBackend{inner: fb}
	cfg := &config.Config{
		Backends:     []config.BackendConfig{{Type: "fake", APIKey: "k"}},
		Routes:       map[string]config.ModelRoute{"m1": {Backend: "fake", Model: "upstream-m1"}},
		DefaultRoute: config.ModelRoute{Backend: "fake"},
	}
	s := New(cfg, msgQuietLogger(), nil, []backend.Backend{wrapped})

	rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":10,"messages":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	// Client still received the stream byte-exact.
	for _, want := range []string{"message_start", `"tool_use"`, `"output_tokens":5`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("client body missing %q: %s", want, rec.Body.String())
		}
	}

	// A follow-up request reports the tool result as errored; its response
	// body is delivered slowly so throughput gets recorded.
	fb.body = `{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":40}}`
	fb.contentType = "application/json"
	wrapped.slowNext = true
	postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":10,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","is_error":true}]}]}`)

	stats := getStatsModels(t, s)
	if len(stats) != 1 {
		t.Fatalf("stats rows = %d, want 1: %+v", len(stats), stats)
	}
	row := stats[0]
	if row.Backend != "fake" || row.Model != "upstream-m1" {
		t.Fatalf("row identity = %s/%s", row.Backend, row.Model)
	}
	if row.Requests != 2 || row.Successes != 2 || row.Uptime != 1 {
		t.Fatalf("availability = %d/%d uptime %f", row.Successes, row.Requests, row.Uptime)
	}
	if row.InputTokens != 21 || row.OutputTokens != 45 || row.CacheReadTokens != 10 {
		t.Fatalf("tokens in/out/cache = %d/%d/%d", row.InputTokens, row.OutputTokens, row.CacheReadTokens)
	}
	if row.ToolCalls != 1 || row.ToolErrors != 1 || row.ToolErrorRate != 1 {
		t.Fatalf("tool calls/errors/rate = %d/%d/%f", row.ToolCalls, row.ToolErrors, row.ToolErrorRate)
	}
	if row.E2E.P50 <= 0 || row.E2E.P99 <= 0 || row.TTFT.P99 <= 0 {
		t.Fatalf("latencies missing: ttft %+v e2e %+v", row.TTFT, row.E2E)
	}
	if row.Throughput.P50 < 5 || row.Throughput.P50 > 700 {
		t.Fatalf("throughput p50 = %f outside sane band", row.Throughput.P50)
	}
}

// slowOnceBackend delivers one response's body at a crawl so measured
// generation windows clear the recorder's minimum in fast tests.
type slowOnceBackend struct {
	inner    *msgFakeBackend
	slowNext bool
}

var _ backend.Backend = (*slowOnceBackend)(nil)

func (b *slowOnceBackend) Name() string                                 { return b.inner.Name() }
func (b *slowOnceBackend) Models(ctx context.Context) ([]string, error) { return b.inner.Models(ctx) }
func (b *slowOnceBackend) Supports(k backend.Kind) bool                 { return b.inner.Supports(k) }

func (b *slowOnceBackend) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	resp, err := b.inner.Send(ctx, req)
	if err != nil || !b.slowNext {
		return resp, err
	}
	b.slowNext = false
	resp.Body = io.NopCloser(&slowBodyReader{r: resp.Body, dly: 60 * time.Millisecond})
	return resp, nil
}

func TestStatsRecordsUpstreamFailureAndTransportError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fb := &msgFakeBackend{
			supported: map[backend.Kind]bool{backend.KindAnthropic: true},
			status:    http.StatusBadGateway,
			body:      `{"error":{"message":"upstream down"}}`,
		}
		s := newMsgServer(t, fb, nil)
		postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":10,"messages":[]}`)

		fb.sendErr = errFakeSend{}
		postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":10,"messages":[]}`)

		stats := getStatsModels(t, s)
		row := stats[0]
		// Both requests fail transiently, so each records the full bounded retry
		// budget plus its final surfaced attempt. Retried attempts count as their
		// own upstream requests so the uptime denominator stays honest.
		if row.Requests != 2*(defaultRetryAttempts+1) || row.Successes != 0 || row.Uptime != 0 {
			t.Fatalf("availability = %d/%d uptime %f", row.Successes, row.Requests, row.Uptime)
		}
	})
}
func TestSnapshotWithPersistenceCountsEachRequestOnce(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := newStats(reg, config.StatsConfig{PersistFile: filepath.Join(t.TempDir(), "stats.json")})
	defer func() { _ = st.Close() }()

	// One successful and one failed attempt against the same backend/model.
	ok := st.track("fake", "upstream-m1")
	ok.setUpstreamStatus(http.StatusOK)
	ok.done()
	bad := st.track("fake", "upstream-m1")
	bad.setUpstreamStatus(http.StatusServiceUnavailable)
	bad.done()

	for _, row := range st.snapshot() {
		if row.Backend != "fake" || row.Model != "upstream-m1" {
			continue
		}
		// record() folds each tracker into both the lifetime counters and the
		// current 5-minute bucket; the summary must not sum the two.
		if row.Requests != 2 || row.Successes != 1 {
			t.Fatalf("requests = %d, successes = %d; want 1 success in 2 requests (no double count)", row.Requests, row.Successes)
		}
		return
	}
	t.Fatal("snapshot missing fake/upstream-m1 row")
}

func TestStatsSeriesRecordsWithoutPersistence(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := newStats(reg, config.StatsConfig{})
	tr := st.track("fake", "upstream-m1")
	tr.setUpstreamStatus(http.StatusOK)
	tr.done()

	now := time.Now()
	series, models, err := st.seriesAt("1h", now)
	if err != nil {
		t.Fatalf("seriesAt: %v", err)
	}
	var requests uint64
	for _, point := range series.Requests {
		requests += uint64(point.Value)
	}
	if requests == 0 {
		t.Fatalf("requests series = %#v, want recorded traffic", series.Requests)
	}
	if len(models) != 1 || models[0] != "upstream-m1" {
		t.Fatalf("models = %#v, want [upstream-m1]", models)
	}
}

func TestStatsSeriesScopeIncludesLoadedModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	now := time.Now()
	window := now.Truncate(5 * time.Minute)
	snapshot := statsSnapshot{
		Version: 1,
		SavedAt: now,
		Models: map[string]modelSnapshot{
			"fake\x00loaded-model": {
				Buckets: []bucket{{
					WindowStart:       window,
					Requests:          2,
					Successes:         1,
					TokensIn:          10,
					TokensOut:         5,
					TTFTBuckets:       make([]uint64, len(ttftEdges)),
					E2EBuckets:        make([]uint64, len(e2eEdges)),
					ThroughputBuckets: make([]uint64, len(tpsEdges)),
				}},
			},
		},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	reg := prometheus.NewRegistry()
	st := newStats(reg, config.StatsConfig{
		PersistFile:   path,
		RetentionDays: 7,
	})
	if err := st.load(path); err != nil {
		t.Fatalf("load: %v", err)
	}

	result, models, err := st.seriesAt("1h", now)
	if err != nil {
		t.Fatalf("seriesAt: %v", err)
	}
	var requests float64
	for _, point := range result.Requests {
		requests += point.Value
	}
	if requests != 2 {
		t.Fatalf("requests = %f, want 2", requests)
	}
	if len(models) != 1 || models[0] != "loaded-model" {
		t.Fatalf("models = %#v, want [loaded-model]", models)
	}
}

type errFakeSend struct{}

func (errFakeSend) Error() string { return "boom" }

// getStatsModels issues GET /stats and decodes the models array.
func getStatsModels(t *testing.T, s *Server) []ModelStat {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/stats status = %d", rec.Code)
	}
	var parsed struct {
		Models []ModelStat `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode /stats: %v", err)
	}
	return parsed.Models
}

// ---------------------------------------------------------------------------
// Sniffer passthrough

func TestSnifferForwardsBytesAndCaptures(t *testing.T) {
	reg := prometheus.NewRegistry()
	tr := newStats(reg, config.StatsConfig{}).track("b", "m")
	payload := strings.Repeat("x", 4096)
	sn := newSniffer(io.NopCloser(strings.NewReader(payload)), tr, false, http.StatusOK)
	buf := make([]byte, len(payload)+16)
	total := 0
	for {
		n, err := sn.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}
	if total != len(payload) || string(buf[:total]) != payload {
		t.Fatalf("passthrough broken: %d bytes", total)
	}
	sn.Finish()
	if tr.rep.output != 0 {
		t.Fatalf("unexpected usage %+v", tr.rep)
	}
	_ = sn.Close()
	_ = sn.Close() // idempotent
}

// ---------------------------------------------------------------------------
// Percentile math

func histFromBuckets(bounds []float64, counts []uint64) *dto.Histogram {
	h := &dto.Histogram{SampleCount: proto.Uint64(counts[len(counts)-1])}
	for i := range bounds {
		h.Bucket = append(h.Bucket, &dto.Bucket{
			CumulativeCount: proto.Uint64(counts[i]),
			UpperBound:      proto.Float64(bounds[i]),
		})
	}
	return h
}

func TestBucketQuantile(t *testing.T) {
	h := histFromBuckets(
		[]float64{0.1, 0.5, 1, 2.5, math.Inf(1)},
		[]uint64{2, 5, 8, 10, 14},
	)
	// rank 7 of 14 falls in bucket (0.5, 1]: interpolated inside it.
	if q := bucketQuantile(0.5, h); q <= 0.5 || q > 1 {
		t.Fatalf("p50 = %f outside its bucket", q)
	}
	// rank 13.86 falls in the final finite bucket (1, 2.5].
	if p99 := bucketQuantile(0.99, h); p99 <= 1 || p99 > 2.5 {
		t.Fatalf("p99 = %f outside its bucket", p99)
	}
	// A full-rank query against an infinite top bucket degrades to its lower edge.
	if got := bucketQuantile(1, h); got != 2.5 {
		t.Fatalf("q=1 with +Inf top bucket = %f, want 2.5", got)
	}
}

func TestPercentilesOfEmpty(t *testing.T) {
	if got := percentilesOf(nil); got != (Percentiles{}) {
		t.Fatalf("nil histogram gave %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Cache-rate computation

func TestCacheRateComputation(t *testing.T) {
	reg := prometheus.NewRegistry()
	st := newStats(reg, config.StatsConfig{})

	// input=100, cached=40 -> 0.4. Upstream providers report input_tokens
	// inclusive of cached tokens, so cache_read is a subset of input, not
	// additive with it.
	tr := st.track("b", "m")
	tr.rep.input = 100
	tr.rep.cacheRead = 40
	tr.done()

	// input=0 -> ratio guards division by zero and yields 0.
	tr2 := st.track("b", "m2")
	tr2.rep.input = 0
	tr2.rep.cacheRead = 0
	tr2.done()

	// Regression for the live-dashboard numbers: 98,112 cached of 98,479
	// input -> 99.6%, not 49.9%.
	tr3 := st.track("b", "m3")
	tr3.rep.input = 98479
	tr3.rep.cacheRead = 98112
	tr3.done()

	got := st.snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot rows = %d, want 3: %+v", len(got), got)
	}
	byModel := make(map[string]ModelStat, len(got))
	for _, r := range got {
		byModel[r.Model] = r
	}

	if want := 40.0 / 100.0; byModel["m"].CacheRate != want {
		t.Fatalf("m CacheRate = %f, want %f", byModel["m"].CacheRate, want)
	}
	if byModel["m2"].CacheRate != 0 {
		t.Fatalf("m2 CacheRate = %f, want 0", byModel["m2"].CacheRate)
	}
	if want := 98112.0 / 98479.0; byModel["m3"].CacheRate != want {
		t.Fatalf("m3 CacheRate = %f, want %f", byModel["m3"].CacheRate, want)
	}
}
