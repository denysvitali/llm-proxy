package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/denysvitali/llm-proxy/internal/shape"
)

// httptestGet issues an unauthenticated GET against the server's full handler.
func httptestGet(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// shapesFor returns the only retained shape trace in the stats state.
func shapesFor(t *testing.T, s *Server) *ShapeTrace {
	t.Helper()
	requests := s.stats.RecentRequests()
	if len(requests) != 1 {
		t.Fatalf("retained requests = %d, want 1", len(requests))
	}
	if requests[0].Shapes == nil {
		t.Fatal("request retained no shape trace")
	}
	return requests[0].Shapes
}

func hasKey(node *shape.Node, key string) bool {
	return node != nil && node.Keys != nil && node.Keys[key] != nil
}

func TestShapeTraceReportsTranslatedChatToAnthropic(t *testing.T) {
	fb := anthropicOnlyBackend("claude-like")
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","stream":false,"messages":[{"role":"user","content":"hello"}],"temperature":0.5}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	trace := shapesFor(t, s)

	// Client request keeps the Chat keys; the forwarded request is Anthropic.
	if !hasKey(trace.RequestClient, "messages") || !hasKey(trace.RequestClient, "temperature") {
		t.Fatalf("client request shape = %+v", trace.RequestClient)
	}
	// Anthropic requires max_tokens; a Chat request does not carry it.
	if !hasKey(trace.RequestUpstream, "max_tokens") || hasKey(trace.RequestClient, "max_tokens") {
		t.Fatalf("upstream request shape = %+v", trace.RequestUpstream)
	}
	if trace.RequestChanges == nil || trace.RequestChanges.Identical {
		t.Fatalf("request diff = %+v", trace.RequestChanges)
	}

	// Response: Anthropic upstream, Chat client, so both sides are recorded.
	if trace.ResponseUpstream == nil || trace.ResponseUpstream.Format != "json" {
		t.Fatalf("upstream response shape = %+v", trace.ResponseUpstream)
	}
	if !hasKey(trace.ResponseUpstream.Shape, "stop_reason") || !hasKey(trace.ResponseUpstream.Shape, "content") {
		t.Fatalf("upstream response keys = %+v", trace.ResponseUpstream.Shape.Keys)
	}
	if trace.ResponseClient == nil || trace.ResponseClient.Format != "json" || !hasKey(trace.ResponseClient.Shape, "choices") {
		t.Fatalf("client response shape = %+v", trace.ResponseClient)
	}
	if trace.ResponseChanges == nil || len(trace.ResponseChanges.Changes) == 0 {
		t.Fatalf("response diff = %+v", trace.ResponseChanges)
	}
}

func TestShapeTraceNativeRequestIsUnchanged(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		header: http.Header{"Content-Type": []string{"application/json"}},
		body:   `{"id":"c1","object":"chat.completion","choices":[{"message":{"content":"ok"}}]}`,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-x": {Backend: "fakeoa", Model: "upstream-x"},
	})
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-x","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	trace := shapesFor(t, s)
	if trace.RequestChanges == nil || !trace.RequestChanges.Identical {
		t.Fatalf("native request diff = %+v", trace.RequestChanges)
	}
	if trace.ResponseChanges == nil || !trace.ResponseChanges.Identical {
		t.Fatalf("native response diff = %+v", trace.ResponseChanges)
	}
}

func TestShapeTraceTranslatedStreamRecordsClientEvents(t *testing.T) {
	streamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"m1","model":"u-a"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hey"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
	fb := &fakeOABackend{
		name:   "claude-like",
		kinds:  map[backend.Kind]bool{backend.KindAnthropic: true},
		header: http.Header{"Content-Type": []string{"text/event-stream"}},
		body:   streamBody,
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	trace := shapesFor(t, s)
	if trace.ResponseUpstream == nil || trace.ResponseUpstream.Format != "sse" {
		t.Fatalf("upstream stream shape = %+v", trace.ResponseUpstream)
	}
	if trace.ResponseClient == nil || trace.ResponseClient.Format != "sse" {
		t.Fatalf("client stream shape = %+v", trace.ResponseClient)
	}
	if len(trace.ResponseClient.Events) == 0 {
		t.Fatalf("client stream events = %+v", trace.ResponseClient)
	}
	if trace.ResponseChanges == nil || trace.ResponseChanges.Identical {
		t.Fatalf("stream diff = %+v", trace.ResponseChanges)
	}
}

func TestShapeAPIExposesSummariesAndDetails(t *testing.T) {
	fb := anthropicOnlyBackend("claude-like")
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	list := httptestGet(t, s, "/api/shapes")
	var payload struct {
		Shapes []ShapeSummary `json:"shapes"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list: %v (%s)", err, list.Body.String())
	}
	if len(payload.Shapes) != 1 {
		t.Fatalf("shapes = %+v", payload.Shapes)
	}
	summary := payload.Shapes[0]
	if summary.ID == "" || summary.Backend != "claude-like" {
		t.Fatalf("summary = %+v", summary)
	}
	if !summary.Request.Recorded || !summary.Request.Changed || !summary.Response.Changed {
		t.Fatalf("summary diffs = %+v", summary)
	}
	detail := httptestGet(t, s, "/api/shapes/"+summary.ID)
	var trace struct {
		ID     string      `json:"id"`
		Shapes *ShapeTrace `json:"shapes"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode detail: %v (%s)", err, detail.Body.String())
	}
	if trace.ID != summary.ID || trace.Shapes == nil || trace.Shapes.RequestUpstream == nil {
		t.Fatalf("detail = %+v", trace)
	}
	if missing := httptestGet(t, s, "/api/shapes/nope"); missing.Code != http.StatusNotFound {
		t.Fatalf("missing id status = %d", missing.Code)
	}
}

// A shape trace must never contain a value from either side of the exchange.
func TestShapeTraceRetainsNoValues(t *testing.T) {
	fb := anthropicOnlyBackend("claude-like")
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-a": {Backend: "claude-like", Model: "u-a"},
	})
	postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-a","messages":[{"role":"user","content":"SECRET-PROMPT-VALUE"}]}`)
	encoded, err := json.Marshal(shapesFor(t, s))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"SECRET-PROMPT-VALUE", "hi there", "claude-like", "u-a", "gpt-a"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("shape trace leaked %q: %s", leaked, encoded)
		}
	}
}

// Native streaming relays upstream bytes verbatim, so the recorded client
// response is the upstream body itself rather than a second decode.
func TestShapeTraceNativeStreamReusesUpstreamBody(t *testing.T) {
	fb := &fakeOABackend{
		name:   "fakeoa",
		header: http.Header{"Content-Type": []string{"text/event-stream"}},
		body: "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
			"data: [DONE]\n\n",
	}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{
		"gpt-x": {Backend: "fakeoa", Model: "upstream-x"},
	})
	rec := postOpenAI(t, s, "/v1/chat/completions",
		`{"model":"gpt-x","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	trace := shapesFor(t, s)
	if trace.ResponseUpstream == nil || trace.ResponseUpstream.Format != "sse" {
		t.Fatalf("upstream shape = %+v", trace.ResponseUpstream)
	}
	if trace.ResponseClient == nil || trace.ResponseClient.Format != "sse" {
		t.Fatalf("client shape = %+v", trace.ResponseClient)
	}
	if trace.ResponseChanges == nil || !trace.ResponseChanges.Identical {
		t.Fatalf("native stream diff = %+v", trace.ResponseChanges)
	}
}
