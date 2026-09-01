// Package abliteration implements the abliteration.ai inference backend.
// abliteration.ai exposes native OpenAI Chat Completions, OpenAI Responses,
// and Anthropic Messages APIs, plus a live model catalog.
package abliteration

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
	defaultBaseURL = "https://api.abliteration.ai/v1"

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

func (c *Client) Name() string { return "abliteration" }

func init() {
	backend.Register("abliteration", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports reports the native API shapes exposed by abliteration.ai.
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
		return nil, fmt.Errorf("abliteration backend has no API key configured")
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, fmt.Errorf("abliteration backend does not support kind %q", req.Kind)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	httpReq.Header.Set("Accept", accept)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to abliteration.ai failed: %w", err)
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
		return nil, fmt.Errorf("request to abliteration.ai failed: %w", err)
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
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("abliteration.ai returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Body: b}
}
