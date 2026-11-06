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
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
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
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+path, req.RawBody, req.Streaming)
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
	return upstream.SendInference(c.HTTP, httpReq, "Open Design", c.BaseURL, req.TracePropagation)
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
	models, err := enabledModelIDs(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return models, nil
}

// enabledModelIDs extracts catalog IDs, skipping entries the account cannot
// use. Open Design marks each model with "enabled"; a missing flag counts as
// enabled so a catalog without it still lists everything.
func enabledModelIDs(data []byte) ([]string, error) {
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			Enabled *bool  `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		if model.ID == "" || (model.Enabled != nil && !*model.Enabled) {
			continue
		}
		models = append(models, model.ID)
	}
	return models, nil
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Open Design returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}
