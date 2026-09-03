// Package opencodego implements the OpenCode Go backend. Go exposes different
// native API endpoints for different models, so SupportsModel selects the
// model's documented wire format before the server translates or forwards a
// request.
package opencodego

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	defaultBaseURL = "https://opencode.ai/zen/go/v1"

	anthropicVersion = "2023-06-01"
	userAgent        = "llm-proxy"
)

// modelKinds is the protocol mapping published in the OpenCode Go endpoint
// table. Models not listed here default to Chat Completions, which is the
// compatibility endpoint used by the remaining Go models and lets newly
// published chat models work without a proxy release.
var modelKinds = map[string]backend.Kind{
	"grok-4.6":                   backend.KindOpenAIResponses,
	"gpt-5.6-luna":               backend.KindOpenAIResponses,
	"muse-spark-1.2-contributor": backend.KindOpenAIResponses,
	"minimax-m3":                 backend.KindAnthropic,
	"minimax-m2.7":               backend.KindAnthropic,
	"minimax-m2.5":               backend.KindAnthropic,
	"qwen3.8-max":                backend.KindAnthropic,
	"qwen3.7-max":                backend.KindAnthropic,
	"qwen3.7-plus":               backend.KindAnthropic,
	"qwen3.6-plus":               backend.KindAnthropic,
}

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
				ResponseHeaderTimeout: 10 * time.Minute,
			},
		},
	}
}

func init() {
	backend.Register("opencode-go", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

func (c *Client) Name() string { return "opencode-go" }

// HasAPIKey reports whether an upstream API key is configured.
func (c *Client) HasAPIKey() bool { return c.Key != "" }

// Supports reports the API shapes available from Go's model-specific
// endpoints. SupportsModel refines this to the endpoint for one model.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindAnthropic, backend.KindOpenAIChat, backend.KindOpenAIResponses:
		return true
	default:
		return false
	}
}

// SupportsModel reports whether model natively accepts the requested wire
// format. The model may carry the qualified opencode-go/ prefix.
func (c *Client) SupportsModel(kind backend.Kind, model string) bool {
	return kind == modelKind(model)
}

func modelKind(model string) backend.Kind {
	if _, rest, found := strings.Cut(model, "/"); found {
		model = rest
	}
	if kind, ok := modelKinds[model]; ok {
		return kind
	}
	return backend.KindOpenAIChat
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
	path, ok := endpoint(req.Kind)
	if !ok || !c.SupportsModel(req.Kind, req.Model) {
		return nil, fmt.Errorf("opencode-go backend does not support kind %q for model %q", req.Kind, req.Model)
	}
	session, err := ensureSession(req)
	if err != nil {
		return nil, fmt.Errorf("prepare OpenCode Go session: %w", err)
	}

	body := req.RawBody
	if req.Kind == backend.KindOpenAIResponses && len(body) > 0 {
		normalized, err := translate.NormalizeResponsesRequest(body)
		if err != nil {
			return nil, fmt.Errorf("normalize OpenCode Go Responses request: %w", err)
		}
		body = normalized
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("x-opencode-session", session)
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	httpReq.Header.Set("Accept", accept)
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenCode Go failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Models lists the models currently available through OpenCode Go.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	}
	httpReq.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenCode Go failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data}
	}
	var list modelList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode OpenCode Go models: %w", err)
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		if model.ID != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

// ensureSession returns the client's OpenCode session when one is supplied.
// Some Responses clients use Session-Id rather than OpenCode's header, so it
// is accepted as an input alias but is never forwarded under its original
// name. Requests without either header receive an opaque fallback value. The
// value is stored on req so retries of the same request keep their upstream
// affinity; callers with a multi-turn conversation should send the same
// x-opencode-session value on each turn for prompt-cache affinity.
func ensureSession(req *backend.Request) (string, error) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for _, name := range []string{"X-OpenCode-Session", "X-Session-Id", "Session-Id"} {
		if session := strings.TrimSpace(req.Header.Get(name)); session != "" {
			req.Header.Set("X-OpenCode-Session", session)
			return session, nil
		}
	}

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	session := "llm-proxy-" + hex.EncodeToString(random[:])
	req.Header.Set("X-OpenCode-Session", session)
	return session, nil
}

type HTTPError struct {
	Status int
	Header http.Header
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OpenCode Go returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the
// client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
}
