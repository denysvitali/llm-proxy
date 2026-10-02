// Package apodex implements the Apodex backend (platform.apodex.ai). Apodex
// exposes Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses
// endpoints. Responses requests are routed through Chat translation because
// Apodex's Responses compatibility is not sufficient for Codex conversation
// history and opaque reasoning state.
package apodex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
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
		// Deep-research responses can take roughly 600 seconds, so wait
		// beyond that without imposing an overall streaming deadline.
		HTTP: upstream.NewClient(15 * time.Minute),
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

	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+path, body, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	return upstream.Send(c.HTTP, httpReq, "Apodex")
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

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := upstream.CatalogRequest(ctx, c.BaseURL+"/models", c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, httpReq, "Apodex")
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
	return fmt.Sprintf("Apodex returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Body: b}
}
