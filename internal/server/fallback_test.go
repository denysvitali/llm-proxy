package server

import (
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newNamedScripted builds a scriptedBackend under a different name so
// fallback chains can address several fakes in one server.
func newNamedScripted(name string, kind backend.Kind, steps ...step) *scriptedBackend {
	b := newScripted(kind, steps...)
	b.name = name
	return b
}

// newFallbackServer wires a two-backend chain: "fake" is primary for m1,
// "second" is its fallback. Backend-level fallbacks are appended separately
// by the tests that exercise them.
func newFallbackServer(t *testing.T, primary, secondary backend.Backend, primaryCfg config.BackendConfig, routes map[string]config.ModelRoute) *Server {
	t.Helper()
	cfg := &config.Config{
		Backends: []config.BackendConfig{primaryCfg, {Type: "second", APIKey: "k"}},
		Routes:   routes,
	}
	return New(cfg, msgQuietLogger(), nil, []backend.Backend{primary, secondary})
}

func fallbackRoute() map[string]config.ModelRoute {
	return map[string]config.ModelRoute{
		"m1": {
			Backend:   "fake",
			Model:     "upstream-m1",
			Fallbacks: []config.FallbackRoute{{Backend: "second", Model: "upstream-m1"}},
		},
	}
}

// TestFallbackServesWhenPrimaryExhausted: the primary answers nothing but
// retryable 503s, so after its retry budget is spent the request moves to
// the fallback, which serves a full stream. The client only ever sees the
// fallback's answer.
func TestFallbackServesWhenPrimaryExhausted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusServiceUnavailable, `{"error":{"message":"down"}}`)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())

		rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "event: message_stop") {
			t.Fatalf("fallback stream incomplete:\n%s", rec.Body.String())
		}
		if got := testutil.ToFloat64(s.metrics.fallbacks.WithLabelValues("fake", "second")); got != 1 {
			t.Fatalf("fallback metric = %v, want 1", got)
		}
	})
}

// TestFallbackOnTerminal5xx: a 500 is not retried by the connect-phase loop,
// but it still moves the request to the fallback before anything reaches the
// client.
func TestFallbackOnTerminal5xx(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusInternalServerError, `{"error":{"message":"boom"}}`)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())

		rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if primary.callCount() != 1 {
			t.Fatalf("primary attempts = %d, want 1 (500 is not retried in place)", primary.callCount())
		}
	})
}

// TestNoFallbackOnClientError: a 400 rejection is relayed verbatim; the
// fallback never sees the request.
func TestNoFallbackOnClientError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusBadRequest, `{"error":{"message":"bad input"}}`)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())

		rec := postMsg(t, s, "/v1/messages", `{"model":"m1","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 relayed", rec.Code)
		}
		if secondary.callCount() != 0 {
			t.Fatalf("fallback attempts = %d, want 0", secondary.callCount())
		}
	})
}

// TestNoFallbackAfterContentFlowed: once the primary streamed content and
// broke, replaying on the fallback would duplicate output, so the break is
// surfaced in-band instead.
func TestNoFallbackAfterContentFlowed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", brokenChatSSE)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{Type: "fake", APIKey: "k"}, fallbackRoute())

		rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "event: error") {
			t.Fatalf("expected in-band failure, got:\n%s", rec.Body.String())
		}
		if secondary.callCount() != 0 {
			t.Fatalf("fallback attempts = %d, want 0 after content flowed", secondary.callCount())
		}
	})
}

// TestBackendFallbackAppliesToQualifiedID: qualified IDs bypass routes, so
// the fallback configured on the backend entry itself is what fails over.
func TestBackendFallbackAppliesToQualifiedID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusServiceUnavailable, `{"error":{"message":"down"}}`)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{
				Type:      "fake",
				APIKey:    "k",
				Fallbacks: []config.FallbackRoute{{Backend: "second"}},
			}, nil)

		rec := postMsg(t, s, "/v1/messages",
			`{"model":"fake/m1","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if got := testutil.ToFloat64(s.metrics.fallbacks.WithLabelValues("fake", "second")); got != 1 {
			t.Fatalf("fallback metric = %v, want 1", got)
		}
	})
}

// TestBackendFallbackSkipsQualifiedModelMissingFromCatalog: a qualified ID
// the pinned backend's catalog no longer lists never reaches that backend —
// no wasted upstream round trip, no relayed 4xx — and the configured
// fallback serves the request directly.
func TestBackendFallbackSkipsQualifiedModelMissingFromCatalog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusServiceUnavailable, `{"error":{"message":"down"}}`)})
		secondary := newNamedScripted("second", backend.KindOpenAIChat,
			step{resp: sseResponse("text/event-stream", fullChatSSE)})
		s := newFallbackServer(t, primary, secondary,
			config.BackendConfig{
				Type:      "fake",
				APIKey:    "k",
				Fallbacks: []config.FallbackRoute{{Backend: "second", Model: "rewrite-m1"}},
			}, nil)

		// scriptedBackend's catalog lists only "m1", so fake/gone is a miss.
		rec := postMsg(t, s, "/v1/messages",
			`{"model":"fake/gone","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		if primary.callCount() != 0 {
			t.Fatalf("primary attempts = %d, want 0 (catalog miss must skip the pinned backend)", primary.callCount())
		}
		if got := testutil.ToFloat64(s.metrics.fallbacks.WithLabelValues("fake", "second")); got != 1 {
			t.Fatalf("fallback metric = %v, want 1", got)
		}
	})
}

// TestRetryAttemptsConfigured: backends[].retry_attempts caps the extra
// connect-phase attempts; with 2 the third failure is the one the client
// sees, relayed like any exhausted single-backend proxy would.
func TestRetryAttemptsConfigured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		primary := newNamedScripted("fake", backend.KindOpenAIChat,
			step{resp: unavailableResponse(http.StatusServiceUnavailable, `{"error":{"message":"down"}}`)})
		s := newFallbackServer(t, primary, newNamedScripted("second", backend.KindOpenAIChat),
			config.BackendConfig{Type: "fake", APIKey: "k", RetryAttempts: 2}, nil)

		rec := postMsg(t, s, "/v1/messages", anthropicStreamRequest)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 relayed", rec.Code)
		}
		if primary.callCount() != 3 {
			t.Fatalf("primary attempts = %d, want 3 (1 + retry_attempts)", primary.callCount())
		}
	})
}

// TestResolveChainDedupsAndCaps: fallbacks naming the primary, disabled
// backends, or entries already in the chain are skipped, and the chain is
// capped at maxRouteChain backends including the primary.
func TestResolveChainDedupsAndCaps(t *testing.T) {
	disabled := false
	primary := newNamedScripted("fake", backend.KindOpenAIChat)
	second := newNamedScripted("second", backend.KindOpenAIChat)
	third := newNamedScripted("third", backend.KindOpenAIChat)
	fourth := newNamedScripted("fourth", backend.KindOpenAIChat)
	fifth := newNamedScripted("fifth", backend.KindOpenAIChat)
	sixth := newNamedScripted("sixth", backend.KindOpenAIChat)
	cfg := &config.Config{
		Backends: []config.BackendConfig{
			{Type: "fake", APIKey: "k", Fallbacks: []config.FallbackRoute{
				{Backend: "fake"}, // self: skipped
				{Backend: "gone"}, // not configured: skipped
				{Backend: "fourth"},
				{Backend: "fifth"},
				{Backend: "sixth"}, // beyond the cap: skipped
			}},
			{Type: "second"},
			{Type: "third", Enabled: &disabled}, // disabled: skipped
			{Type: "fourth"},
			{Type: "fifth"},
			{Type: "sixth"},
		},
		Routes: map[string]config.ModelRoute{
			"m1": {Backend: "fake", Fallbacks: []config.FallbackRoute{
				{Backend: "second", Model: "upstream-m2"},
				{Backend: "third"}, // disabled: skipped
			}},
		},
	}
	s := New(cfg, msgQuietLogger(), nil, []backend.Backend{primary, second, third, fourth, fifth, sixth})

	chain, ok := s.resolveChain(t.Context(), "m1")
	if !ok {
		t.Fatal("resolveChain failed")
	}
	var got []string
	for _, rt := range chain {
		got = append(got, rt.backend.Name()+"/"+rt.model)
	}
	// Primary keeps the inbound model name (route has no rewrite), the
	// fallback rewrites, later backend-level fallbacks inherit the primary's.
	want := []string{"fake/m1", "second/upstream-m2", "fourth/m1", "fifth/m1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", got, want)
	}
}
