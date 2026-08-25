package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// ---------------------------------------------------------------------------
// rewriteModel
// ---------------------------------------------------------------------------

func TestRewriteModelPreservesKeyOrder(t *testing.T) {
	input := []byte(`{"model":"sonnet","temperature":0.7,"max_tokens":100,"top_p":1}`)
	got, err := rewriteModel(input, "opus")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	want := []byte(`{"model":"opus","temperature":0.7,"max_tokens":100,"top_p":1}`)
	if !bytes.Equal(got, want) {
		t.Errorf("key order not preserved:\n got  %s\n want %s", got, want)
	}
}

func TestRewriteModelPreservesWhitespace(t *testing.T) {
	input := []byte("{\n  \"model\": \"sonnet\",\n  \"temperature\": 0.7\n}")
	got, err := rewriteModel(input, "opus")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	want := []byte("{\n  \"model\": \"opus\",\n  \"temperature\": 0.7\n}")
	if !bytes.Equal(got, want) {
		t.Errorf("whitespace not preserved:\n got  %q\n want %q", got, want)
	}
}

func TestRewriteModelIgnoresModelInsideStringValue(t *testing.T) {
	input := []byte(`{"model":"sonnet","prompt":"the model is x"}`)
	got, err := rewriteModel(input, "opus")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	want := []byte(`{"model":"opus","prompt":"the model is x"}`)
	if !bytes.Equal(got, want) {
		t.Errorf("string value mutated:\n got  %s\n want %s", got, want)
	}
}

func TestRewriteModelOldValueWithEscapes(t *testing.T) {
	// The scanner must parse escapes in the old value to find its true end.
	input := []byte(`{"model":"a\"bAc","x":1}`)
	got, err := rewriteModel(input, "new")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	want := []byte(`{"model":"new","x":1}`)
	if !bytes.Equal(got, want) {
		t.Errorf("got  %s\n want %s", got, want)
	}
}

func TestRewriteModelUnicodeNewValue(t *testing.T) {
	input := []byte(`{"model":"old","x":1}`)
	got, err := rewriteModel(input, "modèle")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	var out struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out.Model != "modèle" {
		t.Errorf("model = %q, want %q", out.Model, "modèle")
	}
	if !bytes.Contains(got, []byte(`"x":1`)) {
		t.Errorf("rest of body mangled: %q", got)
	}
}

func TestRewriteModelPreservesNestedObject(t *testing.T) {
	input := []byte(`{"model":"sonnet","nested":{"a":{"model":"inner","b":2},"c":[1,2,3]},"after":"value"}`)
	got, err := rewriteModel(input, "opus")
	if err != nil {
		t.Fatalf("rewriteModel() error = %v", err)
	}
	want := []byte(`{"model":"opus","nested":{"a":{"model":"inner","b":2},"c":[1,2,3]},"after":"value"}`)
	if !bytes.Equal(got, want) {
		t.Errorf("nested object not preserved byte-for-byte:\n got  %s\n want %s", got, want)
	}
}

func TestRewriteModelInvalidJSON(t *testing.T) {
	for _, in := range []string{`{"model":"sonnet"`, `not json`, `{`, ``, `   `} {
		_, err := rewriteModel([]byte(in), "opus")
		if err == nil {
			t.Errorf("rewriteModel(%q) succeeded, want error", in)
		}
	}
}

func TestRewriteModelMissingModelKey(t *testing.T) {
	_, err := rewriteModel([]byte(`{"temperature":0.7}`), "opus")
	if err == nil {
		t.Error("rewriteModel() succeeded without a model key, want error")
	}
}

func TestRewriteModelNonObject(t *testing.T) {
	for _, in := range [][]byte{[]byte(`[]`), []byte(`"string"`), []byte(`123`), []byte(`null`), []byte(`true`)} {
		_, err := rewriteModel(in, "opus")
		if err == nil {
			t.Errorf("rewriteModel(%q) succeeded, want error", in)
		}
	}
}

func TestRewriteModelDoesNotMatchModelNameKey(t *testing.T) {
	_, err := rewriteModel([]byte(`{"model_name":"sonnet","x":1}`), "opus")
	if err == nil {
		t.Error("rewriteModel() matched model_name, want error")
	}
}

func TestRewriteModelNestedModelKeyUntouched(t *testing.T) {
	_, err := rewriteModel([]byte(`{"nested":{"model":"inner"},"x":1}`), "opus")
	if err == nil {
		t.Error("rewriteModel() matched a nested model key, want error")
	}
}

func TestRewriteModelEmptyObject(t *testing.T) {
	_, err := rewriteModel([]byte(`{}`), "opus")
	if err == nil {
		t.Error("rewriteModel({}) succeeded, want error")
	}
}

func TestRewriteModelModelValueNotString(t *testing.T) {
	for _, in := range []string{`{"model":null}`, `{"model":123}`, `{"model":true}`, `{"model":{"a":1}}`, `{"model":["x"]}`} {
		_, err := rewriteModel([]byte(in), "opus")
		if err == nil {
			t.Errorf("rewriteModel(%s) succeeded with non-string model value, want error", in)
		}
	}
}

// ---------------------------------------------------------------------------
// hasModel
// ---------------------------------------------------------------------------

func TestHasModel(t *testing.T) {
	tests := []struct {
		models []string
		want   string
		ok     bool
		name   string
	}{
		{[]string{"claude-sonnet-4-20250514"}, "claude-sonnet-4", true, "Anthropic dated snapshot matches base"},
		{[]string{"foo-12345678"}, "foo", false, "eight digits that are not a valid date do not match"},
		{[]string{"foo-abcdefgh"}, "foo", false, "non-digit suffix does not match"},
		{[]string{"claude-sonnet-4"}, "claude-sonnet-4", true, "exact match"},
		{[]string{"claude-sonnet-4-20250514"}, "claude-sonnet-4-20250514", true, "exact dated match"},
		{[]string{"foo-20250230"}, "foo", false, "invalid calendar date (Feb 30) does not match"},
		{[]string{"foo-20251301"}, "foo", false, "invalid month does not match"},
		{[]string{"foo-20250001"}, "foo", false, "month 00 does not match"},
		{[]string{"foo-20250101"}, "foo", true, "valid date suffix matches"},
		{[]string{"foo-1234567"}, "foo", false, "seven-digit suffix does not match"},
		{[]string{"foo-123456789"}, "foo", false, "nine-digit suffix does not match"},
		{[]string{}, "anything", false, "empty list never matches"},
		{[]string{"claude-sonnet-4-20250514"}, "claude-sonnet-5", false, "different base does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasModel(tt.models, tt.want); got != tt.ok {
				t.Errorf("hasModel(%q, %q) = %v, want %v", tt.models, tt.want, got, tt.ok)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// catalog staleness cap
// ---------------------------------------------------------------------------

// countingBackend wraps a backend.Backend and counts Models() calls.
type countingBackend struct {
	backend.Backend
	calls int
}

func (c *countingBackend) Models(ctx context.Context) ([]string, error) {
	c.calls++
	return c.Backend.Models(ctx)
}

func TestCatalogRefreshesBeyondStalenessCap(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa", models: []string{"fresh-model"}}
	s := newOATestServer(t, fb, nil)

	cb := &countingBackend{Backend: fb}

	// Inject an entry that expired 15 minutes ago: beyond the 10-minute cap.
	s.catalogs.mu.Lock()
	s.catalogs.entries["fakeoa"] = cachedCatalog{
		models:  []string{"stale-model"},
		expires: time.Now().Add(-15 * time.Minute),
	}
	s.catalogs.mu.Unlock()

	got, err := s.catalog(context.Background(), cb)
	if err != nil {
		t.Fatalf("catalog() unexpected error: %v", err)
	}
	if cb.calls != 1 {
		t.Errorf("Models() calls = %d, want 1 (expired-beyond-cap entry must be refreshed)", cb.calls)
	}
	if len(got) != 1 || got[0] != "fresh-model" {
		t.Errorf("catalog() = %v, want [fresh-model]", got)
	}

	// The refreshed entry should now be fresh; a second call must hit the cache.
	cb.calls = 0
	got2, err := s.catalog(context.Background(), cb)
	if err != nil {
		t.Fatalf("catalog() second call error: %v", err)
	}
	if cb.calls != 0 {
		t.Errorf("second catalog() call hit backend (calls=%d), want cache hit", cb.calls)
	}
	if len(got2) != 1 || got2[0] != "fresh-model" {
		t.Errorf("second catalog() = %v, want [fresh-model]", got2)
	}
}

func TestCatalogServesStaleWithinCap(t *testing.T) {
	fb := &fakeBackend{name: "fakeoa", err: errors.New("upstream down")}
	s := newTestServer(t, []backend.Backend{fb}, config.BackendConfig{Type: "fakeoa"})

	cb := &countingBackend{Backend: fb}

	// Inject an entry fetched 6 minutes ago (expired 5 minutes ago with the
	// 1-minute TTL): within the 10-minute cap.
	s.catalogs.mu.Lock()
	s.catalogs.entries["fakeoa"] = cachedCatalog{
		models:    []string{"stale-model"},
		expires:   time.Now().Add(-5 * time.Minute),
		fetchedAt: time.Now().Add(-6 * time.Minute),
	}
	s.catalogs.mu.Unlock()

	got, err := s.catalog(context.Background(), cb)
	if err != nil {
		t.Fatalf("catalog() unexpected error: %v", err)
	}
	if cb.calls != 1 {
		t.Errorf("Models() calls = %d, want 1", cb.calls)
	}
	if len(got) != 1 || got[0] != "stale-model" {
		t.Errorf("catalog() = %v, want [stale-model] (serve stale within cap)", got)
	}
}

func TestCatalogFreshEntryNotRefreshed(t *testing.T) {
	fb := &fakeOABackend{name: "fakeoa", models: []string{"fresh-model"}}
	s := newOATestServer(t, fb, nil)

	cb := &countingBackend{Backend: fb}

	// Inject an entry that is still fresh.
	s.catalogs.mu.Lock()
	s.catalogs.entries["fakeoa"] = cachedCatalog{
		models:  []string{"cached-model"},
		expires: time.Now().Add(30 * time.Second),
	}
	s.catalogs.mu.Unlock()

	got, err := s.catalog(context.Background(), cb)
	if err != nil {
		t.Fatalf("catalog() unexpected error: %v", err)
	}
	if cb.calls != 0 {
		t.Errorf("Models() calls = %d, want 0 (fresh entry should not be refreshed)", cb.calls)
	}
	if len(got) != 1 || got[0] != "cached-model" {
		t.Errorf("catalog() = %v, want [cached-model]", got)
	}
}
