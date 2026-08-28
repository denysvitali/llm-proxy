package workbuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestSessionReadsDesktopLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
	if err := os.WriteFile(path, []byte(`{"auth":{"accessToken":"account-token"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := NewSession(path).AccessToken(context.Background())
	if err != nil || token != "account-token" {
		t.Fatalf("AccessToken() = %q, %v", token, err)
	}
}

func TestSendUsesPrivateChatEndpointAndAggregatesStream(t *testing.T) {
	var gotPath string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer account-token" || r.Header.Get("X-CodeBuddy-Request") != "1" {
			t.Errorf("missing WorkBuddy auth/fingerprint headers: %v", r.Header)
		}
		if r.Header.Get("Origin") != server.URL {
			t.Errorf("origin = %q, want %q", r.Header.Get("Origin"), server.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true {
			t.Errorf("stream = %#v, want true", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chat-1\",\"model\":\"glm-5.1\",\"created\":7,\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	c := New(server.URL, staticToken("account-token"))
	resp, err := c.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat, Model: "glm-5.1", RawBody: []byte(`{"model":"glm-5.1","messages":[{"role":"user","content":"hi"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if gotPath != "/v2/chat/completions" || !strings.Contains(string(b), `"content":"hello"`) {
		t.Fatalf("path=%q body=%s", gotPath, b)
	}
}

func TestNormalizeRequestSanitizesUnsupportedSchema(t *testing.T) {
	b, err := normalizeRequest([]byte(`{"tools":[{"type":"function","function":{"name":"x","parameters":{"$schema":"x","properties":{"v":{"const":"a"}}}}}],"tool_choice":{"type":"function"}}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "$schema") || strings.Contains(s, "const") || !strings.Contains(s, `"tool_choice":"auto"`) {
		t.Fatalf("normalized body = %s", s)
	}
}

type staticToken string

func (s staticToken) AccessToken(context.Context) (string, error) { return string(s), nil }
