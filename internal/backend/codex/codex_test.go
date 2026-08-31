package codex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

type staticCredentials struct{ credentials Credentials }

func (s staticCredentials) AccessToken(context.Context) (string, error) {
	return s.credentials.AccessToken, nil
}
func (s staticCredentials) Credentials(context.Context) (Credentials, error) {
	return s.credentials, nil
}

func TestModelsUsesChatGPTAccountAuthentication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.URL.Query().Get("client_version") == "" {
			t.Errorf("request URL = %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-Id") != "account" {
			t.Errorf("auth headers = %#v", r.Header)
		}
		writeTestJSON(w, map[string]any{"models": []map[string]any{
			{"slug": "gpt-codex", "supported_in_api": true},
			{"slug": "hidden", "supported_in_api": false},
		}})
	}))
	defer server.Close()
	client := New(server.URL, staticCredentials{Credentials{AccessToken: "token", AccountID: "account"}})
	client.HTTP = server.Client()
	models, err := client.Models(t.Context())
	if err != nil || len(models) != 1 || models[0] != "gpt-codex" {
		t.Fatalf("Models = %#v, %v", models, err)
	}
}

func TestSendForwardsResponsesRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-Id") != "account" || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("headers = %#v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"gpt-codex"`) {
			t.Errorf("body = %s", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: done\n\n"))
	}))
	defer server.Close()
	client := New(server.URL, staticCredentials{Credentials{AccessToken: "token", AccountID: "account"}})
	client.HTTP = server.Client()
	body, _ := json.Marshal(map[string]any{"model": "gpt-codex", "stream": true})
	resp, err := client.Send(t.Context(), &backend.Request{Kind: backend.KindOpenAIResponses, RawBody: body, Streaming: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d", resp.Status)
	}
}

func TestSendAggregatesNonStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["stream"] != true || r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("body = %#v, Accept = %q", body, r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	client := New(server.URL, staticCredentials{Credentials{AccessToken: "token", AccountID: "account"}})
	client.HTTP = server.Client()
	resp, err := client.Send(t.Context(), &backend.Request{Kind: backend.KindOpenAIResponses, RawBody: []byte(`{"model":"gpt-codex","stream":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(body), `"id":"resp_1"`) {
		t.Fatalf("response headers = %#v, body = %s", resp.Header, body)
	}
}

func TestClientSupportsOnlyResponses(t *testing.T) {
	client := New("", nil)
	if !client.Supports(backend.KindOpenAIResponses) || client.Supports(backend.KindOpenAIChat) || client.Supports(backend.KindAnthropic) {
		t.Fatal("unexpected wire support")
	}
}
