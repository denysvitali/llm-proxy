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

// codexModelMessages carries the base prompt codex substitutes for its own
// default instructions for a catalog entry.
type codexModelMessages struct {
	InstructionsTemplate string `json:"instructions_template"`
}

// codexTruncationPolicy bounds how aggressively codex compacts a thread.
type codexTruncationPolicy struct {
	Mode  string `json:"mode"` // "bytes" | "tokens"
	Limit int    `json:"limit"`
}

// codexReasoningLevel is one selectable reasoning effort.
type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

// codexServiceTier is one purchasable speed tier; proxied backends have
// none, but the field itself is part of the codex catalog schema.
type codexServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// codexModel is one entry of the Codex-flavored catalog served on
// /v1/models. Codex decodes GET {base}/models as {"models": [...]}
// (ModelsResponse); the OpenAI {"object":"list","data":[...]} envelope
// fails with "failed to decode models response: missing field `models`"
// and kills turn startup inside the app-server. The field set is the union
// of what the codex 0.144–0.150 catalog parsers require — extra fields are
// ignored, so the union decodes on every release in between (verified
// empirically with `codex debug models -c model_catalog_json=...` against
// each version). Metadata values are deliberately conservative: they only
// size codex's own context accounting, never the upstream request.
type codexModel struct {
	Slug                        string                `json:"slug"`
	DisplayName                 string                `json:"display_name"`
	Description                 string                `json:"description"`
	BaseInstructions            string                `json:"base_instructions"`
	ContextWindow               int                   `json:"context_window"`
	MaxContextWindow            int                   `json:"max_context_window"`
	EffectiveContextWindowPct   int                   `json:"effective_context_window_percent"`
	MaxOutputTokens             int                   `json:"max_output_tokens"`
	TruncationPolicy            codexTruncationPolicy `json:"truncation_policy"`
	Reasoning                   bool                  `json:"reasoning"`
	SupportedReasoningLevels    []codexReasoningLevel `json:"supported_reasoning_levels"`
	DefaultReasoningLevel       string                `json:"default_reasoning_level"`
	DefaultReasoningSummary     string                `json:"default_reasoning_summary"`
	DefaultVerbosity            string                `json:"default_verbosity"`
	SupportVerbosity            bool                  `json:"support_verbosity"`
	SupportsParallelToolCalls   bool                  `json:"supports_parallel_tool_calls"`
	SupportsReasoningSummaries  bool                  `json:"supports_reasoning_summaries"`
	SupportsImageDetailOriginal bool                  `json:"supports_image_detail_original"`
	SupportsSearchTool          bool                  `json:"supports_search_tool"`
	ShellType                   string                `json:"shell_type"`
	ApplyPatchToolType          string                `json:"apply_patch_tool_type"`
	WebSearchToolType           string                `json:"web_search_tool_type"`
	ToolMode                    string                `json:"tool_mode"`
	UseResponsesLite            bool                  `json:"use_responses_lite"`
	InputModalities             []string              `json:"input_modalities"`
	ExperimentalSupportedTools  []string              `json:"experimental_supported_tools"`
	AdditionalSpeedTiers        []string              `json:"additional_speed_tiers"`
	ServiceTiers                []codexServiceTier    `json:"service_tiers"`
	ModelMessages               codexModelMessages    `json:"model_messages"`
	CompHash                    string                `json:"comp_hash"`
	Visibility                  string                `json:"visibility"`
	SupportedInAPI              bool                  `json:"supported_in_api"`
	Priority                    int                   `json:"priority"`
	IncludeAppsUsageInstruct    bool                  `json:"include_apps_usage_instructions"`
	IncludePluginUsageInstruct  bool                  `json:"include_plugin_usage_instructions"`
	IncludeSkillsUsageInstruct  bool                  `json:"include_skills_usage_instructions"`
	NodeReplAutoReviewRequired  bool                  `json:"node_repl_auto_review_required"`
	NodeReplDisabled            bool                  `json:"node_repl_disabled"`
	// AvailabilityNux, Upgrade and MultiAgentVersion are nullable/optional
	// on every codex parser observed; nil marshals as the null those
	// parsers expect.
	AvailabilityNux *struct {
		Message string `json:"message"`
	} `json:"availability_nux"`
	Upgrade *struct {
		Model string `json:"model"`
	} `json:"upgrade"`
	MultiAgentVersion *string `json:"multi_agent_version"`
}

// codexModelsResponse is the envelope codex expects from GET /v1/models.
type codexModelsResponse struct {
	Models []codexModel `json:"models"`
}

// codexReasoningLevels and the constants below are the conservative
// metadata served for every proxied model: reasoning is advertised with the
// classic low/medium/high efforts, and context accounting assumes a
// 200k-token window with compaction at 170k so codex compacts early rather
// than overflowing providers with larger real limits.
var codexReasoningLevels = []codexReasoningLevel{
	{Effort: "low", Description: "Fast, shallow reasoning"},
	{Effort: "medium", Description: "Balanced reasoning"},
	{Effort: "high", Description: "Deep reasoning"},
}

const (
	codexContextWindow     = 200_000
	codexTruncationLimit   = 170_000
	codexMaxOutputTokens   = 32_768
	codexEffectiveWindowPc = 95
	// codexBaseInstructions mirrors the built-in prompt codex falls back to
	// for unknown models, so serving catalog entries does not silently
	// change an agent's personality.
	codexBaseInstructions = "You are a coding agent running in the Codex CLI, " +
		"a terminal-based coding assistant. You are expected to be precise, " +
		"safe, and helpful."
)

// newCodexModel synthesizes a catalog entry for one qualified model ID.
func newCodexModel(id, backend string) codexModel {
	return codexModel{
		Slug:                       id,
		DisplayName:                id,
		Description:                fmt.Sprintf("%s backend, routed by llm-proxy", backend),
		BaseInstructions:           codexBaseInstructions,
		ContextWindow:              codexContextWindow,
		MaxContextWindow:           codexContextWindow,
		EffectiveContextWindowPct:  codexEffectiveWindowPc,
		MaxOutputTokens:            codexMaxOutputTokens,
		TruncationPolicy:           codexTruncationPolicy{Mode: "tokens", Limit: codexTruncationLimit},
		Reasoning:                  true,
		SupportedReasoningLevels:   codexReasoningLevels,
		DefaultReasoningLevel:      "medium",
		DefaultReasoningSummary:    "none",
		DefaultVerbosity:           "medium",
		SupportVerbosity:           true,
		SupportsParallelToolCalls:  true,
		SupportsReasoningSummaries: true,
		ShellType:                  "shell_command",
		ApplyPatchToolType:         "freeform",
		WebSearchToolType:          "text",
		ToolMode:                   "tools",
		InputModalities:            []string{"text"},
		ExperimentalSupportedTools: []string{},
		AdditionalSpeedTiers:       []string{},
		ServiceTiers:               []codexServiceTier{},
		ModelMessages:              codexModelMessages{InstructionsTemplate: codexBaseInstructions},
		CompHash:                   "0",
		Visibility:                 "list",
		SupportedInAPI:             true,
		Priority:                   100,
	}
}

// isCodexClient reports whether the request comes from a Codex process.
// Codex identifies its HTTP traffic as codex_exec/<version>,
// codex_app_server/<version>, codex_tui/<version>, and so on, and also
// stamps an originator header with the same product token.
func isCodexClient(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "codex") ||
		strings.HasPrefix(strings.ToLower(r.Header.Get("Originator")), "codex")
}

// writeModels answers /v1/models in the caller's dialect: codex clients get
// the ModelsResponse envelope their turn-startup catalog refresh decodes,
// everything else keeps the OpenAI list envelope.
func writeModels(w http.ResponseWriter, r *http.Request, data []modelEntry) {
	if isCodexClient(r) {
		models := make([]codexModel, 0, len(data))
		for _, e := range data {
			models = append(models, newCodexModel(e.ID, e.OwnedBy))
		}
		writeJSON(w, http.StatusOK, codexModelsResponse{Models: models})
		return
	}
	writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
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
// backends. Every entry uses the canonical qualified "<backend>/<id>" form,
// which clients can use on any endpoint to pin one backend. One failing
// backend is logged and skipped; when every enabled backend fails the answer
// is 502.
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

	total := 0
	for _, c := range lists {
		total += len(c.models)
	}
	data := make([]modelEntry, 0, total)
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
		}
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	writeModels(w, r, data)
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
		data = append(data, modelEntry{ID: name + "/" + id, Object: "model", OwnedBy: name})
	}
	sort.Slice(data, func(i, j int) bool { return data[i].ID < data[j].ID })
	writeModels(w, r, data)
}
