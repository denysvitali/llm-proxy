package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// cappedBackend speaks one wire, declares a catalog output limit, and keeps
// the last body it was sent.
type cappedBackend struct {
	kind  backend.Kind
	limit int
	mu    sync.Mutex
	body  []byte
}

func (b *cappedBackend) Name() string                             { return "fake" }
func (b *cappedBackend) Models(context.Context) ([]string, error) { return []string{"m1"}, nil }
func (b *cappedBackend) Supports(k backend.Kind) bool             { return k == b.kind }
func (b *cappedBackend) ModelInfo(string) (backend.ModelInfo, bool) {
	return backend.ModelInfo{MaxOutputTokens: b.limit}, b.limit > 0
}

func (b *cappedBackend) Send(_ context.Context, req *backend.Request) (*backend.Response, error) {
	b.mu.Lock()
	b.body = append([]byte(nil), req.RawBody...)
	b.mu.Unlock()
	body := `{"id":"c","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	return &backend.Response{Status: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (b *cappedBackend) sentInt(t *testing.T, field string) int64 {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b.body, &fields); err != nil {
		t.Fatalf("forwarded body: %v", err)
	}
	var n int64
	if err := json.Unmarshal(fields[field], &n); err != nil {
		t.Fatalf("%s missing in %s", field, b.body)
	}
	return n
}

// Gateways such as Open Design reject an output cap above the model's limit
// with an opaque 400, so the proxy lowers it to the declared maximum.
func TestOutputCapClampedToModelLimit(t *testing.T) {
	t.Run("catalog limit, translated Messages", func(t *testing.T) {
		b := &cappedBackend{kind: backend.KindOpenAIChat, limit: 32000}
		rec := postMsg(t, newMsgServerWith(t, b), "/v1/messages", `{"model":"m1","max_tokens":200000,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK || b.sentInt(t, "max_tokens") != 32000 {
			t.Fatalf("status %d, sent %s", rec.Code, b.body)
		}
	})
	t.Run("catalog limit, native Chat keeps other bytes", func(t *testing.T) {
		b := &cappedBackend{kind: backend.KindOpenAIChat, limit: 32000}
		rec := postMsg(t, newMsgServerWith(t, b), "/v1/chat/completions", `{"model":"m1","max_completion_tokens":99999,"messages":[{"role":"user","content":"hi"}],"x_extra":1.50}`)
		if rec.Code != http.StatusOK || b.sentInt(t, "max_completion_tokens") != 32000 || !strings.Contains(string(b.body), `"x_extra":1.50`) {
			t.Fatalf("status %d, sent %s", rec.Code, b.body)
		}
	})
	t.Run("within limit is untouched", func(t *testing.T) {
		b := &cappedBackend{kind: backend.KindOpenAIChat, limit: 32000}
		postMsg(t, newMsgServerWith(t, b), "/v1/chat/completions", `{"model":"m1","max_tokens":1000,"messages":[{"role":"user","content":"hi"}]}`)
		if b.sentInt(t, "max_tokens") != 1000 {
			t.Fatalf("sent %s", b.body)
		}
	})
	t.Run("operator metadata wins over catalog", func(t *testing.T) {
		b := &cappedBackend{kind: backend.KindOpenAIChat, limit: 32000}
		limit := 131072
		cfg := &config.Config{
			Backends:     []config.BackendConfig{{Type: "fake", APIKey: "k", ModelMetadata: []config.ModelMetadata{{Model: "upstream-m1", MaxOutputTokens: &limit}}}},
			Routes:       map[string]config.ModelRoute{"m1": {Backend: "fake", Model: "upstream-m1"}},
			DefaultRoute: config.ModelRoute{Backend: "fake"},
		}
		postMsg(t, New(cfg, msgQuietLogger(), nil, []backend.Backend{b}), "/v1/chat/completions", `{"model":"m1","max_tokens":200000,"messages":[{"role":"user","content":"hi"}]}`)
		if b.sentInt(t, "max_tokens") != 131072 {
			t.Fatalf("sent %s", b.body)
		}
	})
}

func TestClampTopLevelIntsOnlyTouchesTopLevel(t *testing.T) {
	in := `{"max_tokens":500,"nested":{"max_tokens":900},"max_output_tokens":"9000"}`
	out, changed := clampTopLevelInts([]byte(in), outputTokenFields, 100)
	if !changed || string(out) != `{"max_tokens":100,"nested":{"max_tokens":900},"max_output_tokens":"9000"}` {
		t.Fatalf("out = %s", out)
	}
}
