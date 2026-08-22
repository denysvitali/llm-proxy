// Package opencode implements the OpenCode Zen backend. Zen natively serves
// both the Anthropic Messages API (/messages) and the OpenAI Chat Completions
// API (/chat/completions), so both request kinds pass through byte-for-byte
// with the upstream key swapped in.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	defaultBaseURL = "https://opencode.ai/zen/v1"

	// anthropicVersion is the API version header Zen expects on /messages.
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
		HTTP: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 10 * time.Minute,
			},
		},
	}
}

// HasAPIKey reports whether an upstream API key is configured. Without one,
// Zen only serves its free models.
func (c *Client) HasAPIKey() bool {
	return c.Key != ""
}

func init() {
	backend.Register("opencode", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

func (c *Client) Name() string { return "opencode" }

// Supports: Zen exposes /messages and /chat/completions natively, so both
// shapes pass through untouched. The OpenAI Responses API is translated by
// the server instead.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindAnthropic, backend.KindOpenAIChat:
		return true
	case backend.KindOpenAIResponses:
		return false
	default:
		return false
	}
}

// Do performs a request against the Zen API, attaching the bearer token when
// a key is configured. A nil body means no request body is sent.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, accept string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	}
	httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		httpReq.Header.Set("Accept", accept)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenCode Zen failed: %w", err)
	}
	return resp, nil
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	var path string
	switch req.Kind {
	case backend.KindAnthropic:
		path = "/messages"
	case backend.KindOpenAIChat:
		path = "/chat/completions"
	default:
		return nil, fmt.Errorf("opencode backend does not support kind %q", req.Kind)
	}
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	resp, err := c.Do(ctx, http.MethodPost, path, req.RawBody, accept)
	if err != nil {
		return nil, err
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Models lists the models the account can use through Zen. The catalog
// endpoint is public, so this works with or without a key.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/models", nil, "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data}
	}
	var list modelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return models, nil
}

type HTTPError struct {
	Status int
	Header http.Header
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OpenCode Zen returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
}
