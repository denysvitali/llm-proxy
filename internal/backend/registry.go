package backend

import (
	"fmt"
	"sort"
	"sync"
)

// Options carries the constructor inputs every backend factory receives,
// sourced from the matching config.BackendConfig entry.
type Options struct {
	// BaseURL overrides the provider default endpoint; empty means default.
	BaseURL string
	// APIKey is the resolved upstream credential (env var or literal).
	APIKey string
}

// Factory builds a backend instance from its configuration options.
type Factory func(Options) (Backend, error)

var registry = struct {
	mu        sync.RWMutex
	factories map[string]Factory
}{factories: map[string]Factory{}}

// Register installs a backend factory under name. It panics on an empty name,
// nil factory, or duplicate registration: all three are programming errors
// caught at process start, not runtime conditions.
func Register(name string, factory Factory) {
	if name == "" {
		panic("backend: Register called with empty name")
	}
	if factory == nil {
		panic(fmt.Sprintf("backend: Register(%q) called with nil factory", name))
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, dup := registry.factories[name]; dup {
		panic(fmt.Sprintf("backend: duplicate registration of %q", name))
	}
	registry.factories[name] = factory
}

// Has reports whether a backend with the given type name is registered.
func Has(name string) bool {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	_, ok := registry.factories[name]
	return ok
}

// Names returns the registered backend type names in sorted order.
func Names() []string {
	registry.mu.RLock()
	names := make([]string, 0, len(registry.factories))
	for name := range registry.factories {
		names = append(names, name)
	}
	registry.mu.RUnlock()
	sort.Strings(names)
	return names
}

// New builds the backend registered under name. Unknown names return an error
// listing what is available so misconfiguration is actionable.
func New(name string, opts Options) (Backend, error) {
	registry.mu.RLock()
	factory, ok := registry.factories[name]
	registry.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backend: unknown type %q (registered: %v)", name, Names())
	}
	return factory(opts)
}
