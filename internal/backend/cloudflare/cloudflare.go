// Package cloudflare implements Cloudflare's account-scoped OpenAI-compatible
// Chat Completions endpoint.
package cloudflare

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
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
		HTTP: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 15 * time.Minute,
			},
		},
	}, nil
}

func init() {
	backend.Register("cloudflare", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey)
	})
}

func (c *Client) Name() string { return "cloudflare" }

func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindOpenAIChat
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
	if !c.Supports(req.Kind) {
		return nil, backend.Terminal(fmt.Errorf("cloudflare backend does not support kind %q", req.Kind))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(req.RawBody))
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
		return nil, fmt.Errorf("request to Cloudflare failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}
