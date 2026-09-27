package minimaxcode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestMessagesRequestMatchesMiniMaxCodeGateway(t *testing.T) {
	var sessionIDs []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mavis/api/v1/llm/v1/messages" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		for key, want := range map[string]string{
			"Authorization": "Bearer account-token", "X-Api-Key": "sk-xxx",
			"Anthropic-Version": "2023-06-01", "User-Agent": "MiniMaxAgent",
			"X-Mavis-Agent-Id": "main", "Accept": "text/event-stream",
		} {
			if got := r.Header.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		sessionIDs = append(sessionIDs, r.Header.Get("X-Mavis-Session-Id"))
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"MiniMax-M3","stream":true}` {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "event: message_start\n\n")
	}))
	defer upstream.Close()
	client := New(upstream.URL+"/mavis/api/v1/llm/v1", "account-token")
	for range 2 {
		resp, err := client.Send(context.Background(), &backend.Request{
			Kind: backend.KindAnthropic, RawBody: []byte(`{"model":"MiniMax-M3","stream":true}`),
			Header: http.Header{"X-Session-Id": {"conversation-1"}}, Streaming: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Status != http.StatusAccepted || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Errorf("response = %d, %v", resp.Status, resp.Header)
		}
		body, _ := io.ReadAll(resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "message_start") {
			t.Errorf("response body = %q", body)
		}
	}
	if len(sessionIDs) != 2 || sessionIDs[0] == "" || sessionIDs[0] != sessionIDs[1] {
		t.Errorf("session IDs = %v", sessionIDs)
	}
}

func TestModelsAndSupportedWire(t *testing.T) {
	client := New("", "")
	if client.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q", client.BaseURL)
	}
	if !client.Supports(backend.KindAnthropic) || client.Supports(backend.KindOpenAIChat) || client.Supports(backend.KindOpenAIResponses) {
		t.Fatal("MiniMax Code must expose only Anthropic Messages")
	}
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != catalogPath {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("region") != "en" || r.URL.Query().Get("buildEnv") != "prod" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"version":"1.0","ttlSeconds":300,
			"providers":[{"providerId":"minimax","config":{
				"models":{"MiniMax-M3":{},"MiniMax-M3.1-Flash-Preview":{},"MiniMax-M2.7":{}},
				"model_order":["MiniMax-M3.1-Flash-Preview","MiniMax-M3","MiniMax-M2.7"],
				"whitelist":["MiniMax-M3","MiniMax-M2.7"]
			}}]
		}`)
	}))
	defer catalog.Close()
	client = New(catalog.URL+"/mavis/api/v1/llm/v1", "")
	models, err := client.Models(context.Background())
	want := []string{"MiniMax-M3.1-Flash-Preview", "MiniMax-M3", "MiniMax-M2.7"}
	if err != nil || strings.Join(models, ",") != strings.Join(want, ",") {
		t.Errorf("models = %v, %v; want %v", models, err, want)
	}
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat}); err == nil {
		t.Fatal("unsupported wire request succeeded")
	}
}

func TestModelsFallsBackWhenCatalogUnparseable(t *testing.T) {
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"providers":[{"providerId":"other","config":{"models":{"X":{}}}}]}`)
	}))
	defer catalog.Close()
	client := New(catalog.URL+"/mavis/api/v1/llm/v1", "")
	models, err := client.Models(context.Background())
	if err != nil || strings.Join(models, ",") != strings.Join(fallbackModels, ",") {
		t.Errorf("models = %v, %v; want fallback %v", models, err, fallbackModels)
	}
}

func TestModelsFallsBackWhenCatalogUnavailable(t *testing.T) {
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer catalog.Close()
	client := New(catalog.URL+"/mavis/api/v1/llm/v1", "")
	models, err := client.Models(context.Background())
	if err != nil || strings.Join(models, ",") != strings.Join(fallbackModels, ",") {
		t.Errorf("models = %v, %v; want fallback %v", models, err, fallbackModels)
	}
}
