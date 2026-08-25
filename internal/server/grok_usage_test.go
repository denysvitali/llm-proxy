package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestGrokUsageEndpoint(t *testing.T) {
	isolatePrometheus(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user" {
			_, _ = w.Write([]byte(`{"data":{"user":{"id":"user-1","email":"person@example.com"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"billing":{"credits":{"creditUsagePercent":12.5,"monthlyLimit":1000,"used":125}}}}`))
	}))
	t.Cleanup(upstream.Close)

	grokAuth := grokbackend.NewManager(filepath.Join(t.TempDir(), "auth.json"))
	if err := grokAuth.Store.Save(&grokbackend.Token{AccessToken: "test-token"}); err != nil {
		t.Fatalf("save session: %v", err)
	}
	server := NewWithGrokAuth(&config.Config{
		Backends: []config.BackendConfig{{Type: "grok", BaseURL: upstream.URL}},
	}, quietLogger(), nil, nil, grokAuth)
	server.cfg.Defaults()

	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/grok/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if want := `"percentUsed":12.5`; !contains(rec.Body.String(), want) {
		t.Fatalf("body missing %s: %s", want, rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
