package server

import (
	"reflect"
	"sync"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestResolveChainDoesNotMutateConfiguredFallbacks(t *testing.T) {
	for _, source := range []string{"explicit", "default"} {
		t.Run(source, func(t *testing.T) {
			// Two routes share fallback storage, as callers assembling config
			// programmatically may do. Resolving one must not rewrite the other.
			shared := []config.FallbackRoute{{Backend: "second"}, {Backend: "fourth"}}
			configured := config.ModelRoute{Backend: "primary", Model: "upstream", Fallbacks: shared[:1]}
			cfg := &config.Config{
				Backends: []config.BackendConfig{
					{Type: "primary", Fallbacks: []config.FallbackRoute{{Backend: "third"}}},
					{Type: "second"}, {Type: "third"}, {Type: "fourth"},
				},
				Routes: map[string]config.ModelRoute{"other": {Backend: "primary", Fallbacks: shared}},
			}
			if source == "explicit" {
				cfg.Routes["requested"] = configured
			} else {
				cfg.DefaultRoute = configured
			}
			backends := []backend.Backend{
				&fakeBackend{name: "primary"}, &fakeBackend{name: "second"},
				&fakeBackend{name: "third"}, &fakeBackend{name: "fourth"},
			}
			s := New(cfg, quietLogger(), nil, backends)
			t.Cleanup(func() { _ = s.Close() })
			want := []string{"primary/upstream", "second/upstream", "third/upstream"}
			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					chain, ok := s.resolveChain(t.Context(), "requested")
					if !ok {
						t.Error("configured model did not resolve")
						return
					}
					var got []string
					for _, route := range chain {
						got = append(got, route.backend.Name()+"/"+route.model)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("chain = %v, want %v", got, want)
					}
				})
			}
			wg.Wait()
			if got := cfg.Routes["other"].Fallbacks[1].Backend; got != "fourth" {
				t.Errorf("resolving model changed another route's fallback to %q", got)
			}
		})
	}
}
