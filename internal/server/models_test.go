package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// fakeBackend is a minimal backend.Backend implementation for tests.
type fakeBackend struct {
	name   string
	models []string
	err    error
}

var _ backend.Backend = (*fakeBackend)(nil)

func (f *fakeBackend) Name() string { return f.name }

func (f *fakeBackend) Models(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]string(nil), f.models...), nil
}

func (f *fakeBackend) Supports(backend.Kind) bool { return true }

func (f *fakeBackend) Send(context.Context, *backend.Request) (*backend.Response, error) {
	return nil, errors.New("fakeBackend cannot send")
}

// quietLogger returns a logger that swallows output so warnings expected in
// tests do not pollute test output.
func quietLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

// isolatePrometheus points the default Prometheus registry at a fresh one so
// each test Server can register its metrics without colliding with servers
// from earlier tests.
func isolatePrometheus(t *testing.T) {
	t.Helper()
	prev := prometheus.DefaultRegisterer
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	t.Cleanup(func() { prometheus.DefaultRegisterer = prev })
}

// newTestServer builds a Server from fake backends with matching config
// entries (one config entry per backend, same order).
func newTestServer(t *testing.T, backends []backend.Backend, backendCfgs ...config.BackendConfig) *Server {
	t.Helper()
	isolatePrometheus(t)
	return New(&config.Config{Backends: backendCfgs}, quietLogger(), nil, backends)
}

// getModels issues GET url against s.Handler() and decodes a successful
// /v1/models response.
func getModels(t *testing.T, s *Server, url string) (*httptest.ResponseRecorder, modelList) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var list modelList
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatalf("decode /v1/models response: %v", err)
		}
	}
	return rec, list
}

func TestHandleModelsMergesAndSorts(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", models: []string{"zephyr", "shared-model"}},
			&fakeBackend{name: "opencode", models: []string{"alpha", "shared-model"}},
		},
		config.BackendConfig{Type: "venice"},
		config.BackendConfig{Type: "opencode"},
	)

	rec, list := getModels(t, s, "/v1/models")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := modelList{
		Object: "list",
		Data: []modelEntry{
			{ID: "alpha", Object: "model", OwnedBy: "opencode"},
			{ID: "shared-model", Object: "model", OwnedBy: "venice"}, // dedup: first backend wins
			{ID: "zephyr", Object: "model", OwnedBy: "venice"},
		},
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("merged list mismatch:\n got %+v\nwant %+v", list, want)
	}
}

func TestHandleModelsToleratesBackendFailure(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", models: []string{"venice-only"}},
			&fakeBackend{name: "grok", err: errors.New("upstream down")},
		},
		config.BackendConfig{Type: "venice"},
		config.BackendConfig{Type: "grok"},
	)

	rec, list := getModels(t, s, "/v1/models")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := modelList{Object: "list", Data: []modelEntry{
		{ID: "venice-only", Object: "model", OwnedBy: "venice"},
	}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("list mismatch:\n got %+v\nwant %+v", list, want)
	}
}

func TestHandleModelsAllBackendsFailing(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", err: errors.New("upstream down")},
		},
		config.BackendConfig{Type: "venice", APIKey: "sk-test"},
	)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Type != "api_error" {
		t.Errorf("error type = %q, want api_error", body.Error.Type)
	}
}

func TestHandleModelsBackendFilter(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", models: []string{"venice-only"}},
			&fakeBackend{name: "opencode", models: []string{"oc-a", "oc-b"}},
		},
		config.BackendConfig{Type: "venice"},
		config.BackendConfig{Type: "opencode"},
	)

	rec, list := getModels(t, s, "/v1/models?backend=opencode")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := modelList{Object: "list", Data: []modelEntry{
		{ID: "oc-a", Object: "model", OwnedBy: "opencode"},
		{ID: "oc-b", Object: "model", OwnedBy: "opencode"},
	}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("filtered list mismatch:\n got %+v\nwant %+v", list, want)
	}

	unknown, _ := getModels(t, s, "/v1/models?backend=nope")
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown backend status = %d, want 404", unknown.Code)
	}
}
