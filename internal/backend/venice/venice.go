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
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const defaultBaseURL = "https://api.venice.ai/api/v1"

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
	// FreeOnly restricts the backend to models Venice prices at $0. The
	// catalog is filtered and Send refuses anything not proven free.
	FreeOnly bool

	mu     sync.Mutex
	prices map[string]modelPrice
}

// modelPrice holds Venice's published per-1M-token USD pricing. ok is false
// when the /models payload carried no pricing block, which free_only treats
// as "not proven free".
type modelPrice struct {
	InputUSD  float64
	OutputUSD float64
	ok        bool
}

func (p modelPrice) free() bool { return p.ok && p.InputUSD == 0 && p.OutputUSD == 0 }

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

func init() {
	backend.Register("venice", func(opts backend.Options) (backend.Backend, error) {
		c := New(opts.BaseURL, opts.APIKey)
		c.FreeOnly = opts.FreeOnly
		return c, nil
	})
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
	if c.FreeOnly {
		if err := c.ensurePrices(ctx); err != nil {
			return nil, fmt.Errorf("venice free_only: load pricing: %w", err)
		}
		if p, known := c.priceFor(req.Model); !known || !p.free() {
			return nil, fmt.Errorf("venice free_only: model %q is not free", req.Model)
		}
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
		ID        string `json:"id"`
		ModelSpec *struct {
			Pricing *struct {
				Input *struct {
					USD float64 `json:"usd"`
				} `json:"input"`
				Output *struct {
					USD float64 `json:"usd"`
				} `json:"output"`
			} `json:"pricing"`
		} `json:"model_spec"`
	} `json:"data"`
}

// recordPrices folds a fetched catalog into the price cache under lock.
func (c *Client) recordPrices(list *modelList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prices == nil {
		c.prices = make(map[string]modelPrice, len(list.Data))
	}
	for _, m := range list.Data {
		p := modelPrice{}
		if m.ModelSpec != nil && m.ModelSpec.Pricing != nil && m.ModelSpec.Pricing.Input != nil && m.ModelSpec.Pricing.Output != nil {
			p = modelPrice{InputUSD: m.ModelSpec.Pricing.Input.USD, OutputUSD: m.ModelSpec.Pricing.Output.USD, ok: true}
		}
		c.prices[m.ID] = p
	}
}

// ensurePrices lazily fetches the catalog so Send can enforce free_only even
// when no /v1/models listing ran first.
func (c *Client) ensurePrices(ctx context.Context) error {
	c.mu.Lock()
	loaded := c.prices != nil
	c.mu.Unlock()
	if loaded {
		return nil
	}
	_, err := c.Models(ctx)
	return err
}

func (c *Client) priceFor(model string) (modelPrice, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.prices[model]
	return p, ok
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
	c.recordPrices(&list)
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == "" {
			continue
		}
		if c.FreeOnly {
			if p, known := c.priceFor(m.ID); !known || !p.free() {
				continue
			}
		}
		models = append(models, m.ID)
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
