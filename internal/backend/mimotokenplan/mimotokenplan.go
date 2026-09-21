// Package mimotokenplan implements Xiaomi MiMo Token Plan access through its
// OpenAI-compatible API. The provider's Anthropic compatibility API is
// deliberately not used; Anthropic clients are translated to an OpenAI wire
// format by the server.
package mimotokenplan

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const defaultBaseURL = "https://token-plan-sgp.xiaomimimo.com/v1"

var models = []string{
	"mimo-v2.5-pro",
	"mimo-v2.5",
}

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
		HTTP: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 15 * time.Minute,
			},
		},
	}
}

func init() {
	backend.Register("mimo-token-plan", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

func (c *Client) Name() string { return "mimo-token-plan" }

// Supports intentionally excludes Anthropic Messages. MiMo's OpenAI-compatible
// Chat Completions and Responses endpoints are used for every client protocol.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindOpenAIChat, backend.KindOpenAIResponses:
		return true
	default:
		return false
	}
}

func endpoint(kind backend.Kind) (string, bool) {
	switch kind {
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
		return nil, fmt.Errorf("MiMo Token Plan backend has no API key configured")
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, fmt.Errorf("MiMo Token Plan backend does not support kind %q", req.Kind)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	// Token Plan documentation specifies api-key for tp- credentials.
	httpReq.Header.Set("api-key", c.Key)
	httpReq.Header.Set("Content-Type", "application/json")
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	httpReq.Header.Set("Accept", accept)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to MiMo Token Plan failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// Models returns the current Token Plan language models. Speech models use
// separate APIs that llm-proxy does not expose.
func (c *Client) Models(context.Context) ([]string, error) {
	return append([]string(nil), models...), nil
}
