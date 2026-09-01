package zcode

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestSupportsAndDefaults(t *testing.T) {
	client := New("", "token")
	if client.BaseURL != defaultBaseURL {
		t.Errorf("New(\"\") BaseURL = %q, want %q", client.BaseURL, defaultBaseURL)
	}
	if got := New("https://example.test/api/v1/zcode-plan/", "token").BaseURL; got != "https://example.test/api/v1/zcode-plan" {
		t.Errorf("New trailing slash BaseURL = %q", got)
	}
	for _, test := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, true},
		{backend.KindOpenAIResponses, false},
		{"", false},
	} {
		if got := client.Supports(test.kind); got != test.want {
			t.Errorf("Supports(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestSendNativeEndpoints(t *testing.T) {
	const requestBody = `{"model":"glm-5.3-flash","messages":[]}`
	const responseBody = `{"id":"response-id"}`
	for _, test := range []struct {
		name             string
		kind             backend.Kind
		path             string
		streaming        bool
		wantAccept       string
		wantAnthropicVer string
	}{
		{
			name:             "anthropic non-streaming",
			kind:             backend.KindAnthropic,
			path:             "/anthropic/v1/messages",
			wantAccept:       "application/json",
			wantAnthropicVer: anthropicVersion,
		},
		{
			name:       "chat completions streaming",
			kind:       backend.KindOpenAIChat,
			path:       "/chat/completions",
			streaming:  true,
			wantAccept: "text/event-stream",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, test.path)
				}
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if !bytes.Equal(received, []byte(requestBody)) {
					t.Errorf("request body = %q, want byte-for-byte %q", received, requestBody)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer secret" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				if got := r.Header.Get("Accept"); got != test.wantAccept {
					t.Errorf("Accept = %q, want %q", got, test.wantAccept)
				}
				if got := r.Header.Get("Anthropic-Version"); got != test.wantAnthropicVer {
					t.Errorf("Anthropic-Version = %q, want %q", got, test.wantAnthropicVer)
				}
				_, _ = fmt.Fprint(w, responseBody)
			}))
			defer server.Close()

			response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
				Kind:      test.kind,
				Model:     "glm-5.3-flash",
				RawBody:   []byte(requestBody),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			received, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if !bytes.Equal(received, []byte(responseBody)) {
				t.Errorf("response body = %q, want %q", received, responseBody)
			}
		})
	}
}

func TestSendForwardsCaptchaAndRuntimeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{
			"X-Aliyun-Captcha-Verify-Param": "fresh-param",
			"X-ZCode-App-Version":           "3.7.7",
			"X-ZCode-Agent":                 "glm",
			"X-Title":                       "Z Code@test",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-ZCode-Api-Key"); got != "" {
			t.Errorf("X-ZCode-Api-Key was forwarded: %q", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
		Kind: backend.KindAnthropic,
		Header: http.Header{
			"X-Aliyun-Captcha-Verify-Param": []string{"fresh-param"},
			"X-ZCode-App-Version":           []string{"3.7.7"},
			"X-Title":                       []string{"Z Code@test"},
			"X-ZCode-Api-Key":               []string{"must-not-forward"},
		},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestModels(t *testing.T) {
	models, err := New("", "secret").Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	want := []string{"glm-5.3-flash"}
	if !reflect.DeepEqual(models, want) {
		t.Errorf("Models() = %#v, want %#v", models, want)
	}
	models[0] = "changed"
	models, _ = New("", "secret").Models(context.Background())
	if models[0] != want[0] {
		t.Errorf("Models() returned mutable package data: %#v", models)
	}
}

func TestSendRejectsMissingKeyAndUnsupportedKind(t *testing.T) {
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer server.Close()

	client := New(server.URL, "")
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat}); err == nil || !strings.Contains(err.Error(), "no ZCode session") {
		t.Fatalf("Send without key error = %v, want missing-key error", err)
	}

	client.Key = "secret"
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIResponses}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Send unsupported kind error = %v, want unsupported-kind error", err)
	}
	if reached {
		t.Error("upstream called for invalid credentials or unsupported kind")
	}
}

type staticTokenSource string

func (s staticTokenSource) AccessToken(context.Context) (string, error) {
	return string(s), nil
}

type staticTokenAndCaptchaSource struct{}

func (staticTokenAndCaptchaSource) AccessToken(context.Context) (string, error) {
	return "session-token", nil
}

func (staticTokenAndCaptchaSource) CaptchaVerifyParam(context.Context) (string, error) {
	return "source-param", nil
}

func TestSendUsesTokenSourceInsteadOfConfiguredKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Errorf("Authorization = %q, want session token", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "stale-configured-key")
	client.Tokens = staticTokenSource("session-token")
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: []byte(`{"model":"glm-5.3-flash"}`),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestSendUsesCaptchaSourceWhenClientDidNotProvideOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(aliyunCaptchaHeader); got != "source-param" {
			t.Errorf("captcha header = %q, want source-param", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "stale-configured-key")
	client.Tokens = staticTokenAndCaptchaSource{}
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindOpenAIChat,
		RawBody: []byte(`{"model":"glm-5.3-flash"}`),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestBearerTokenAcceptsPrefixedKey(t *testing.T) {
	if got := bearerToken("Bearer secret"); got != "Bearer secret" {
		t.Errorf("bearerToken() = %q, want unchanged bearer token", got)
	}
}
