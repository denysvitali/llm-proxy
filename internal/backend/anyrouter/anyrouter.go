// Package anyrouter implements the AnyRouter inference backend.
// AnyRouter exposes native OpenAI Chat Completions, OpenAI Responses,
// and Anthropic Messages APIs, plus a live model catalog.
package anyrouter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

const (
	defaultBaseURL = "https://anyrouter.dev/api/v1"

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

func (c *Client) Name() string { return "anyrouter" }

func init() {
	backend.Register("anyrouter", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports reports the native API shapes exposed by AnyRouter.
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
		return nil, backend.Terminal(fmt.Errorf("anyrouter backend has no API key configured"))
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, backend.Terminal(fmt.Errorf("anyrouter backend does not support kind %q", req.Kind))
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
	return upstream.Send(c.HTTP, httpReq, "AnyRouter")
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := upstream.CatalogRequest(ctx, c.BaseURL+"/models", c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, httpReq, "AnyRouter")
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, &HTTPError{Status: resp.Status, Body: resp.Body}
	}
	models, err := upstream.ModelIDs(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return models, nil
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("AnyRouter returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}
