// Package cloudflare implements Cloudflare's account-scoped native Messages,
// Chat Completions, and Responses endpoints.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
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
	_, ok := endpoint(kind)
	return ok
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

// Models returns the known inference model without assuming a discovery API.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []string{"stealth/union-alpha"}, nil
}

// defaultMessagesToolTypes fills only absent tool discriminators. Raw messages
// preserve unknown fields and numeric precision; invalid shapes remain upstream's
// responsibility, and bodies needing no defaults are returned byte-for-byte.
func defaultMessagesToolTypes(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(request["tools"], &tools); err != nil {
		return body, nil
	}
	changed := false
	for i, raw := range tools {
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(raw, &tool); err != nil || tool == nil {
			continue
		}
		if _, exists := tool["type"]; exists {
			continue
		}
		tool["type"] = json.RawMessage(`"custom"`)
		encoded, err := json.Marshal(tool)
		if err != nil {
			return nil, err
		}
		tools[i] = encoded
		changed = true
	}
	if !changed {
		return body, nil
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	request["tools"] = encoded
	return json.Marshal(request)
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	if c.Key == "" {
		return nil, backend.Terminal(fmt.Errorf("cloudflare backend has no API key configured"))
	}
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, backend.Terminal(fmt.Errorf("cloudflare backend does not support kind %q", req.Kind))
	}
	body := req.RawBody
	if req.Kind == backend.KindAnthropic {
		var err error
		body, err = defaultMessagesToolTypes(body)
		if err != nil {
			return nil, backend.Terminal(fmt.Errorf("normalize Cloudflare Messages tools: %w", err))
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	httpReq.Header.Set("Content-Type", "application/json")
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", "2023-06-01")
	}
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
