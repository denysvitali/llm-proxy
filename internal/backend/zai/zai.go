// Package zai implements the Z.ai GLM Coding Plan backend. The plan is
// API-key based and served natively over Anthropic Messages and OpenAI
// Responses. Its Chat Completions endpoint is deliberately unused: it silently
// substitutes models (e.g. glm-4.7 is served as glm-5.3-flash), so Chat
// clients are translated by the server instead.
package zai

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
	// defaultBaseURL is the API root; the Coding Plan endpoints hang off it.
	defaultBaseURL = "https://api.z.ai/api"

	anthropicPath = "/anthropic/v1/messages"
	responsesPath = "/v1/responses"
	modelsPath    = "/coding/paas/v4/models"

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

func (c *Client) Name() string { return "zai" }

func init() {
	backend.Register("zai", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports reports the native API shapes of the Coding Plan.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindAnthropic || kind == backend.KindOpenAIResponses
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("zai backend has no API key configured")
	}
	var path string
	switch req.Kind {
	case backend.KindAnthropic:
		path = anthropicPath
	case backend.KindOpenAIResponses:
		path = responsesPath
	default:
		return nil, fmt.Errorf("zai backend does not support kind %q", req.Kind)
	}
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+path, req.RawBody, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	return upstream.SendInference(c.HTTP, httpReq, "Z.ai", c.BaseURL, req.TracePropagation)
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := upstream.CatalogRequest(ctx, c.BaseURL+modelsPath, c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, httpReq, "Z.ai")
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
	return fmt.Sprintf("Z.ai returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}
