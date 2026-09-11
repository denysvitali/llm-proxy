package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// TestZCodeQuotaRejectionIsNotRetried pins the retry behaviour for the upstream
// shape that matters most for the plan gateway: a spent plan quota arrives as
// HTTP 502 carrying a business envelope. Classified as a transient gateway
// fault it would be retried, and every retry is another request against an
// account the gateway is already throttling — the pattern observed immediately
// before a code-3012 unusual-activity block.
func TestZCodeQuotaRejectionIsNotRetried(t *testing.T) {
	const rejection = `{"code":1005,"msg":"exceed quota limit"}`

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, rejection)
	}))
	defer upstream.Close()

	client := zcodebackend.New(upstream.URL+"/api/v1/zcode-plan", "test-token")
	cfg := &config.Config{
		Backends: []config.BackendConfig{{Type: "zcode", APIKey: "test-token"}},
	}
	s := New(cfg, msgQuietLogger(), nil, []backend.Backend{client})

	rec := postMsg(t, s, "/v1/messages",
		`{"model":"zcode/glm-5.3-flash","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	if got := upstreamCalls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1: a spent plan quota was retried upstream", got)
	}
}
