// Package zcode implements the ZCode Start Plan backend.
//
// ZCode exposes Anthropic Messages and OpenAI Chat Completions endpoints under
// the same plan gateway. The Start Plan JWT is sent as a bearer token; it is
// not interchangeable with a Z.ai API key.
package zcode

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	// defaultBaseURL is the shared ZCode plan gateway root. The protocol
	// suffixes are different for Anthropic and OpenAI Chat requests.
	defaultBaseURL = "https://zcode.z.ai/api/v1/zcode-plan"

	// anthropicVersion is required by the Anthropic Messages API.
	anthropicVersion = "2023-06-01"

	// zcodeAppVersion and the identity headers below match the headers used by
	// the ZCode client. The inbound request can override runtime values when it
	// already comes from a ZCode client.
	zcodeAppVersion = "3.0.1"

	aliyunCaptchaHeader = "X-Aliyun-Captcha-Verify-Param"
)

// captchaSource is implemented by the ZCode account manager. Keeping this
// interface local avoids making CAPTCHA state part of the generic backend
// contract used by unrelated providers.
type captchaSource interface {
	CaptchaVerifyParam(context.Context) (string, error)
}

// defaultModels is the model included in the currently published Start Plan
// entitlement. Explicit route entries can address another model if ZCode
// enables it for the account.
var defaultModels = []string{"glm-5.3-flash"}

// Client sends requests to the ZCode plan gateway using either a configured
// JWT or, preferably, a TokenSource populated by the browser sign-in flow.
type Client struct {
	BaseURL string
	Key     string
	Tokens  backend.TokenSource
	HTTP    *http.Client
}

// New constructs a ZCode client. BaseURL overrides the gateway root and is
// expected to include /api/v1/zcode-plan when set.
func New(baseURL, key string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     strings.TrimSpace(key),
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
	backend.Register("zcode", func(opts backend.Options) (backend.Backend, error) {
		client := New(opts.BaseURL, opts.APIKey)
		client.Tokens = opts.TokenSource
		return client, nil
	})
}

func (c *Client) Name() string { return "zcode" }

// Supports reports the two wire formats exposed by the ZCode plan gateway.
// Responses API clients are translated by the proxy onto one of these paths.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindAnthropic || kind == backend.KindOpenAIChat
}

func endpoint(kind backend.Kind) (string, bool) {
	switch kind {
	case backend.KindAnthropic:
		return "/anthropic/v1/messages", true
	case backend.KindOpenAIChat:
		return "/chat/completions", true
	default:
		return "", false
	}
}

func (c *Client) Send(ctx context.Context, req *backend.Request) (*backend.Response, error) {
	path, ok := endpoint(req.Kind)
	if !ok {
		return nil, fmt.Errorf("zcode backend does not support kind %q", req.Kind)
	}
	token := c.Key
	if c.Tokens != nil {
		var err error
		token, err = c.Tokens.AccessToken(ctx)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("zcode backend has no ZCode session configured")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(req.RawBody))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", bearerToken(token))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "ZCode/"+zcodeAppVersion)
	httpReq.Header.Set("X-ZCode-App-Version", zcodeAppVersion)
	httpReq.Header.Set("X-ZCode-Agent", "glm")
	httpReq.Header.Set("X-Title", "Z Code@electron")
	httpReq.Header.Set("HTTP-Referer", "https://zcode.z.ai/")
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	httpReq.Header.Set("Accept", accept)
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	if param := strings.TrimSpace(req.Header.Get(aliyunCaptchaHeader)); param != "" {
		httpReq.Header.Set(aliyunCaptchaHeader, param)
	} else if source, ok := c.Tokens.(captchaSource); ok {
		param, err := source.CaptchaVerifyParam(ctx)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set(aliyunCaptchaHeader, param)
	}
	copyRuntimeHeaders(httpReq.Header, req.Header)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to ZCode failed: %w", err)
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// copyRuntimeHeaders forwards only headers that describe the ZCode client or
// its already-solved CAPTCHA. Credentials and hop-by-hop headers are never
// copied from the client request; the backend owns Authorization and the
// transport owns connection headers.
func copyRuntimeHeaders(dst, src http.Header) {
	for name, values := range src {
		if !isRuntimeHeader(name) {
			continue
		}
		copied := false
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				if !copied {
					dst.Del(name)
					copied = true
				}
				dst.Add(name, value)
			}
		}
	}
}

func isRuntimeHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case strings.ToLower(aliyunCaptchaHeader),
		"x-title",
		"http-referer",
		"x-request-id",
		"x-query-id",
		"x-session-id",
		"x-device-mid",
		"x-os-category",
		"x-os-version",
		"x-zcode-app-version",
		"x-zcode-agent",
		"x-zcode-trace-id":
		return true
	default:
		return false
	}
}

// Models returns the models included in the Start Plan catalog known to this
// backend. ZCode does not expose a public /models endpoint for this gateway.
func (c *Client) Models(context.Context) ([]string, error) {
	return append([]string(nil), defaultModels...), nil
}

func bearerToken(key string) string {
	if strings.HasPrefix(strings.ToLower(key), "bearer ") {
		return key
	}
	return "Bearer " + key
}

var _ backend.Backend = (*Client)(nil)
