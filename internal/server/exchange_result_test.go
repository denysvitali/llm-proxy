package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Every pre-output failure must remain invisible when a fallback is available,
// and must be reported as a committed failure when the final route writes 502.
// Trace attributes are asserted too: these paths previously recorded "ok".
func TestExchangeFailureDeliveryAndTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := tracer
	tracer = provider.Tracer("exchange-result-test")
	t.Cleanup(func() {
		tracer = previous
		_ = provider.Shutdown(context.Background())
	})

	broken := func() *backend.Response {
		return &backend.Response{Status: http.StatusOK, Header: http.Header{}, Body: &failingBody{}}
	}
	cases := []struct {
		name      string
		kind      backend.Kind
		streaming bool
		response  func() *backend.Response
	}{
		{"native body read", backend.KindAnthropic, false, broken},
		{"translated body read", backend.KindOpenAIChat, false, broken},
		{"native error envelope", backend.KindAnthropic, false, func() *backend.Response {
			return jsonResponse(http.StatusOK, cloudflareStyleError)
		}},
		{"translated error envelope", backend.KindOpenAIChat, false, func() *backend.Response {
			return jsonResponse(http.StatusOK, cloudflareStyleError)
		}},
		{"translated malformed body", backend.KindOpenAIChat, false, func() *backend.Response {
			return jsonResponse(http.StatusOK, "invalid json")
		}},
		{"native empty stream", backend.KindAnthropic, true, func() *backend.Response {
			return sseResponse("text/event-stream", "")
		}},
		{"translated empty stream", backend.KindOpenAIChat, true, func() *backend.Response {
			return sseResponse("text/event-stream", "")
		}},
	}
	for _, tc := range cases {
		for _, final := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/final=%t", tc.name, final), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					exporter.Reset()
					upstream := newScripted(tc.kind, step{resp: tc.response()})
					s := newMsgServerWith(t, upstream)
					defer func() { _ = s.Close() }()
					rt := route{backend: upstream, model: "upstream-m1"}
					wire, ok := resolveWire(backend.KindAnthropic, upstream, rt.model)
					if !ok {
						t.Fatal("test backend has no translation path")
					}
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					result := s.exchange(rec, req, msgQuietLogger(), rt, anthropicDialect(nil), wire,
						[]byte(`{"model":"upstream-m1"}`), http.Header{},
						translateEnv{kind: backend.KindAnthropic, clientModel: "m1", streaming: tc.streaming}, final)
					if result.outcome != exchangeFailed || result.committed != final || result.fallbackEligible == final {
						t.Fatalf("result = %+v, want failed, committed=%t, fallback=%t", result, final, !final)
					}
					if final {
						if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "api_error") {
							t.Fatalf("status = %d, body = %s; want final 502", rec.Code, rec.Body.String())
						}
					} else if rec.Body.Len() != 0 || rec.Flushed {
						t.Fatalf("fallback attempt wrote a response: %s", rec.Body.String())
					}
					spans := exporter.GetSpans()
					if len(spans) != 1 {
						t.Fatalf("spans = %d, want 1", len(spans))
					}
					attrs := make(map[attribute.Key]attribute.Value)
					for _, attr := range spans[0].Attributes {
						attrs[attr.Key] = attr.Value
					}
					if got := attrs["llm_proxy.outcome"].AsString(); got != "failed" {
						t.Fatalf("span outcome = %q, want failed", got)
					}
					if attrs["llm_proxy.response_committed"].AsBool() != final || attrs["llm_proxy.fallback_eligible"].AsBool() == final {
						t.Fatalf("span delivery attributes = %v", attrs)
					}
				})
			})
		}
	}
}

type failedClientWriter struct {
	header http.Header
	status int
	writes int
}

func (w *failedClientWriter) Header() http.Header    { return w.header }
func (w *failedClientWriter) WriteHeader(status int) { w.status = status }
func (w *failedClientWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}
func (w *failedClientWriter) Flush() {}

func TestNoFallbackAfterClientWriteFailure(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, kind := range []backend.Kind{backend.KindAnthropic, backend.KindOpenAIChat} {
			t.Run(fmt.Sprintf("%s/streaming=%t", kind, streaming), func(t *testing.T) {
				body := `{"id":"c1","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`
				if kind == backend.KindAnthropic {
					body = `{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}`
				}
				resp := jsonResponse(http.StatusOK, body)
				if streaming {
					body = fullChatSSE
					if kind == backend.KindAnthropic {
						body = fullAnthropicSSE
					}
					resp = sseResponse("text/event-stream", body)
				}
				primary := newNamedScripted("fake", kind, step{resp: resp})
				secondary := newNamedScripted("second", backend.KindAnthropic)
				s := newFallbackServer(t, primary, secondary, config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())
				defer func() { _ = s.Close() }()
				req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(fmt.Sprintf(
					`{"model":"m1","max_tokens":16,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, streaming)))
				client := &failedClientWriter{header: http.Header{}}
				s.Handler().ServeHTTP(client, req)
				if primary.callCount() != 1 || secondary.callCount() != 0 {
					t.Fatalf("attempts = %d primary, %d fallback; want 1 and 0 after response commitment", primary.callCount(), secondary.callCount())
				}
				if client.status != http.StatusOK || client.writes != 1 {
					t.Fatalf("client status = %d, writes = %d; want 200 and one failed write", client.status, client.writes)
				}
			})
		}
	}
}

type cancelingBackend struct {
	*scriptedBackend
	cancel context.CancelFunc
}

func (b *cancelingBackend) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	b.cancel()
	return b.scriptedBackend.Send(ctx, req)
}

func TestNoFallbackAfterClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	primary := &cancelingBackend{
		scriptedBackend: newNamedScripted("fake", backend.KindAnthropic, step{err: errors.New("request canceled")}),
		cancel:          cancel,
	}
	secondary := newNamedScripted("second", backend.KindAnthropic)
	s := newFallbackServer(t, primary, secondary, config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())
	defer func() { _ = s.Close() }()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(anthropicStreamRequest)).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if primary.callCount() != 1 || secondary.callCount() != 0 || rec.Body.Len() != 0 {
		t.Fatalf("attempts = %d primary, %d fallback, response = %q; canceled request must stop without output", primary.callCount(), secondary.callCount(), rec.Body.String())
	}
}
