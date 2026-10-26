package backend

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func inferenceContext() context.Context {
	member, _ := baggage.NewMember("secret", "private")
	bag, _ := baggage.New(member)
	state, _ := trace.ParseTraceState("vendor=value")
	return baggage.ContextWithBaggage(trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled, TraceState: state,
	})), bag)
}

func TestInferencePropagationStaysOnTrustedOrigin(t *testing.T) {
	ctx := inferenceContext()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			var foreignCalls, trustedCalls int
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				foreignCalls++
				for _, key := range []string{"traceparent", "tracestate", "baggage"} {
					if got := r.Header.Get(key); got != "" {
						t.Errorf("foreign origin received %s=%q", key, got)
					}
				}
			}))
			defer foreign.Close()
			trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				trustedCalls++
				got := trace.SpanContextFromContext((propagation.TraceContext{}).Extract(context.Background(), propagation.HeaderCarrier(r.Header)))
				want := trace.SpanContextFromContext(ctx)
				if enabled && (got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() || got.TraceState().String() != "vendor=value") {
					t.Errorf("propagated context=%v, want active attempt %v", got, want)
				}
				if !enabled && (r.Header.Get("traceparent") != "" || r.Header.Get("tracestate") != "") {
					t.Error("disabled propagation sent trace context")
				}
				if r.Header.Get("baggage") != "" {
					t.Error("trusted origin received baggage")
				}
				if r.URL.Path == "/start" {
					http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
				} else {
					http.Redirect(w, r, foreign.URL, http.StatusTemporaryRedirect)
				}
			}))
			defer trusted.Close()
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, trusted.URL+"/start", strings.NewReader("{}"))
			req.Header.Set("traceparent", "untrusted")
			req.Header.Set("tracestate", "untrusted")
			req.Header.Set("baggage", "secret=untrusted")
			client := trusted.Client()
			client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
				// A redirect callback must not accidentally reintroduce baggage.
				req.Header.Set("baggage", "callback=value")
				return nil
			}
			resp, err := DoInference(client, req, trusted.URL+"/api/v1", enabled)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if trustedCalls != 2 || foreignCalls != 1 {
				t.Fatalf("trusted calls=%d foreign calls=%d", trustedCalls, foreignCalls)
			}
			if req.Header.Get("traceparent") != "untrusted" || client.Transport == nil {
				t.Error("inference mutated original request or client")
			}
		})
	}
}

func TestInferencePropagationRequiresResolvedOrigin(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") != "" {
			t.Error("request outside trusted origin received trace context")
		}
	}))
	defer target.Close()
	for _, origin := range []string{"https://trusted.invalid", "", ":invalid"} {
		req, _ := http.NewRequestWithContext(inferenceContext(), http.MethodGet, target.URL, nil)
		resp, err := DoInference(target.Client(), req, origin, true)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
}
