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
	// Every catalog ID is exposed in its unambiguously provider-qualified form.
	want := modelList{
		Object: "list",
		Data: []modelEntry{
			{ID: "opencode/alpha", Object: "model", OwnedBy: "opencode"},
			{ID: "opencode/shared-model", Object: "model", OwnedBy: "opencode"},
			{ID: "venice/shared-model", Object: "model", OwnedBy: "venice"},
			{ID: "venice/zephyr", Object: "model", OwnedBy: "venice"},
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
		{ID: "venice/venice-only", Object: "model", OwnedBy: "venice"},
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
		{ID: "opencode/oc-a", Object: "model", OwnedBy: "opencode"},
		{ID: "opencode/oc-b", Object: "model", OwnedBy: "opencode"},
	}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("filtered list mismatch:\n got %+v\nwant %+v", list, want)
	}

	unknown, _ := getModels(t, s, "/v1/models?backend=nope")
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown backend status = %d, want 404", unknown.Code)
	}
}

// getCodexModels issues GET url with a codex user agent and returns the raw
// recorder plus the decoded codex envelope.
func getCodexModels(t *testing.T, s *Server, url string) (*httptest.ResponseRecorder, codexModelsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "codex_exec/0.150.1 (Ubuntu 24.4.0; x86_64)")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var resp codexModelsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode codex /v1/models response: %v", err)
		}
	}
	return rec, resp
}

// codexModelFields is the exact JSON key set codex 0.144–0.150 catalog
// parsers accept. Codex rejects missing required fields, so dropping a key
// from codexModel breaks real clients; this lock fails first.
var codexModelFields = []string{
	"additional_speed_tiers", "apply_patch_tool_type", "availability_nux",
	"base_instructions", "comp_hash", "context_window",
	"default_reasoning_level", "default_reasoning_summary",
	"default_verbosity", "description", "display_name",
	"effective_context_window_percent", "experimental_supported_tools",
	"include_apps_usage_instructions", "include_plugin_usage_instructions",
	"include_skills_usage_instructions", "input_modalities",
	"max_context_window", "max_output_tokens", "model_messages",
	"multi_agent_version", "node_repl_auto_review_required",
	"node_repl_disabled", "priority", "reasoning", "service_tiers",
	"shell_type", "slug", "support_verbosity", "supported_in_api",
	"supported_reasoning_levels", "supports_image_detail_original",
	"supports_parallel_tool_calls", "supports_reasoning_summaries",
	"supports_search_tool", "tool_mode", "truncation_policy", "upgrade",
	"use_responses_lite", "visibility", "web_search_tool_type",
}

func TestHandleModelsCodexEnvelope(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", models: []string{"zephyr"}},
			&fakeBackend{name: "opencode", models: []string{"alpha"}},
		},
		config.BackendConfig{Type: "venice"},
		config.BackendConfig{Type: "opencode"},
	)

	rec, resp := getCodexModels(t, s, "/v1/models")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// The codex envelope must carry models[] and must not leak the OpenAI
	// data/object keys: codex decodes this body as ModelsResponse.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw body: %v", err)
	}
	if _, ok := raw["data"]; ok {
		t.Error("codex response contains OpenAI \"data\" envelope")
	}
	if _, ok := raw["models"]; !ok {
		t.Error("codex response missing \"models\" field")
	}

	wantSlugs := []string{"opencode/alpha", "venice/zephyr"}
	if len(resp.Models) != len(wantSlugs) {
		t.Fatalf("model count = %d, want %d (%+v)", len(resp.Models), len(wantSlugs), resp.Models)
	}
	for i, want := range wantSlugs {
		got := resp.Models[i]
		if got.Slug != want || got.DisplayName != want {
			t.Errorf("entry %d slug/display_name = %q/%q, want %q", i, got.Slug, got.DisplayName, want)
		}
		// Schema lock: every serialized entry carries the full field set
		// the codex parsers require.
		var entry map[string]json.RawMessage
		reencoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("re-marshal entry: %v", err)
		}
		if err := json.Unmarshal(reencoded, &entry); err != nil {
			t.Fatalf("decode entry: %v", err)
		}
		for _, field := range codexModelFields {
			if _, ok := entry[field]; !ok {
				t.Errorf("entry %s missing field %q", want, field)
			}
		}
		if got.ModelMessages.InstructionsTemplate == "" {
			t.Errorf("entry %s has empty model_messages.instructions_template", want)
		}
	}
}

func TestHandleModelsCodexBackendFilter(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{
			&fakeBackend{name: "venice", models: []string{"venice-only"}},
			&fakeBackend{name: "opencode", models: []string{"oc-a"}},
		},
		config.BackendConfig{Type: "venice"},
		config.BackendConfig{Type: "opencode"},
	)

	rec, resp := getCodexModels(t, s, "/v1/models?backend=opencode")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(resp.Models) != 1 || resp.Models[0].Slug != "opencode/oc-a" {
		t.Errorf("filtered codex models = %+v, want one opencode/oc-a", resp.Models)
	}
}

// Non-codex clients must keep receiving the OpenAI envelope, without the
// codex models array leaking in.
func TestHandleModelsOpenAIEnvelopeUnchangedForOtherClients(t *testing.T) {
	s := newTestServer(t,
		[]backend.Backend{&fakeBackend{name: "venice", models: []string{"zephyr"}}},
		config.BackendConfig{Type: "venice"},
	)

	for _, ua := range []string{"", "OpenAI/Python 1.90.0", "Mozilla/5.0"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ua %q: status = %d", ua, rec.Code)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("ua %q: decode: %v", ua, err)
		}
		if _, ok := body["models"]; ok {
			t.Errorf("ua %q: response contains codex \"models\" field", ua)
		}
		if _, ok := body["data"]; !ok {
			t.Errorf("ua %q: response missing OpenAI \"data\" field", ua)
		}
	}
}
