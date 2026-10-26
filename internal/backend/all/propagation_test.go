package all_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type propagationToken struct{}

func (propagationToken) AccessToken(context.Context) (string, error) { return "test-token", nil }

func TestEveryInferenceBackendHonorsPropagationOptIn(t *testing.T) {
	for _, name := range backend.Names() {
		for _, enabled := range []bool{false, true} {
			mode := "disabled"
			if enabled {
				mode = "enabled"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				state, _ := trace.ParseTraceState("vendor=value")
				active := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled, TraceState: state})
				ctx := trace.ContextWithSpanContext(context.Background(), active)
				member, _ := baggage.NewMember("private", "secret")
				bag, _ := baggage.New(member)
				ctx = baggage.ContextWithBaggage(ctx, bag)
				calls := 0
				foreignCalls := 0
				foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					foreignCalls++
					for _, key := range []string{"traceparent", "tracestate", "baggage"} {
						if r.Header.Get(key) != "" {
							t.Errorf("redirected origin received %s", key)
						}
					}
					// Keep response handling out of inference protocol aggregation.
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"test"}}`)
				}))
				defer foreign.Close()
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					got := trace.SpanContextFromContext((propagation.TraceContext{}).Extract(context.Background(), propagation.HeaderCarrier(r.Header)))
					if enabled && (got.TraceID() != active.TraceID() || got.SpanID() != active.SpanID() || got.TraceState().String() != "vendor=value") {
						t.Errorf("wrong active attempt: %v", got)
					}
					if !enabled && (r.Header.Get("traceparent") != "" || r.Header.Get("tracestate") != "") {
						t.Error("disabled provider sent trace context")
					}
					if r.Header.Get("baggage") != "" {
						t.Error("provider sent baggage")
					}
					http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
				}))
				defer target.Close()
				client, err := backend.New(name, backend.Options{BaseURL: target.URL, APIKey: "test-key", TokenSource: propagationToken{}})
				if err != nil {
					t.Fatal(err)
				}
				kind := backend.KindOpenAIChat
				if name == "grok" || name == "codex" {
					kind = backend.KindOpenAIResponses
				}
				if name == "zcode" || name == "minimax-code" {
					kind = backend.KindAnthropic
				}
				resp, err := client.Send(ctx, &backend.Request{Kind: kind, Model: "test-model", RawBody: []byte(`{"model":"test-model","messages":[{"role":"user","content":"test"}],"input":[{"role":"user","content":"test"}]}`), Header: http.Header{"Traceparent": {"untrusted"}, "Baggage": {"private=untrusted"}}, TracePropagation: enabled})
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if calls != 1 {
					t.Fatalf("inference calls=%d, want 1", calls)
				}
				if foreignCalls != 1 {
					t.Fatalf("redirect calls=%d, want 1", foreignCalls)
				}
			})
		}
	}
}
