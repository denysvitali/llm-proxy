// Package nous implements the Nous Portal inference backend
// (portal.nousresearch.com). The Portal exposes an OpenAI-compatible API at
// inference-api.nousresearch.com serving chat completions, legacy completions,
// embeddings, and the model catalog, so chat-completions requests pass through
// with the upstream key swapped in.
package nous

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

const defaultBaseURL = "https://inference-api.nousresearch.com/v1"

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
		HTTP:    upstream.NewClient(5 * time.Minute),
	}
}

func (c *Client) Name() string { return "nous" }

func init() {
	backend.Register("nous", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

// Supports: the Portal serves /chat/completions natively; Anthropic requests
// are translated by the server's translate package before reaching Send. The
// upstream has no Responses API endpoint.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindOpenAIChat
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, fmt.Errorf("nous backend has no API key configured")
	}
	switch req.Kind {
	case backend.KindOpenAIChat:
	default:
		return nil, fmt.Errorf("nous backend does not support kind %q", req.Kind)
	}
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+"/chat/completions", req.RawBody, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	return upstream.Send(c.HTTP, httpReq, "Nous Portal")
}

func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := upstream.CatalogRequest(ctx, c.BaseURL+"/models", c.Key)
	if err != nil {
		return nil, err
	}
	resp, err := upstream.ReadCatalog(c.HTTP, httpReq, "Nous Portal")
	if err != nil {
		return nil, err
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, &HTTPError{Status: resp.Status, Body: resp.Body}
	}
	return upstream.ModelIDs(resp.Body)
}

type HTTPError struct {
	Status int
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Nous Portal returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Body: b}
}
