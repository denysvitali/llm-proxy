// Package venice implements the Venice AI backend. Venice exposes an
// OpenAI-compatible API, so chat-completions requests pass through with the
// upstream key swapped in.
package venice

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

const defaultBaseURL = "https://api.venice.ai/api/v1"

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
				ResponseHeaderTimeout: 5 * time.Minute,
			},
		},
	}
}

func (c *Client) Name() string { return "venice" }

// Supports: Venice exposes OpenAI Chat Completions natively; Anthropic and
// Responses requests are translated or rejected by the server.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindOpenAIChat
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("venice backend has no API key configured")
	}
	var path string
	switch req.Kind {
	case backend.KindOpenAIChat:
		path = "/chat/completions"
	case backend.KindOpenAIResponses:
		path = "/responses"
	default:
		return nil, fmt.Errorf("venice backend does not support kind %q", req.Kind)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if req.Streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Venice failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Venice failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: data}
	}
	var list modelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
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
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Venice returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Body: b}
}
