// Package opendesign implements the Open Design Cloud (AMR) inference
// backend. Open Design exposes native OpenAI Chat Completions, OpenAI
// Responses, and Anthropic Messages APIs at amr-link.open-design.ai, plus a
// live per-account model catalog.
package opendesign

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
	"github.com/denysvitali/llm-proxy/internal/translate"
)

const (
	defaultBaseURL = "https://amr-link.open-design.ai/v1"

	// anthropicVersion is the API version header expected on /messages.
	anthropicVersion = "2023-06-01"
)

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client

	mu   sync.RWMutex
	info map[string]backend.ModelInfo
}

func New(baseURL, key string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		HTTP:    upstream.NewClient(15 * time.Minute),
	}
}

func (c *Client) Name() string { return "opendesign" }

func init() {
	backend.Register("opendesign", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports reports the native API shapes exposed by Open Design.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses:
		return true
	default:
		return false
	}
}

func endpoint(kind backend.Kind) (string, bool) {
	switch kind {
	case backend.KindAnthropic:
		return "/messages", true
	case backend.KindOpenAIChat:
		return "/chat/completions", true
	case backend.KindOpenAIResponses:
		return "/responses", true
	default:
		return "", false
	}
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, backend.Terminal(fmt.Errorf("opendesign backend has no API key configured"))
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, backend.Terminal(fmt.Errorf("opendesign backend does not support kind %q", req.Kind))
	}
	body := req.RawBody
	if req.Kind == backend.KindAnthropic {
		// Open Design's origin drops the connection (Cloudflare answers an
		// HTML 502) on any tool_reference block, which Claude Code sends after
		// every tool search; that poisons the whole session, since each retry
		// replays the same history. Inline the references as text.
		// A body that does not decode is forwarded as-is for the upstream
		// to judge.
		if rewritten, _, err := translate.InlineToolReferences(body); err == nil {
			body = rewritten
		}
	}
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+path, body, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	if req.Kind == backend.KindAnthropic {
		version := req.Header.Get("Anthropic-Version")
		if version == "" {
			version = anthropicVersion
		}
		httpReq.Header.Set("Anthropic-Version", version)
		httpReq.Header.Set("X-Api-Key", c.Key)
		if beta := req.Header.Get("Anthropic-Beta"); beta != "" {
			httpReq.Header.Set("Anthropic-Beta", beta)
		}
	}
	resp, err := upstream.SendInference(c.HTTP, httpReq, "Open Design", c.BaseURL, req.TracePropagation)
	if err != nil {
		return nil, err
	}
	if req.Kind == backend.KindAnthropic && req.Streaming && resp.Status >= 200 && resp.Status < 300 &&
		strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = newMaxTokensCompleter(resp.Body, body)
	}
	return resp, nil
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := upstream.CatalogRequest(ctx, c.BaseURL+"/models", c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, httpReq, "Open Design")
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, &HTTPError{Status: resp.Status, Body: resp.Body}
	}
	models, info, err := parseCatalog(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return models, nil
}

// ModelInfo reports the limits Open Design declared for model in the most
// recent catalog fetch.
func (c *Client) ModelInfo(model string) (backend.ModelInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.info[model]
	return info, ok
}

// catalogEntry is one /models item. metadata.context_limit is the model's
// window and metadata.output_budget its answer ceiling; context_budget only
// bounds the prompt and is deliberately not used as either.
type catalogEntry struct {
	ID           string `json:"id"`
	Enabled      *bool  `json:"enabled"`
	Architecture struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
	Metadata struct {
		ContextLimit int `json:"context_limit"`
		OutputBudget int `json:"output_budget"`
	} `json:"metadata"`
}

// parseCatalog extracts catalog IDs and their declared limits, skipping
// entries the account cannot use. Open Design marks each model with
// "enabled"; a missing flag counts as enabled.
func parseCatalog(data []byte) ([]string, map[string]backend.ModelInfo, error) {
	var list struct {
		Data []catalogEntry `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, nil, err
	}
	models := make([]string, 0, len(list.Data))
	info := make(map[string]backend.ModelInfo, len(list.Data))
	for _, entry := range list.Data {
		if entry.ID == "" || (entry.Enabled != nil && !*entry.Enabled) {
			continue
		}
		models = append(models, entry.ID)
		mi := backend.ModelInfo{InputModalities: entry.Architecture.InputModalities}
		if entry.Metadata.ContextLimit > 0 {
			mi.ContextWindow = entry.Metadata.ContextLimit
		}
		if entry.Metadata.OutputBudget > 0 && (mi.ContextWindow == 0 || entry.Metadata.OutputBudget <= mi.ContextWindow) {
			mi.MaxOutputTokens = entry.Metadata.OutputBudget
		}
		info[entry.ID] = mi
	}
	return models, info, nil
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Open Design returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}
