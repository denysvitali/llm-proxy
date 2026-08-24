package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"sort"
)

// Build identity shown on the dashboard.
const (
	proxyName    = "llm-proxy"
	proxyVersion = "0.1.0"
)

// dashBackend is one row of the backend table, plus the grouped catalog
// listing shown under it. It never carries key material — only HasKey.
type dashBackend struct {
	Name      string   `json:"name"`
	Enabled   bool     `json:"enabled"`
	Host      string   `json:"host"`
	HasKey    bool     `json:"hasKey"`
	Models    []string `json:"models"`
	CatalogOK bool     `json:"catalogOK"`
}

// dashRoute is one row of the routing table. An empty Upstream means the
// requested model name is forwarded unchanged.
type dashRoute struct {
	Model    string `json:"model"`
	Backend  string `json:"backend"`
	Upstream string `json:"upstream"`
}

// dashboardPage carries everything the dashboard template renders.
type dashboardPage struct {
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Listen        string        `json:"listen"`
	AuthEnabled   bool          `json:"authEnabled"`
	Backends      []dashBackend `json:"backends"`
	Routes        []dashRoute   `json:"routes"`
	Stats         []ModelStat   `json:"stats,omitempty"`
	HasDefault    bool          `json:"hasDefault"`
	DefaultRoute  dashRoute     `json:"defaultRoute"`
	ExampleModel  string        `json:"exampleModel"`
	ClaudeSnippet string        `json:"claudeSnippet"`
	CodexSnippet  string        `json:"codexSnippet"`
}

// handleDashboard serves GET / — status page with routing and client setup.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := dashboardTemplate.Execute(&buf, s.buildDashboardPage(r)); err != nil {
		s.log.WithError(err).Error("render dashboard")
		http.Error(w, "failed to render dashboard", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = buf.WriteTo(w)
}

// handleOverview serves GET /api/overview: the dashboard page's data as JSON
// for the SPA. Key material is never included — only its presence.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.buildDashboardPage(r))
}

// buildDashboardPage collects dashboard data. Upstream API keys are never
// included in the page; only their presence is reported.
func (s *Server) buildDashboardPage(r *http.Request) dashboardPage {
	page := dashboardPage{
		Name:        proxyName,
		Version:     proxyVersion,
		Listen:      s.cfg.Server.Listen,
		AuthEnabled: s.auth != nil,
		Backends:    make([]dashBackend, 0, len(s.cfg.Backends)),
		Routes:      make([]dashRoute, 0, len(s.cfg.Routes)),
	}

	for _, bc := range s.cfg.Backends {
		entry := dashBackend{
			Name:    bc.Type,
			Enabled: bc.IsEnabled(),
			Host:    baseURLHost(bc.BaseURL),
			HasKey:  bc.ResolveKey(os.Getenv) != "",
		}
		if entry.Enabled {
			if models, err := s.dashboardCatalog(r, bc.Type); err != nil {
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
		page.Routes = append(page.Routes, dashRoute{
			Model:    name,
			Backend:  rt.Backend,
			Upstream: rt.Model,
		})
	}
	if d := s.cfg.DefaultRoute; d.Backend != "" {
		page.HasDefault = true
		page.DefaultRoute = dashRoute{Model: "anything unmatched", Backend: d.Backend, Upstream: d.Model}
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

// dashboardCatalog lists one backend's models, sorted. A configured backend
// without a constructed instance counts as unavailable.
func (s *Server) dashboardCatalog(r *http.Request, name string) ([]string, error) {
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

// exampleModel picks a representative model for the setup snippets: the first
// enabled backend's first model, falling back to a placeholder.
func exampleModel(backends []dashBackend) string {
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
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// dashboardFuncs formats stat floats for the model-stats table.
var dashboardFuncs = template.FuncMap{
	"pct": func(f float64) string { return fmt.Sprintf("%.1f%%", f*100) },
	"sec": func(f float64) string { return fmt.Sprintf("%.2fs", f) },
	"tps": func(f float64) string { return fmt.Sprintf("%.0f", f) },
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(dashboardFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Name}}</title>
<style>
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body { margin: 0; font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
  background: #f5f6f8; color: #1c1f23; }
main { max-width: 900px; margin: 0 auto; padding: 24px 16px 48px; }
h1 { font-size: 1.5rem; margin: 0; }
header p { color: #5a6270; margin: 4px 0 0; }
h2 { font-size: 1.05rem; margin: 0 0 12px; border-bottom: 1px solid #e2e5ea; padding-bottom: 6px; }
section { background: #fff; border: 1px solid #e2e5ea; border-radius: 10px;
  padding: 16px 20px; margin-top: 16px; }
table { width: 100%; border-collapse: collapse; font-size: .9rem; }
th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid #eceef1; }
th { color: #5a6270; font-weight: 600; }
tr:last-child td { border-bottom: 0; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: .85em; background: #eef0f3; padding: 1px 5px; border-radius: 4px; }
pre { background: #16181c; color: #e8eaed; padding: 14px 16px; border-radius: 8px;
  overflow-x: auto; font-size: .82rem; line-height: 1.5; }
pre code { background: none; padding: 0; }
ul.models { columns: 2 220px; gap: 24px; margin: 8px 0 0; padding-left: 20px; }
li { margin: 2px 0; }
h3 { margin: 12px 0 2px; font-size: .92rem; }
p { margin: 6px 0 0; }
.muted { color: #5a6270; }
.error { color: #a12727; }
.ok-badge, .off-badge { font-size: .78rem; padding: 1px 8px; border-radius: 999px; }
.ok-badge { background: #dcf3e3; color: #17693a; }
.off-badge { background: #f6dfdf; color: #8c2626; }
@media (prefers-color-scheme: dark) {
  body { background: #0f1114; color: #e8eaed; }
  section { background: #17191d; border-color: #2a2d33; }
  h2 { border-color: #2a2d33; }
  th, td { border-color: #23262c; }
  th, .muted, header p { color: #9aa2ad; }
  code { background: #22252b; }
  pre { background: #0b0c0f; }
}
</style>
</head>
<body>
<main>
<header>
<h1>{{.Name}}</h1>
<p>v{{.Version}} &middot; listening on <code>{{.Listen}}</code> &middot; authentication{{if .AuthEnabled}} enabled{{else}} disabled{{end}}</p>
</header>

<section>
<h2>Backends</h2>
<table>
<thead><tr><th>Name</th><th>Enabled</th><th>Base URL host</th><th>API key</th></tr></thead>
<tbody>
{{- range .Backends}}
<tr><td>{{.Name}}</td><td>{{if .Enabled}}<span class="ok-badge">yes</span>{{else}}<span class="off-badge">no</span>{{end}}</td><td><code>{{.Host}}</code></td><td>{{if .HasKey}}set{{else}}none{{end}}</td></tr>
{{- end}}
</tbody>
</table>
</section>

<section>
<h2>Model catalog</h2>
{{- range .Backends}}
<h3>{{.Name}}</h3>
{{- if not .Enabled}}
<p class="muted">backend disabled</p>
{{- else if not .CatalogOK}}
<p class="error">catalog unavailable</p>
{{- else if not .Models}}
<p class="muted">no models reported</p>
{{- else}}
<ul class="models">
{{- range .Models}}<li><code>{{.}}</code></li>{{end}}
</ul>
{{- end}}
{{- end}}
</section>

<section>
<h2>Routing</h2>
{{- if or .Routes .HasDefault}}
<table>
<thead><tr><th>Requested model</th><th>Backend</th><th>Upstream model</th></tr></thead>
<tbody>
{{- range .Routes}}
<tr><td><code>{{.Model}}</code></td><td>{{.Backend}}</td><td>{{if .Upstream}}<code>{{.Upstream}}</code>{{else}}<span class="muted">as requested</span>{{end}}</td></tr>
{{- end}}
{{- if .HasDefault}}
<tr><td><em>anything unmatched</em></td><td>{{.DefaultRoute.Backend}}</td><td>{{if .DefaultRoute.Upstream}}<code>{{.DefaultRoute.Upstream}}</code>{{else}}<span class="muted">as requested</span>{{end}}</td></tr>
{{- end}}
</tbody>
</table>
{{- else}}
<p class="muted">No explicit routes configured.</p>
{{- end}}
</section>

<section>
<h2>Model stats</h2>
<p class="muted">Per-model traffic since proxy start. Full percentiles in the <a href="/stats"><code>/stats</code></a> JSON; raw histograms under <code>/metrics</code>.</p>
{{- if not .Stats}}
<p class="muted">No model traffic recorded yet.</p>
{{- else}}
<table>
<thead><tr><th>Backend / model</th><th>Requests</th><th>Uptime</th><th>TTFT p50 / p99</th><th>E2E p50 / p99</th><th>tok/s p50</th><th>Cache hit</th><th>Tool calls</th><th>Tool err rate</th></tr></thead>
<tbody>
{{- range .Stats}}
<tr>
<td><code>{{.Backend}}/{{.Model}}</code></td>
<td>{{.Requests}}</td>
<td>{{pct .Uptime}}</td>
<td>{{sec .TTFT.P50}} / {{sec .TTFT.P99}}</td>
<td>{{sec .E2E.P50}} / {{sec .E2E.P99}}</td>
<td>{{tps .Throughput.P50}}</td>
<td>{{pct .CacheRate}}</td>
<td>{{.ToolCalls}}</td>
<td>{{pct .ToolErrorRate}}</td>
</tr>
{{- end}}
</tbody>
</table>
{{- end}}
</section>

<section>
<h2>Claude Code</h2>
<pre><code>{{.ClaudeSnippet}}</code></pre>
<p class="muted">{{if .AuthEnabled}}Replace <code>&lt;key&gt;</code> with one of your proxy API keys (llx_&hellip;).{{else}}Authentication is disabled; any token value works.{{end}}</p>
</section>

<section>
<h2>Codex</h2>
<pre><code>{{.CodexSnippet}}</code></pre>
<p class="muted">Set <code>LLM_PROXY_API_KEY</code> in your environment{{if .AuthEnabled}} to one of your proxy API keys{{end}}.</p>
</section>
</main>
</body>
</html>`))
