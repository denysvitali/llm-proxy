package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
)

// Build identity shown by the overview API and SPA.
const (
	proxyName    = "llm-proxy"
	proxyVersion = "0.1.0"
)

// overviewBackend describes one configured backend. It never carries key
// material — only HasKey.
type overviewBackend struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Host      string   `json:"host"`
	HasKey    bool     `json:"hasKey"`
	Models    []string `json:"models"`
	CatalogOK bool     `json:"catalogOK"`
}

// overviewRoute is one configured route. An empty Upstream means the requested
// model name is forwarded unchanged.
type overviewRoute struct {
	Model    string `json:"model"`
	Backend  string `json:"backend"`
	Upstream string `json:"upstream"`
}

// overviewPage carries everything rendered by the dashboard SPA.
type overviewPage struct {
	Name          string            `json:"name"`
	Version       string            `json:"version"`
	Listen        string            `json:"listen"`
	AuthEnabled   bool              `json:"authEnabled"`
	Backends      []overviewBackend `json:"backends"`
	Routes        []overviewRoute   `json:"routes"`
	Stats         []ModelStat       `json:"stats,omitempty"`
	HasDefault    bool              `json:"hasDefault"`
	DefaultRoute  overviewRoute     `json:"defaultRoute"`
	ExampleModel  string            `json:"exampleModel"`
	ClaudeSnippet string            `json:"claudeSnippet"`
	CodexSnippet  string            `json:"codexSnippet"`
}

// handleOverview serves GET /api/overview: the data backing the dashboard
// SPA. Key material is never included — only its presence.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.buildOverviewPage(r))
}

// buildOverviewPage collects data for the dashboard SPA. Upstream API keys are
// never included in the response; only their presence is reported.
func (s *Server) buildOverviewPage(r *http.Request) overviewPage {
	page := overviewPage{
		Name:        proxyName,
		Version:     proxyVersion,
		Listen:      s.cfg.Server.Listen,
		AuthEnabled: s.auth != nil,
		Backends:    make([]overviewBackend, 0, len(s.cfg.Backends)),
		Routes:      make([]overviewRoute, 0, len(s.cfg.Routes)),
	}

	for _, bc := range s.cfg.Backends {
		entry := overviewBackend{
			Name:    bc.Type,
			Enabled: bc.IsEnabled(),
			Host:    baseURLHost(bc.BaseURL),
			HasKey:  bc.ResolveKey(os.Getenv) != "",
		}
		if entry.Enabled {
			if models, err := s.backendCatalogForOverview(r, bc.Type); err != nil {
				entry.CatalogOK = false
			} else {
				entry.CatalogOK = true
				entry.Models = models
			}
		}
		page.Backends = append(page.Backends, entry)
	}
	page.ExampleModel = exampleModel(page.Backends)

	for _, name := range s.sortedRoutes() {
		rt := s.cfg.Routes[name]
		page.Routes = append(page.Routes, overviewRoute{
			Model:    name,
			Backend:  rt.Backend,
			Upstream: rt.Model,
		})
	}
	if d := s.cfg.DefaultRoute; d.Backend != "" {
		page.HasDefault = true
		page.DefaultRoute = overviewRoute{Model: "anything unmatched", Backend: d.Backend, Upstream: d.Model}
	}

	page.Stats = s.stats.snapshot()

	page.ClaudeSnippet = fmt.Sprintf(
		"ANTHROPIC_BASE_URL=http://%s ANTHROPIC_AUTH_TOKEN=<key> claude --model %s",
		r.Host, page.ExampleModel)
	page.CodexSnippet = fmt.Sprintf(`# ~/.codex/config.toml
[model_providers.%s]
name = %q
base_url = "http://%s/v1"
wire_api = "responses"
env_key = "LLM_PROXY_API_KEY"

# launch: codex --config model_provider=%s --model %s`,
		proxyName, proxyName, r.Host, proxyName, page.ExampleModel)
	return page
}

// backendCatalogForOverview lists one backend's models, sorted. A configured
// backend without a constructed instance counts as unavailable.
func (s *Server) backendCatalogForOverview(r *http.Request, name string) ([]string, error) {
	b, ok := s.byName[name]
	if !ok {
		return nil, fmt.Errorf("backend %q is configured but not constructed", name)
	}
	models, err := s.backendCatalog(r.Context(), b)
	if err != nil {
		s.log.WithError(err).WithField("backend", name).Warn("catalog fetch failed")
		return nil, err
	}
	out := append([]string(nil), models...)
	sort.Strings(out)
	return out, nil
}

// exampleModel picks a representative model for setup snippets: the first
// enabled backend's first model, falling back to a placeholder.
func exampleModel(backends []overviewBackend) string {
	for _, b := range backends {
		if b.Enabled && b.CatalogOK && len(b.Models) > 0 {
			return b.Models[0]
		}
	}
	return "<model>"
}

// baseURLHost extracts host:port from a base URL for display; an unset base
// URL means the backend's provider default.
func baseURLHost(raw string) string {
	if raw == "" {
		return "(provider default)"
	}
	parsedURL, err := url.Parse(raw)
	if err != nil || parsedURL.Host == "" {
		return raw
	}
	return parsedURL.Host
}
