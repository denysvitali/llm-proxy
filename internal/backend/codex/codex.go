// Package codex exposes the models included with a signed-in ChatGPT Codex
// subscription. The upstream service speaks the OpenAI Responses API.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	defaultBaseURL = "https://chatgpt.com/backend-api/codex"
	clientVersion  = "0.1.0"
	modelCacheTTL  = 5 * time.Minute
)

type Client struct {
	BaseURL string
	Tokens  backend.TokenSource
	HTTP    *http.Client

	modelMu      sync.Mutex
	cachedModels []string
	modelsUntil  time.Time
}

func New(baseURL string, tokens backend.TokenSource) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Tokens:  tokens,
		HTTP: &http.Client{Timeout: 0, Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, MaxIdleConns: 100,
			IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 5 * time.Minute,
		}},
	}
}

func init() {
	backend.Register("codex", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.TokenSource), nil
	})
}

func (c *Client) Name() string                    { return "codex" }
func (c *Client) Supports(kind backend.Kind) bool { return kind == backend.KindOpenAIResponses }

func (c *Client) Models(ctx context.Context) ([]string, error) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if len(c.cachedModels) > 0 && time.Now().Before(c.modelsUntil) {
		return append([]string(nil), c.cachedModels...), nil
	}
	models, err := c.fetchModels(ctx)
	if err != nil {
		if len(c.cachedModels) > 0 {
			return append([]string(nil), c.cachedModels...), nil
		}
		return nil, err
	}
	c.cachedModels = append(c.cachedModels[:0], models...)
	c.modelsUntil = time.Now().Add(modelCacheTTL)
	return append([]string(nil), models...), nil
}

func (c *Client) fetchModels(ctx context.Context) ([]string, error) {
	credentials, err := c.credentials(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.BaseURL + "/models?client_version=" + url.QueryEscape(clientVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req.Header, credentials, false)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Codex model catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Codex model catalog: HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Models []struct {
			Slug           string `json:"slug"`
			SupportedInAPI bool   `json:"supported_in_api"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Codex model catalog: %w", err)
	}
	models := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		if model.Slug != "" && model.SupportedInAPI {
			models = append(models, model.Slug)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("Codex model catalog is empty")
	}
	return models, nil
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	credentials, err := c.credentials(ctx)
	if err != nil {
		return nil, err
	}
	body, err := normalizeRequest(req.RawBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setHeaders(httpReq.Header, credentials, true)
	copyCodexHeaders(httpReq.Header, req.Header)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to Codex failed: %w", err)
	}
	if req.Streaming || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
	}
	aggregated, err := aggregateResponsesSSE(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("decode Codex response stream: %w", err)
	}
	header := resp.Header.Clone()
	header.Set("Content-Type", "application/json")
	return &backend.Response{Status: resp.StatusCode, Header: header, Body: io.NopCloser(bytes.NewReader(aggregated))}, nil
}

// PreviewRequest returns the body Send places on the wire. ChatGPT's Codex
// endpoint is streaming-only, so even non-streaming proxy calls request SSE
// and are aggregated before being returned to the client.
func (c *Client) PreviewRequest(req *backend.Request) ([]byte, error) {
	return normalizeRequest(req.RawBody)
}

func normalizeRequest(raw []byte) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode Codex request: %w", err)
	}
	body["stream"] = true
	body["store"] = false
	return json.Marshal(body)
}

func aggregateResponsesSSE(body io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "response.completed":
			if len(event.Response) == 0 {
				return nil, errors.New("response.completed omitted response")
			}
			return event.Response, nil
		case "response.failed":
			return nil, fmt.Errorf("upstream response failed: %s", event.Response)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("Codex stream ended without response.completed")
}

func (c *Client) credentials(ctx context.Context) (Credentials, error) {
	if c.Tokens == nil {
		return Credentials{}, errors.New("codex backend has no ChatGPT account configured; sign in from the dashboard")
	}
	if source, ok := c.Tokens.(interface {
		Credentials(context.Context) (Credentials, error)
	}); ok {
		return source.Credentials(ctx)
	}
	token, err := c.Tokens.AccessToken(ctx)
	return Credentials{AccessToken: token}, err
}

func setHeaders(header http.Header, credentials Credentials, streaming bool) {
	header.Set("Authorization", "Bearer "+credentials.AccessToken)
	if credentials.AccountID != "" {
		header.Set("ChatGPT-Account-Id", credentials.AccountID)
	}
	header.Set("Content-Type", "application/json")
	if streaming {
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	header.Set("Originator", "llm-proxy")
}

func copyCodexHeaders(dst, src http.Header) {
	for _, name := range []string{
		"OpenAI-Beta", "Session-Id", "X-Codex-Installation-Id",
		"X-OpenAI-Subagent", "X-OpenAI-Client-Metadata",
	} {
		if value := src.Get(name); value != "" {
			dst.Set(name, value)
		}
	}
}
