package server

import (
	"reflect"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestCatalogCacheZeroValue(t *testing.T) {
	var cache catalogCache
	b := &countingBackend{Backend: &fakeBackend{name: "provider", models: []string{"model"}}}
	for range 2 {
		models, err := cache.get(t.Context(), b, quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(models, []string{"model"}) {
			t.Fatalf("models = %v, want [model]", models)
		}
	}
	if b.calls != 1 {
		t.Errorf("backend catalog fetched %d times, want 1", b.calls)
	}
}

func TestRoutingAndModelsShareCatalogCache(t *testing.T) {
	b := &countingBackend{Backend: &fakeBackend{name: "provider", models: []string{"model"}}}
	s := newTestServer(t, []backend.Backend{b}, config.BackendConfig{Type: "provider"})
	t.Cleanup(func() { _ = s.Close() })
	if _, ok := s.resolveChain(t.Context(), "model"); !ok {
		t.Fatal("catalog model did not resolve")
	}
	response, models := getModels(t, s, "/v1/models")
	if response.Code != 200 || len(models.Data) != 1 || models.Data[0].ID != "provider/model" {
		t.Fatalf("unexpected model list: status=%d models=%+v", response.Code, models)
	}
	if b.calls != 1 {
		t.Errorf("routing and model listing fetched catalog %d times, want 1", b.calls)
	}
}
