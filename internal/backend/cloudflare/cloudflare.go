// Package cloudflare implements Cloudflare's account-scoped Chat Completions
// and Responses endpoints. Anthropic clients use the server's Chat translation.
package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

func New(baseURL, key string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("cloudflare backend requires an absolute HTTP(S) base_url without credentials, query or fragment")
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		HTTP:    upstream.NewClient(15 * time.Minute),
	}, nil
}

func init() {
	backend.Register("cloudflare", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey)
	})
}

func (c *Client) Name() string { return "cloudflare" }

func (c *Client) Supports(kind backend.Kind) bool {
	_, ok := endpoint(kind)
	return ok
}

func endpoint(kind backend.Kind) (string, bool) {
	// The current model returns Chat wire data even on /messages. Do not
	// advertise native Anthropic support: the server must translate instead.
	switch kind {
	case backend.KindOpenAIChat:
		return "/chat/completions", true
	case backend.KindOpenAIResponses:
		return "/responses", true
	default:
		return "", false
	}
}

// Models returns the known inference model without assuming a discovery API.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []string{"stealth/union-alpha"}, nil
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, backend.Terminal(fmt.Errorf("cloudflare backend has no API key configured"))
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, backend.Terminal(fmt.Errorf("cloudflare backend does not support kind %q", req.Kind))
	}
	httpReq, err := upstream.JSONRequest(ctx, c.BaseURL+path, req.RawBody, req.Streaming)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	return upstream.Send(c.HTTP, httpReq, "Cloudflare")
}
