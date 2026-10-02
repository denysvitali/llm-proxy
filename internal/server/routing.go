package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// route is a resolved routing decision: which backend receives the request
// and under which upstream model name.
type route struct {
	backend backend.Backend
	model   string
}

// normalizeCodexModelSelector strips Codex's optional reasoning-effort suffix
// from a qualified model ID before routing. The original selector remains the
// client-facing model name; only the upstream route uses the bare model.
func normalizeCodexModelSelector(model string) (string, string, error) {
	prefix, rest, found := strings.Cut(model, "/")
	if !found || prefix != "codex" || !strings.Contains(rest, ":") {
		return model, "", nil
	}
	colon := strings.LastIndexByte(rest, ':')
	if colon <= 0 || colon == len(rest)-1 {
		return "", "", fmt.Errorf("invalid Codex model selector %q: expected codex/<model>:<effort>", model)
	}
	base, effort := rest[:colon], rest[colon+1:]
	switch effort {
	case "low", "medium", "high", "xhigh":
		return prefix + "/" + base, effort, nil
	default:
		return "", "", fmt.Errorf("unsupported Codex reasoning effort %q in model selector %q", effort, model)
	}
}

// stripClaudeContextSuffix removes the context-window marker Happy appends to
// Claude model selections. It is client-side selection metadata, not part of
// the upstream provider's model ID.
func stripClaudeContextSuffix(model string) string {
	return strings.TrimSuffix(model, "[1m]")
}

// resolveWithFallbacks maps an inbound model name to a backend + upstream
// model, and reports the fallback entries attached to the route entry that
// matched (explicit route or default route; qualified IDs and catalog
// matches carry none of their own).
//
// A "<backend>/<model>" ID has highest precedence: it addresses one backend
// directly, bypassing routes, catalogs and DefaultRoute (split at the first
// "/", so nested upstream names like "nousresearch/hermes-4-70b" on backend
// "nous" work). Otherwise: explicit Routes entry, then live catalogs of
// enabled backends in config order, then DefaultRoute. ok=false means no
// route exists; callers answer 404 (model not found).
func (s *Server) resolveWithFallbacks(ctx context.Context, model string) (route, []config.FallbackRoute, bool) {
	if prefix, rest, found := strings.Cut(model, "/"); found && rest != "" {
		if b, known := s.byName[prefix]; known {
			if !s.enabled(prefix) {
				return route{}, nil, false
			}
			return route{backend: b, model: rest}, nil, true
		}
	}
	if r, ok := s.cfg.Routes[model]; ok {
		if b, known := s.byName[r.Backend]; known && s.enabled(r.Backend) {
			upstream := r.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, r.Fallbacks, true
		}
	}
	for _, b := range s.backends {
		if !s.enabled(b.Name()) {
			continue
		}
		models, err := s.catalog(ctx, b)
		if err != nil {
			s.log.WithError(err).WithField("backend", b.Name()).Warn("catalog fetch failed")
			continue
		}
		if hasModel(models, model) {
			return route{backend: b, model: model}, nil, true
		}
	}
	if d := s.cfg.DefaultRoute; d.Backend != "" {
		if b, known := s.byName[d.Backend]; known && s.enabled(d.Backend) {
			upstream := d.Model
			if upstream == "" {
				upstream = model
			}
			return route{backend: b, model: upstream}, d.Fallbacks, true
		}
	}
	return route{}, nil, false
}

// maxRouteChain bounds how many backends one request may visit: the primary
// route plus up to three fallbacks.
const maxRouteChain = 4

// resolveChain resolves the primary route and appends the fallbacks that
// apply to it. Fallbacks are taken from the matched route entry, the
// DefaultRoute, and the primary backend's own fallback list. A qualified
// "<backend>/<model>" ID pins the request to that exact backend: the caller
// asked for that upstream specifically, so the backend's own `fallbacks:`
// list is skipped. The model is still forwarded even when it is absent from
// the backend catalog: catalogs are discovery hints, not an allowlist, and
// providers such as Grok may accept models before they publish them. Falling
// back to a different provider on a 502/503 from the pinned upstream silently
// reroutes a deliberate choice to an unrelated model (e.g.
// opencode-go/glm-5.3-flash → zcode/glm-5.3-flash on a zcode fallback, which
// fails a different quota and confuses the user about which upstream is slow).
// Fallbacks naming unknown, disabled or repeated backends are skipped; an
// empty model rewrite keeps the primary's upstream model.
func (s *Server) resolveChain(ctx context.Context, model string) ([]route, bool) {
	normalized, _, err := normalizeCodexModelSelector(model)
	if err != nil {
		return nil, false
	}
	normalized = stripClaudeContextSuffix(normalized)
	primary, fallbacks, ok := s.resolveWithFallbacks(ctx, normalized)
	if !ok {
		return nil, false
	}
	chain := s.appendFallbacks([]route{primary}, fallbacks, primary.model)
	if _, pinned := qualifiedPin(normalized, primary.backend.Name()); !pinned {
		if bc, ok := s.cfg.BackendByType(primary.backend.Name()); ok {
			chain = s.appendFallbacks(chain, bc.Fallbacks, primary.model)
		}
	}
	return chain, true
}

// qualifiedPin reports whether model is a qualified "<backend>/<model>" ID
// that pinned backendName (split at the first slash, mirroring
// resolveWithFallbacks), and returns the upstream model remainder.
func qualifiedPin(model, backendName string) (string, bool) {
	prefix, rest, found := strings.Cut(model, "/")
	if !found || rest == "" || prefix != backendName {
		return "", false
	}
	return rest, true
}

// appendFallbacks appends the fallback entries that can serve the request to
// chain: unknown, disabled and duplicate backends are skipped, the
// maxRouteChain cap holds, and an empty model rewrite keeps the primary's
// upstream model.
func (s *Server) appendFallbacks(chain []route, fallbacks []config.FallbackRoute, primaryModel string) []route {
	for _, f := range fallbacks {
		if len(chain) >= maxRouteChain {
			break
		}
		b, known := s.byName[f.Backend]
		if !known || !s.enabled(f.Backend) {
			continue
		}
		duplicate := false
		for _, rt := range chain {
			if rt.backend.Name() == b.Name() {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		upstream := f.Model
		if upstream == "" {
			upstream = primaryModel
		}
		chain = append(chain, route{backend: b, model: upstream})
	}
	return chain
}

func (s *Server) enabled(name string) bool {
	bc, ok := s.cfg.BackendByType(name)
	if !ok {
		return false
	}
	return bc.IsEnabled()
}

// hasModel reports whether the list contains an exact match, ignoring a
// trailing -YYYYMMDD date suffix on catalog entries (Anthropic-style dated
// snapshots: a dash followed by exactly eight ASCII digits forming a valid
// calendar date).
func hasModel(models []string, want string) bool {
	for _, m := range models {
		if m == want {
			return true
		}
		if len(m) > 9 && m[len(m)-9] == '-' && m[:len(m)-9] == want && isDatedSnapshotSuffix(m[len(m)-8:]) {
			return true
		}
	}
	return false
}

// isDatedSnapshotSuffix reports whether s is exactly eight ASCII digits that
// parse as a valid YYYYMMDD date (e.g. 20250514).
func isDatedSnapshotSuffix(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < 8; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, err := time.Parse("20060102", s)
	return err == nil
}

// sortedRoutes returns route keys deterministically for the dashboard.
func (s *Server) sortedRoutes() []string {
	keys := make([]string, 0, len(s.cfg.Routes))
	for k := range s.cfg.Routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
