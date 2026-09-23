// Package opencode implements the OpenCode Zen backend. Zen natively serves
// both the Anthropic Messages API (/messages) and the OpenAI Chat Completions
// API (/chat/completions), so both request kinds pass through byte-for-byte
// with the upstream key swapped in.
package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	defaultBaseURL = "https://opencode.ai/zen/v1"

	// anthropicVersion is the API version header Zen expects on /messages.
	anthropicVersion = "2023-06-01"

	// Zen's free tier expects the client identity headers sent by OpenCode.
	// These values identify llm-proxy's compatibility layer, not the caller.
	openCodeUserAgent = "opencode/1.18.32 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"
	openCodeClient    = "cli"
	openCodeProject   = "global"

	// openCodePublicToken is the anonymous free-tier bearer Zen accepts when
	// no account key is configured.
	openCodePublicToken = "public"

	// freeTierMaxBody caps how much of a free-tier SSE body is buffered while
	// aggregating it back into a non-stream completion.
	freeTierMaxBody = 16 << 20

	// openCodeIDChars is the base62 alphabet OpenCode's identifier generator
	// uses for the random tail of session and request IDs.
	openCodeIDChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
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
				Proxy:                 http.ProxyFromEnvironment,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 10 * time.Minute,
			},
		},
	}
}

// HasAPIKey reports whether an upstream API key is configured. Key presence
// does not establish model access; Zen may restrict offerings by account or
// model.
func (c *Client) HasAPIKey() bool {
	return c.Key != ""
}

func init() {
	backend.Register("opencode", func(opts backend.Options) (backend.Backend, error) {
		return New(opts.BaseURL, opts.APIKey), nil
	})
}

func (c *Client) Name() string { return "opencode" }

// Supports: Zen exposes /messages and /chat/completions natively, so both
// shapes pass through untouched. The OpenAI Responses API is translated by
// the server instead.
func (c *Client) Supports(kind backend.Kind) bool {
	switch kind {
	case backend.KindAnthropic, backend.KindOpenAIChat:
		return true
	case backend.KindOpenAIResponses:
		return false
	default:
		return false
	}
}

// SupportsModel refines Supports per model. Zen's Anthropic /messages endpoint
// only serves its Anthropic-native (Claude) models; for every other model in
// its catalog (OpenAI-native and community models such as x-preview-f-free)
// /messages returns HTTP 500, while /chat/completions works. Reporting no
// native Anthropic support for those models makes the server translate
// Anthropic requests onto Chat Completions instead of forwarding them to the
// broken endpoint. Chat Completions is served for every model.
func (c *Client) SupportsModel(kind backend.Kind, model string) bool {
	if kind == backend.KindAnthropic && !isAnthropicNativeModel(model) {
		return false
	}
	return c.Supports(kind)
}

// isAnthropicNativeModel reports whether a Zen model id is a Claude model,
// which are the only models Zen serves over the Anthropic /messages endpoint.
// The id may carry an "opencode/" backend prefix.
func isAnthropicNativeModel(model string) bool {
	m := model
	if i := strings.IndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return strings.HasPrefix(m, "claude-")
}

// Do performs a request against the Zen API, attaching the bearer token when
// a key is configured. A nil body means no request body is sent.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, accept string) (*http.Response, error) {
	return c.do(ctx, method, path, body, accept, "")
}

// do is the request path used by Send when it can preserve the caller's
// session affinity across retries. Zen's free tier checks for the identity
// headers emitted by the OpenCode CLI, so every Zen request carries the same
// compatibility header set.
func (c *Client) do(ctx context.Context, method, path string, body []byte, accept, session string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Key)
	} else {
		// Keyless path: Zen's free tier expects the OpenCode public token.
		httpReq.Header.Set("Authorization", "Bearer "+openCodePublicToken)
	}
	httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		httpReq.Header.Set("Accept", accept)
	}
	if session == "" {
		var err error
		session, err = newOpenCodeSessionID()
		if err != nil {
			return nil, fmt.Errorf("generate OpenCode session ID: %w", err)
		}
	}
	requestID, err := newOpenCodeRequestID()
	if err != nil {
		return nil, fmt.Errorf("generate OpenCode request ID: %w", err)
	}
	httpReq.Header.Set("User-Agent", openCodeUserAgent)
	httpReq.Header.Set("x-opencode-client", openCodeClient)
	httpReq.Header.Set("x-opencode-project", openCodeProject)
	httpReq.Header.Set("x-opencode-session", session)
	// Zen's anonymous/free relay checks the generic session alias as well as
	// the OpenCode attribution header. The official client uses this alias for
	// non-OpenCode providers, and Zen accepts it for free-tier access.
	httpReq.Header.Set("X-Session-Id", session)
	httpReq.Header.Set("x-opencode-request", requestID)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to OpenCode Zen failed: %w", err)
	}
	return resp, nil
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	var path string
	switch req.Kind {
	case backend.KindAnthropic:
		path = "/messages"
	case backend.KindOpenAIChat:
		path = "/chat/completions"
	default:
		return nil, fmt.Errorf("opencode backend does not support kind %q", req.Kind)
	}

	// Free tier (no API key): rewrite the body for Zen's gates (force
	// stream, inject the bash/glob/grep/read tool quartet) and always
	// negotiate SSE upstream. Non-streaming callers get the stream
	// aggregated back into a JSON completion below.
	freeTier := c.Key == ""
	body := req.RawBody
	if freeTier {
		prepared, err := prepareFreeTierBody(req.Kind, body)
		if err != nil {
			return nil, err
		}
		body = prepared
	}
	accept := "application/json"
	if freeTier || req.Streaming {
		accept = "text/event-stream"
	}
	session, err := sessionForRequest(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, path, body, accept, session)
	if err != nil {
		return nil, err
	}
	if freeTier && !req.Streaming {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return aggregateFreeTierResponse(req.Kind, resp)
		}
		// Error statuses (including a residual 403) are relayed verbatim.
		return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// aggregateFreeTierResponse consumes a free-tier SSE body and returns a
// non-stream JSON response the server's buffered relays can decode. Non-SSE
// success bodies are forwarded unchanged with their original headers.
func aggregateFreeTierResponse(kind backend.Kind, resp *http.Response) (*backend.Response, error) {
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") && !strings.Contains(ct, "stream") {
		return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, freeTierMaxBody))
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read free-tier stream: %w", err)
	}
	aggregated, err := aggregateFreeTierStream(kind, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	header := resp.Header.Clone()
	header.Set("Content-Type", "application/json")
	header.Del("Content-Length")
	header.Del("Content-Encoding")
	header.Del("Transfer-Encoding")
	return &backend.Response{
		Status: resp.StatusCode,
		Header: header,
		Body:   io.NopCloser(bytes.NewReader(aggregated)),
	}, nil
}

func sessionForRequest(req *backend.Request) (string, error) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if session := req.Header.Get("x-opencode-session"); session != "" {
		return session, nil
	}
	session, err := newOpenCodeSessionID()
	if err != nil {
		return "", fmt.Errorf("generate OpenCode session ID: %w", err)
	}
	req.Header.Set("x-opencode-session", session)
	return session, nil
}

// openCodeIDCounter differentiates IDs minted within the same millisecond,
// matching OpenCode's per-process counter in schema/src/identifier.ts.
var openCodeIDCounter atomic.Uint32

// newOpenCodeID mints a 26-character identifier in OpenCode's descending
// format: 12 hex digits of ~(timestamp<<12 | counter) followed by 14 base62
// random characters. Zen's free-tier gate rejects session IDs outside this
// shape (wrong length or non-hex/non-base62 charset → 403).
func newOpenCodeID() (string, error) {
	ts := time.Now().UnixMilli()
	counter := openCodeIDCounter.Add(1) % 4096
	current := new(big.Int).Mul(big.NewInt(ts), big.NewInt(0x1000))
	current.Add(current, new(big.Int).SetUint64(uint64(counter)))
	value := current.Not(current) // BigInt ~current ≡ -current-1

	var b strings.Builder
	b.Grow(26)
	for i := 0; i < 6; i++ {
		shift := uint(40 - 8*i)
		byteVal := new(big.Int).Rsh(value, shift)
		byteVal.And(byteVal, big.NewInt(0xff))
		fmt.Fprintf(&b, "%02x", byteVal.Uint64())
	}

	tail := make([]byte, 14)
	if _, err := rand.Read(tail); err != nil {
		return "", err
	}
	for _, c := range tail {
		b.WriteByte(openCodeIDChars[int(c)%len(openCodeIDChars)])
	}
	return b.String(), nil
}

func newOpenCodeSessionID() (string, error) {
	id, err := newOpenCodeID()
	if err != nil {
		return "", err
	}
	return "ses_" + id, nil
}

func newOpenCodeRequestID() (string, error) {
	id, err := newOpenCodeID()
	if err != nil {
		return "", err
	}
	return "msg_" + id, nil
}

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// Models lists Zen's public catalog, which can be read with or without a key.
// Inclusion in the catalog does not guarantee account or client access.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	resp, err := c.Do(ctx, http.MethodGet, "/models", nil, "application/json")
	if err != nil {
		return nil, err
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
	Header http.Header
	Body   []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("OpenCode Zen returned HTTP %d: %s", e.Status, strings.TrimSpace(string(e.Body)))
}

// ReadError drains an error response so its body can be surfaced to the client.
func ReadError(resp *http.Response) *HTTPError {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return &HTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: b}
}
