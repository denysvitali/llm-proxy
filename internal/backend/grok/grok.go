// Package grok implements the xAI Grok subscription backend. The
// subscription endpoint (cli-chat-proxy.grok.com) speaks the OpenAI
// Responses API. Credentials come from the signed-in xAI account session.
package grok

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const defaultBaseURL = "https://cli-chat-proxy.grok.com/v1"

type Client struct {
	BaseURL string
	Tokens  backend.TokenSource
	HTTP    *http.Client
}

func New(baseURL string, tokens backend.TokenSource) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Tokens:  tokens,
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

func (c *Client) Name() string { return "grok" }

func init() {
	backend.Register("grok", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.TokenSource), nil
	})
}

// Supports: the Grok subscription endpoint only speaks the OpenAI Responses
// API; every other inbound shape is translated by the server before Send.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindOpenAIResponses
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Tokens == nil {
		return nil, fmt.Errorf("grok backend has no xAI account configured; sign in from the dashboard")
	}
	token, err := c.Tokens.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("grok account returned an empty access token")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	httpReq.Header.Set("x-grok-client-version", ClientVersion)
	httpReq.Header.Set("x-grok-client-mode", "cli")
	httpReq.Header.Set("User-Agent", "llm-proxy/"+ClientVersion)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if req.Streaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Grok failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// Models: the subscription endpoint has no public model catalog, so a static
// list is returned instead.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	return []string{"grok-4.5", "grok-composer-2.5-fast"}, nil
}

type HTTPError struct {
	Status int
	Header http.Header
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Grok returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 5 * time.Minute}}
}
