package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/mistral"
	"github.com/denysvitali/llm-proxy/internal/backend/openrouter"
	"github.com/denysvitali/llm-proxy/internal/config"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestExchangePropagationUsesResolvedFallbackConfigAndChildSpan(t *testing.T) {
	exporter := middlewareExporter(t)
	for _, primaryEnabled := range []bool{false, true} {
		name := "fallback-opt-in"
		if primaryEnabled {
			name = "primary-opt-in"
		}
		t.Run(name, func(t *testing.T) {
			exporter.Reset()
			var contexts [2]trace.SpanContext
			var calls [2]int
			upstream := func(index, status int, enabled bool) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls[index]++
					contexts[index] = trace.SpanContextFromContext((propagation.TraceContext{}).Extract(context.Background(), propagation.HeaderCarrier(r.Header)))
					if contexts[index].IsValid() != enabled {
						t.Errorf("backend %d propagation valid=%v, want %v", index, contexts[index].IsValid(), enabled)
					}
					if r.Header.Get("baggage") != "" {
						t.Error("inference received baggage")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					if status >= 400 {
						_, _ = io.WriteString(w, `{"error":{"message":"upstream failed"}}`)
						return
					}
					_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
				}))
			}
			primaryHTTP := upstream(0, http.StatusInternalServerError, primaryEnabled)
			defer primaryHTTP.Close()
			fallbackHTTP := upstream(1, http.StatusOK, !primaryEnabled)
			defer fallbackHTTP.Close()
			fallback, err := backend.New("mistral-vibe", backend.Options{BaseURL: fallbackHTTP.URL, APIKey: "test-key"})
			if err != nil {
				t.Fatal(err)
			}
			s := newTestServer(t, []backend.Backend{openrouter.New(primaryHTTP.URL, "test-key"), fallback},
				config.BackendConfig{Type: "openrouter", TracePropagation: primaryEnabled},
				config.BackendConfig{Type: "mistral-vibe", TracePropagation: !primaryEnabled})
			s.cfg.Routes = map[string]config.ModelRoute{"test": {Backend: "openrouter", Model: "test", Fallbacks: []config.FallbackRoute{{Backend: "mistral-vibe", Model: "test"}}}}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"test"}]}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			r.Header.Set("tracestate", "vendor=value")
			r.Header.Set("baggage", "secret=untrusted")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if calls != [2]int{1, 1} {
				t.Fatalf("backend calls=%v", calls)
			}
			for index, got := range contexts {
				if !got.IsValid() {
					continue
				}
				if got.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || got.SpanID().String() == "00f067aa0ba902b7" || got.TraceState().String() != "vendor=value" {
					t.Errorf("not a local child of incoming trace: %v", got)
				}
				matched := false
				wantName := []string{"upstream openrouter", "upstream mistral-vibe"}[index]
				for _, span := range exporter.GetSpans() {
					if span.Name == wantName && span.SpanContext.SpanID() == got.SpanID() && span.SpanKind != trace.SpanKindServer && span.Parent.IsValid() {
						matched = true
					}
				}
				if !matched {
					t.Errorf("propagated context does not match exported backend child: %v", got)
				}
			}
		})
	}
}
