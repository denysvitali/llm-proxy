package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

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

// A backend whose catalog keeps failing (an expired API key) is queried once
// per catalogFailureTTL, not once per routing scan, and logs one warning per
// interval; a caller that went away logs no warning at all.
func TestCatalogFailureIsHeldAndWarnedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeBackend{name: "expired", err: errors.New("HTTP 401: api_key_expired")}
		b := &countingBackend{Backend: fake}
		log, hook := logtest.NewNullLogger()
		log.SetLevel(logrus.DebugLevel)
		s := &Server{log: log, catalogs: newCatalogCache()}
		for range 3 {
			if _, err := s.catalog(t.Context(), b); err == nil {
				t.Fatal("failing catalog returned no error")
			} else {
				s.logCatalogFailure(t.Context(), b.Name(), err)
			}
		}
		if b.calls != 1 {
			t.Fatalf("failing catalog fetched %d times within the hold, want 1", b.calls)
		}
		time.Sleep(catalogFailureTTL + time.Second)
		fake.err = nil
		fake.models = []string{"m"}
		if models, err := s.catalog(t.Context(), b); err != nil || len(models) != 1 {
			t.Fatalf("recovered catalog = %v, %v", models, err)
		}
		warnings := 0
		for _, entry := range hook.AllEntries() {
			if entry.Level == logrus.WarnLevel {
				warnings++
			}
		}
		if warnings != 1 {
			t.Fatalf("warnings = %d, want 1", warnings)
		}

		hook.Reset()
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		s.logCatalogFailure(canceled, "other", context.Canceled)
		if entry := hook.LastEntry(); entry == nil || entry.Level != logrus.DebugLevel {
			t.Fatalf("canceled caller logged %v, want debug", entry)
		}
	})
}
