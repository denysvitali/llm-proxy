package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	grokbackend "github.com/denysvitali/llm-proxy/internal/backend/grok"
	zcodebackend "github.com/denysvitali/llm-proxy/internal/backend/zcode"
)

// Build identity shown by the overview API and SPA.
const (
	proxyName    = "llm-proxy"
	proxyVersion = "0.1.0"
)

// overviewBackend describes one configured backend. It never carries key
// material — only HasKey.
type overviewBackend struct {
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Host           string            `json:"host"`
	HasKey         bool              `json:"hasKey"`
	AuthLabel      string            `json:"authLabel"`
	AuthConfigured bool              `json:"authConfigured"`
	Models         []string          `json:"models"`
	ModelCredits   map[string]string `json:"modelCredits,omitempty"`
	CatalogOK      bool              `json:"catalogOK"`
}

type usageMetadata struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Error      string `json:"error,omitempty"`
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
	GrokUsage     usageMetadata     `json:"grokUsage"`
	ZcodeUsage    usageMetadata     `json:"zcodeUsage"`
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
			HasKey:  bc.Type != "grok" && bc.Type != "codex" && bc.Type != "zcode" && bc.ResolveKey(os.Getenv) != "",
		}
		switch bc.Type {
		case "grok":
			entry.AuthLabel = "xAI account"
			entry.AuthConfigured = s.grokAuth != nil && s.grokAuth.HasSession()
		case "workbuddy":
			entry.AuthLabel = "WorkBuddy account"
			entry.AuthConfigured = s.workBuddyAuth != nil && s.workBuddyAuth.HasSession()
			entry.HasKey = false
		case "codex":
			entry.AuthLabel = "ChatGPT account"
			entry.AuthConfigured = s.codexAuth != nil && s.codexAuth.HasSession()
			entry.HasKey = false
		case "zcode":
			entry.AuthLabel = "ZCode account"
			entry.AuthConfigured = s.zcodeAuth != nil && s.zcodeAuth.HasSession()
			entry.HasKey = false
		default:
			entry.AuthLabel = "API key"
			entry.AuthConfigured = entry.HasKey
		}
		if entry.Enabled {
			if models, err := s.backendCatalogForOverview(r, bc.Type); err != nil {
				entry.CatalogOK = false
			} else {
				entry.CatalogOK = true
				entry.Models = make([]string, 0, len(models))
				for _, model := range models {
					entry.Models = append(entry.Models, bc.Type+"/"+model)
				}
				if credits, ok := s.backendModelCredits(bc.Type); ok {
					entry.ModelCredits = make(map[string]string, len(credits))
					for model, rate := range credits {
						entry.ModelCredits[bc.Type+"/"+model] = rate
					}
				}
			}
		}
		page.Backends = append(page.Backends, entry)
	}

	page.GrokUsage = s.grokUsageMetadata(r)
	page.ZcodeUsage = s.zcodeUsageMetadata(r)
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

func (s *Server) backendModelCredits(name string) (map[string]string, bool) {
	for _, candidate := range s.backends {
		if candidate.Name() != name {
			continue
		}
		provider, ok := candidate.(interface{ ModelCredits() map[string]string })
		if !ok {
			return nil, false
		}
		return provider.ModelCredits(), true
	}
	return nil, false
}

func (s *Server) grokUsageMetadata(r *http.Request) usageMetadata {
	configured := false
	for _, bc := range s.cfg.Backends {
		if bc.Type != grokUsageBackendName || !bc.IsEnabled() {
			continue
		}
		configured = true
		break
	}
	if !configured {
		return usageMetadata{}
	}
	if s.grokAuth == nil || !s.grokAuth.HasSession() {
		return usageMetadata{Configured: true}
	}
	usage, err := s.grokUsage(r.Context(), false)
	if err != nil {
		return usageMetadata{Configured: true, Available: false, Error: sanitizeUsageError(err)}
	}
	return usageMetadata{Configured: true, Available: usage.Available}
}

// zcodeUsageMetadata reports whether the dashboard should render the ZCode
// plan usage card. Like the grok metadata, it carries only presence and a
// sanitized failure reason — never the upstream response.
func (s *Server) zcodeUsageMetadata(r *http.Request) usageMetadata {
	configured := false
	for _, bc := range s.cfg.Backends {
		if bc.Type != zcodeUsageBackendName || !bc.IsEnabled() {
			continue
		}
		configured = true
		break
	}
	if !configured {
		return usageMetadata{}
	}
	if s.zcodeAuth == nil || !s.zcodeAuth.HasSession() {
		return usageMetadata{Configured: true}
	}
	if _, err := s.zcodeUsage(r.Context()); err != nil {
		return usageMetadata{Configured: true, Available: false, Error: err.Error()}
	}
	return usageMetadata{Configured: true, Available: true}
}

func (s *Server) zcodeUsage(ctx context.Context) ([]zcodebackend.PlanUsage, error) {
	if s.zcodeAuth == nil {
		return nil, errUsageUnavailable
	}

	s.zcodeUsageMu.Lock()
	defer s.zcodeUsageMu.Unlock()

	if !s.zcodeUsageAt.IsZero() && time.Since(s.zcodeUsageAt) < zcodeUsageTTL {
		return append([]zcodebackend.PlanUsage(nil), s.zcodeUsagePlans...), nil
	}

	plans, err := s.zcodeAuth.PlanUsage(ctx)
	if err != nil {
		return nil, err
	}
	s.zcodeUsagePlans = append([]zcodebackend.PlanUsage(nil), plans...)
	s.zcodeUsageAt = time.Now()
	return append([]zcodebackend.PlanUsage(nil), s.zcodeUsagePlans...), nil
}

func (s *Server) grokUsage(ctx context.Context, refresh bool) (grokbackend.UsageView, error) {
	if s.grokAuth == nil {
		return grokbackend.UsageView{}, errUsageUnavailable
	}
	if !refresh {
		if usage, ok := s.grokUsageSnapshot(); ok {
			return usage, nil
		}
	}
	s.grokUsageMu.Lock()
	if !refresh {
		if usage, ok := s.grokUsageSnapshotLocked(); ok {
			s.grokUsageMu.Unlock()
			return usage, nil
		}
	}
	baseURL := s.grokUsageBaseURL()
	usage, err := s.grokAuth.Usage(ctx, baseURL)
	if err != nil {
		s.grokUsageMu.Unlock()
		return grokbackend.UsageView{}, err
	}
	view := grokbackend.NewUsageView(usage, time.Now())
	s.grokUsageValue = &view
	s.grokUsageMu.Unlock()
	return view, nil
}

func (s *Server) grokUsageSnapshot() (grokbackend.UsageView, bool) {
	s.grokUsageMu.Lock()
	defer s.grokUsageMu.Unlock()
	return s.grokUsageSnapshotLocked()
}

func (s *Server) grokUsageSnapshotLocked() (grokbackend.UsageView, bool) {
	if s.grokUsageValue == nil || time.Since(s.grokUsageValue.FetchedAt) >= grokUsageTTL {
		return grokbackend.UsageView{}, false
	}
	return *s.grokUsageValue, true
}

func (s *Server) grokUsageBaseURL() string {
	for _, backendConfig := range s.cfg.Backends {
		if backendConfig.Type != grokUsageBackendName {
			continue
		}
		if backendConfig.BaseURL != "" {
			return strings.TrimRight(backendConfig.BaseURL, "/")
		}
	}
	return ""
}

func sanitizeUsageError(err error) string {
	message := err.Error()
	if idx := strings.Index(message, ": {"); idx >= 0 {
		message = message[:idx]
	}
	if idx := strings.Index(message, " body="); idx >= 0 {
		message = message[:idx]
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "Usage information is temporarily unavailable."
	}
	return message
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
