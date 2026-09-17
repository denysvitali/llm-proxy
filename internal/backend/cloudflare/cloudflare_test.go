package cloudflare_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	_ "github.com/denysvitali/llm-proxy/internal/backend/all"
	"github.com/denysvitali/llm-proxy/internal/backend/cloudflare"
)

func TestNew(t *testing.T) {
	for _, baseURL := range []string{"", "/relative", "//example.com/v1", "ftp://example.com/v1", "https:///v1", "https://user:password@example.com/v1", "https://example.com/v1?x=y", "https://example.com/v1?", "https://example.com/v1#fragment", "https://example.com:bad/v1"} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := cloudflare.New(baseURL, "key"); err == nil {
				t.Fatalf("New(%q) succeeded", baseURL)
			}
		})
	}
	for _, baseURL := range []string{"http://localhost:8080/v1", "https://api.cloudflare.com/client/v4/accounts/test/ai/v1"} {
		c, err := cloudflare.New(baseURL+"///", "key")
		if err != nil || c.BaseURL != baseURL {
			t.Fatalf("New(%q) = %v, %v", baseURL, c, err)
		}
	}
}

func TestRegistrationAndCatalog(t *testing.T) {
	c, err := backend.New("cloudflare", backend.Options{BaseURL: "https://example.com/ai/v1", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "cloudflare" {
		t.Fatalf("Name = %q", c.Name())
	}
	if _, err := backend.New("cloudflare", backend.Options{}); err == nil {
		t.Fatal("registry accepted missing base_url")
	}
	for _, kind := range []backend.Kind{backend.KindOpenAIChat, backend.KindAnthropic, backend.KindOpenAIResponses, "unknown"} {
		if c.Supports(kind) != (kind == backend.KindOpenAIChat || kind == backend.KindOpenAIResponses) {
			t.Errorf("Supports(%q) = %v", kind, c.Supports(kind))
		}
	}
	models, err := c.Models(context.Background())
	if err != nil || !reflect.DeepEqual(models, []string{"stealth/union-alpha"}) {
		t.Fatalf("Models = %v, %v", models, err)
	}
	models[0] = "mutated"
	models, _ = c.Models(context.Background())
	if models[0] != "stealth/union-alpha" {
		t.Fatal("catalog aliases mutable state")
	}
}

func TestSend(t *testing.T) {
	for _, wire := range []struct {
		kind    backend.Kind
		path    string
		version string
	}{
		{backend.KindOpenAIChat, "/chat/completions", ""},
		{backend.KindOpenAIResponses, "/responses", ""},
	} {
		t.Run(string(wire.kind), func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				streaming bool
				status    int
				body      string
			}{
				{"json", false, 200, `{"choices":[{"message":{"content":"Paris"}}]}`},
				{"sse", true, 200, "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}]}\n\ndata: [DONE]\n\n"},
				{"unauthorized", false, 401, `{"error":{"message":"invalid token"}}`},
				{"rate_limit", false, 429, `{"error":{"message":"quota"}}`},
				{"server_error", false, 503, `{"error":{"message":"unavailable"}}`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := `{"model":"stealth/union-alpha", "messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16,"stream":false}`
					accept := "application/json"
					if tc.streaming {
						accept = "text/event-stream"
						body = `{"model":"stealth/union-alpha", "messages":[{"role":"user","content":"Capital of France?"}],"max_tokens":16,"stream":true}`
					}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost || r.URL.Path != "/client/v4/accounts/test/ai/v1"+wire.path {
							t.Errorf("request = %s %s", r.Method, r.URL.Path)
						}
						for key, want := range map[string]string{"Authorization": "Bearer upstream-key", "Content-Type": "application/json", "Accept": accept, "Anthropic-Version": wire.version, "X-Api-Key": "", "Cookie": "", "X-Client-Secret": ""} {
							if got := r.Header.Get(key); got != want {
								t.Errorf("header %s = %q, want %q", key, got, want)
							}
						}
						got, err := io.ReadAll(r.Body)
						if err != nil || string(got) != body {
							t.Errorf("body = %s, err = %v", got, err)
						}
						w.Header().Set("Content-Type", accept)
						w.Header().Set("Retry-After", "12")
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, tc.body)
					}))
					defer upstream.Close()
					c, err := cloudflare.New(upstream.URL+"/client/v4/accounts/test/ai/v1/", "upstream-key")
					if err != nil {
						t.Fatal(err)
					}
					defer c.HTTP.CloseIdleConnections()
					headers := http.Header{"Authorization": {"Bearer client-key"}, "X-Api-Key": {"client-key"}, "Cookie": {"session=private"}, "X-Client-Secret": {"private"}}
					original := headers.Clone()
					resp, err := c.Send(context.Background(), &backend.Request{Kind: wire.kind, Model: "stealth/union-alpha", RawBody: []byte(body), Header: headers, Streaming: tc.streaming})
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = resp.Body.Close() }()
					got, err := io.ReadAll(resp.Body)
					if err != nil || string(got) != tc.body || resp.Status != tc.status || resp.Header.Get("Content-Type") != accept || resp.Header.Get("Retry-After") != "12" {
						t.Fatalf("response = %+v, body = %s, err = %v", resp, got, err)
					}
					if !reflect.DeepEqual(headers, original) {
						t.Fatal("client headers mutated")
					}
				})
			}
		})
	}
}

func TestSendLeavesNonMessagesToolsUntouched(t *testing.T) {
	for _, kind := range []backend.Kind{backend.KindOpenAIChat, backend.KindOpenAIResponses} {
		t.Run(string(kind), func(t *testing.T) {
			body := `{"model":"stealth/union-alpha","tools":[{"name":"untyped","input_schema":{"type":"object"}}]}`
			var seen []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				seen = got
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"id":"x"}`)
			}))
			defer upstream.Close()
			c, err := cloudflare.New(upstream.URL+"/client/v4/accounts/test/ai/v1/", "upstream-key")
			if err != nil {
				t.Fatal(err)
			}
			defer c.HTTP.CloseIdleConnections()
			resp, err := c.Send(context.Background(), &backend.Request{Kind: kind, Model: "stealth/union-alpha", RawBody: []byte(body)})
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.Status != http.StatusOK {
				t.Fatalf("response = %d, body = %s, err = %v", resp.Status, got, err)
			}
			if string(seen) != body {
				t.Errorf("forwarded body = %s, want unchanged %s", seen, body)
			}
		})
	}
}

func TestSendInvalidAndCanceled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected upstream request")
	}))
	defer upstream.Close()
	c, err := cloudflare.New(upstream.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.HTTP.CloseIdleConnections()
	if _, err := c.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat}); !backend.IsTerminal(err) {
		t.Fatalf("missing key: %v", err)
	}
	c.Key = "key"
	for _, kind := range []backend.Kind{backend.KindAnthropic, "", "unknown"} {
		if _, err := c.Send(context.Background(), &backend.Request{Kind: kind}); !backend.IsTerminal(err) {
			t.Fatalf("unsupported kind %q: %v", kind, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Send(ctx, &backend.Request{Kind: backend.KindOpenAIChat}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := c.Models(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("catalog cancellation: %v", err)
	}
}
