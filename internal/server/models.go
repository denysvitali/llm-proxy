package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// modelEntry is one item in the OpenAI-style /v1/models payload.
type modelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// modelList is the OpenAI list envelope for /v1/models.
type modelList struct {
	Object string       `json:"object"`
	Data   []modelEntry `json:"data"`
}

// backendCatalog fetches a backend's model list through the shared catalog
// cache. Server.New leaves the cache zero-valued during bring-up (nil map,
// no TTL), so initialize it lazily on first use.
func (s *Server) backendCatalog(ctx context.Context, b backend.Backend) ([]string, error) {
	s.catalogs.mu.Lock()
	if s.catalogs.entries == nil {
		s.catalogs.entries = make(map[string]cachedCatalog)
	}
	if s.catalogs.ttl <= 0 {
		s.catalogs.ttl = time.Minute
	}
	s.catalogs.mu.Unlock()
	return s.catalog(ctx, b)
}

// enabledBackends returns the constructed backends whose config enables them,
// in configuration order.
func (s *Server) enabledBackends() []backend.Backend {
	out := make([]backend.Backend, 0, len(s.backends))
	for _, b := range s.backends {
		if s.enabled(b.Name()) {
			out = append(out, b)
		}
	}
	return out
}

// handleModels serves GET /v1/models with the merged catalogs of all enabled
// backends. Every entry is also listed in its qualified "<backend>/<id>"
// form — the ID clients can use on any endpoint to pin one backend. Bare IDs
// are listed only when unique across backends (ambiguous ones exist solely
// in qualified form). One failing backend is logged and skipped; when every
// enabled backend fails the answer is 502.
// ?backend=<name> restricts the answer to a single backend.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if name := strings.TrimSpace(r.URL.Query().Get("backend")); name != "" {
		s.handleBackendModels(w, r, name)
		return
	}

	all := s.enabledBackends()
	type catalog struct {
		name   string
		models []string
	}
	lists := make([]catalog, 0, len(all))
	failures := 0
	for _, b := range all {
		models, err := s.backendCatalog(r.Context(), b)
		if err != nil {
			failures++
			s.log.WithError(err).WithField("backend", b.Name()).Warn("catalog fetch failed")
			continue
		}
		lists = append(lists, catalog{name: b.Name(), models: models})
	}
	if failures > 0 && failures == len(all) {
		writeOpenAIError(w, http.StatusBadGateway, "api_error", "all backend catalogs are unavailable")
		return
	}

	bare := make(map[string]int)
	for _, c := range lists {
		for _, id := range c.models {
			if id != "" {
				bare[id]++
			}
		}
	}
	data := make([]modelEntry, 0, 2*len(bare))
	seen := make(map[string]bool)
	add := func(id, owner string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		data = append(data, modelEntry{ID: id, Object: "model", OwnedBy: owner})
	}
	for _, c := range lists {
		for _, id := range c.models {
			add(c.name+"/"+id, c.name)
			if bare[id] == 1 {
				add(id, c.name)
			}
		}
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
}

// handleBackendModels answers /v1/models?backend=<name>.
func (s *Server) handleBackendModels(w http.ResponseWriter, r *http.Request, name string) {
	b, ok := s.byName[name]
	if !ok || !s.enabled(name) {
		writeOpenAIError(w, http.StatusNotFound, "invalid_request_error",
			fmt.Sprintf("unknown backend %q", name))
		return
	}
	models, err := s.backendCatalog(r.Context(), b)
	if err != nil {
		s.log.WithError(err).WithField("backend", name).Warn("catalog fetch failed")
		writeOpenAIError(w, http.StatusBadGateway, "api_error",
			fmt.Sprintf("backend %q catalog is unavailable", name))
		return
	}
	data := make([]modelEntry, 0, len(models))
	for _, id := range models {
		if id == "" {
			continue
		}
		data = append(data, modelEntry{ID: id, Object: "model", OwnedBy: name})
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
}
