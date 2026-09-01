// Package zcode implements the ZCode Start Plan backend.
//
// ZCode exposes the Anthropic Messages endpoint under its plan gateway. Other
// client formats are translated to Messages by the proxy, matching current
// ZCode clients. The Start Plan JWT is sent as a bearer token; it is not
// interchangeable with a Z.ai API key.
package zcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
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
	// the ZCode client. They are fixed so an arbitrary inbound client cannot
	// create an inconsistent identity that triggers the gateway's abuse checks.
	zcodeAppVersion = "3.10.0"

	aliyunCaptchaHeader = "X-Aliyun-Captcha-Verify-Param"
)

// captchaSource is implemented by the ZCode account manager. Keeping this
// interface local avoids making CAPTCHA state part of the generic backend
// contract used by unrelated providers.
type captchaSource interface {
	CaptchaVerifyParam(context.Context) (string, error)
}

type captchaInvalidator interface {
	InvalidateCaptcha(string)
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

// Supports reports the wire format used by current ZCode clients. Advertising
// the legacy Chat Completions path makes Responses requests prefer that path,
// which the plan gateway rejects with code 3012.
func (c *Client) Supports(kind backend.Kind) bool {
	return kind == backend.KindAnthropic
}

func endpoint(kind backend.Kind) (string, bool) {
	switch kind {
	case backend.KindAnthropic:
		return "/anthropic/v1/messages", true
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
	httpReq.Header.Set("HTTP-Referer", "https://zcode.z.ai")
	httpReq.Header.Set("X-Platform", runtime.GOOS+"-"+runtime.GOARCH)
	httpReq.Header.Set("X-Release-Channel", "production")
	httpReq.Header.Set("X-Client-Language", "en")
	httpReq.Header.Set("X-Client-Timezone", "UTC")
	httpReq.Header.Set("X-Os-Category", runtime.GOOS)
	httpReq.Header.Set("X-Device-Mid", deviceMID(token))
	accept := "application/json"
	if req.Streaming {
		accept = "text/event-stream"
	}
	httpReq.Header.Set("Accept", accept)
	if req.Kind == backend.KindAnthropic {
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
	}
	captchaParam := strings.TrimSpace(req.Header.Get(aliyunCaptchaHeader))
	if source, ok := c.Tokens.(captchaSource); ok {
		// Prefer the proxy's newest proof. Client applications can retain a
		// previous header across retries, while the manager knows which proof
		// was most recently generated for this proxy session.
		param, sourceErr := source.CaptchaVerifyParam(ctx)
		if sourceErr == nil {
			captchaParam = param
		} else if captchaParam == "" {
			return nil, sourceErr
		}
	}
	if captchaParam != "" {
		httpReq.Header.Set(aliyunCaptchaHeader, captchaParam)
	}
	copyRuntimeHeaders(httpReq.Header, req.Header)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to ZCode failed: %w", err)
	}
	var captchaRejected bool
	captchaRejected, resp.Body = inspectCaptchaRejection(resp.StatusCode, resp.Body)
	if captchaRejected {
		if invalidator, ok := c.Tokens.(captchaInvalidator); ok {
			invalidator.InvalidateCaptcha(captchaParam)
		}
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// copyRuntimeHeaders forwards only request correlation headers. Credentials,
// client identity, and hop-by-hop headers are never copied from the inbound
// request: a stable identity is required by ZCode's unusual-activity checks.
func copyRuntimeHeaders(dst, src http.Header) {
	for name, values := range src {
		if !isRuntimeHeader(name) || strings.EqualFold(name, aliyunCaptchaHeader) {
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
		"x-request-id",
		"x-query-id",
		"x-session-id":
		return true
	default:
		return false
	}
}

// deviceMID returns a stable, non-secret UUID-shaped identifier for a ZCode
// session. It prevents unrelated inbound client identities from making a
// single proxy process appear as a constantly changing device.
func deviceMID(token string) string {
	sum := sha256.Sum256([]byte("llm-proxy/zcode/device/" + strings.TrimSpace(token)))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// inspectCaptchaRejection identifies the ZCode responses that make the
// current proof unusable. Error responses are buffered and restored so the
// normal server path still relays ZCode's original body to the client.
func inspectCaptchaRejection(status int, body io.ReadCloser) (bool, io.ReadCloser) {
	if status != http.StatusBadRequest && status != http.StatusMethodNotAllowed {
		return false, body
	}
	b, err := io.ReadAll(io.LimitReader(body, 1<<20))
	_ = body.Close()
	replay := io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return false, replay
	}
	var envelope struct {
		Code json.Number `json:"code"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return false, replay
	}
	return envelope.Code.String() == "3007" || envelope.Code.String() == "3012", replay
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
