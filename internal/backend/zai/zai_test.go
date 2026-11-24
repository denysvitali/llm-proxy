package zai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

var _ backend.Backend = (*Client)(nil)

func TestSupportsAndDefaults(t *testing.T) {
	c := New("", "k")
	if c.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if got := New("https://x.test/api/", "k").BaseURL; got != "https://x.test/api" {
		t.Errorf("trailing slash kept: %q", got)
	}
	if !c.Supports(backend.KindAnthropic) || !c.Supports(backend.KindOpenAIResponses) || c.Supports(backend.KindOpenAIChat) {
		t.Error("wrong native wire support")
	}
}

func TestSendRoutesPerKind(t *testing.T) {
	for _, tc := range []struct {
		kind backend.Kind
		path string
	}{
		{backend.KindAnthropic, anthropicPath},
		{backend.KindOpenAIResponses, responsesPath},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != tc.path {
				t.Errorf("%s: path = %q, want %q", tc.kind, r.URL.Path, tc.path)
			}
			if r.Header.Get("Authorization") != "Bearer key" {
				t.Errorf("%s: auth = %q", tc.kind, r.Header.Get("Authorization"))
			}
			if got := r.Header.Get("Anthropic-Version"); (got != "") != (tc.kind == backend.KindAnthropic) {
				t.Errorf("%s: Anthropic-Version = %q", tc.kind, got)
			}
			_, _ = io.WriteString(w, `{}`)
		}))
		c := New(srv.URL, "key")
		resp, err := c.Send(context.Background(), &backend.Request{Kind: tc.kind, RawBody: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		srv.Close()
	}
}

func TestSendRejectsMissingKeyAndResponses(t *testing.T) {
	if _, err := New("", "").Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic}); err == nil {
		t.Error("expected missing-key error")
	}
	if _, err := New("", "k").Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIChat}); err == nil {
		t.Error("expected unsupported-kind error")
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != modelsPath {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"glm-4.7"},{"id":"glm-4.5-air"}]}`)
	}))
	defer srv.Close()
	got, err := New(srv.URL, "k").Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"glm-4.7", "glm-4.5-air"}; !reflect.DeepEqual(got, want) {
		t.Errorf("models = %v, want %v", got, want)
	}
}

func TestModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "k").Models(context.Background()); err == nil {
		t.Error("expected error")
	}
}
