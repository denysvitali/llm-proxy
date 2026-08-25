// Package apodex implements the Apodex backend (platform.apodex.ai). Apodex
// exposes Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses
// endpoints. Responses requests are routed through Chat translation because
// Apodex's Responses compatibility is not sufficient for Codex conversation
// history and opaque reasoning state.
package apodex

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
	"github.com/denysvitali/llm-proxy/internal/translate"
)

const (
	defaultBaseURL = "https://api.apodex.ai/v1"

	// anthropicVersion is the API version header Apodex expects on /messages.
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
				Proxy:           http.ProxyFromEnvironment,
				MaxIdleConns:    100,
				IdleConnTimeout: 90 * time.Second,
				// Apodex allows a non-streaming request roughly 600 seconds of
				// wall clock before it gives up; the deep-research models
				// routinely use it, so wait past that rather than cutting a
				// live answer short.
				ResponseHeaderTimeout: 15 * time.Minute,
			},
		},
	}
}

func (c *Client) Name() string { return "apodex" }

func init() {
	backend.Register("apodex", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports reports the API shapes exposed by Apodex. SupportsModel refines
// Responses support for normal server routing.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses:
		return true
	default:
		return false
	}
}

// SupportsModel forces Responses clients through the proxy's Chat adapter.
// Apodex's /responses endpoint rejects valid Codex requests when instructions
// and prompt-role history coexist, and its opaque reasoning state cannot be
// replayed reliably by Codex. The Chat adapter hoists prompt roles, drops
// provider-specific reasoning history, and preserves namespaced tool calls.
func (c *Client) SupportsModel(kind backend.Kind, _ string) bool {
	if kind == backend.KindOpenAIResponses {
		return false
	}
	return c.Supports(kind)
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("apodex backend has no API key configured")
	}
	var path string
	switch req.Kind {
	case backend.KindAnthropic:
		path = "/messages"
	case backend.KindOpenAIChat:
		path = "/chat/completions"
	case backend.KindOpenAIResponses:
		path = "/responses"
	default:
		return nil, fmt.Errorf("apodex backend does not support kind %q", req.Kind)
	}

	body := req.RawBody
	if req.Kind == backend.KindOpenAIResponses {
		normalized, err := translate.NormalizeResponsesRequest(body)
		if err != nil {
			return nil, fmt.Errorf("normalize Apodex Responses request: %w", err)
		}
		body = normalized
	}
	if req.Kind != backend.KindAnthropic {
		body = withExplicitStream(body, req.Streaming)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	httpReq.Header.Set("Accept", "application/json")
	if req.Streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Apodex failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// withExplicitStream pins the "stream" field to what the proxy decided the
// client asked for. Apodex defaults stream to true on /chat/completions and
// /responses for its deep-research models — the opposite of OpenAI — so a body
// that simply omits the field would come back as SSE to a client waiting for
// one JSON object. A body that is not a JSON object is left alone so Apodex
// gets to reject it with its own message.
func withExplicitStream(body []byte, streaming bool) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return body
	}
	encoded, err := json.Marshal(streaming)
	if err != nil {
		return body
	}
	fields["stream"] = encoded
	out, err := json.Marshal(fields)
	if err != nil {
		return body
	}
	return out
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
		return nil, fmt.Errorf("request to Apodex failed: %w", err)
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
	return fmt.Sprintf("Apodex returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Body: b}
}
