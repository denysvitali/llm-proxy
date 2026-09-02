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
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

const (
	// defaultBaseURL is the shared ZCode plan gateway root. The protocol
	// suffixes are different for Anthropic and OpenAI Chat requests.
	defaultBaseURL = "https://zcode.z.ai/api/v1/zcode-plan"

	// anthropicVersion is required by the Anthropic Messages API.
	anthropicVersion = "2023-06-01"

	// zcodeAppVersion and the identity headers below match the current ZCode
	// desktop client. They are fixed so an arbitrary inbound client cannot
	// create an inconsistent identity that triggers the gateway's abuse checks.
	zcodeAppVersion = "3.10.2"
	zcodeLanguage   = "en-US"

	// zcodeOSVersion is the kernel release advertised to the plan gateway.
	// The official client reports its host kernel; the proxy pins one value
	// instead so every replica presents the same stable device identity.
	zcodeOSVersion = "6.8.0-92-generic"

	// unusualActivityCooldown is how long model requests pause after the plan
	// gateway reports code 3012 ("request has been blocked due to unusual
	// activity"). The block targets the account itself, outlives CAPTCHA
	// proofs, and escalates when blocked sessions keep hammering the gateway,
	// so the proxy backs off instead of retrying.
	unusualActivityCooldown = 15 * time.Minute

	aliyunCaptchaHeader       = "X-Aliyun-Captcha-Verify-Param"
	aliyunCaptchaRegionHeader = "X-Aliyun-Captcha-Verify-Region"
	aliyunCaptchaRegion       = "sgp"
)

// zcodeSessionPrefixes and zcodeQueryPrefixes are the internal prefixes the
// official client strips (wrt/Sko) before putting session and query attribution
// on the wire — in the X-Session-Id/X-Query-Id headers and in the request
// metadata alike.
var (
	zcodeSessionPrefixes = []string{"sess_", "subagent_agent_"}
	zcodeQueryPrefixes   = []string{"query_"}
)

// captchaSource is implemented by the ZCode account manager. Keeping this
// interface local avoids making CAPTCHA state part of the generic backend
// contract used by unrelated providers.
type captchaSource interface {
	CaptchaVerifyParam(context.Context) (string, error)
}

type captchaConsumer interface {
	TakeCaptchaVerifyParam(context.Context) (string, error)
}

type captchaInvalidator interface {
	InvalidateCaptcha(string)
}

type captchaRefresher interface {
	RefreshCaptchaVerifyParam(context.Context, string) (string, error)
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

	blockedMu    sync.Mutex
	blockedUntil time.Time
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
	if until, blocked := c.unusualActivityBlock(); blocked {
		// Fail fast — and before consuming a browser proof — while the plan
		// gateway's unusual-activity block is active. Surfacing this as a
		// backend error also lets server-level fallback routes take over.
		return nil, fmt.Errorf("ZCode plan gateway rejected the session for unusual activity (code 3012); requests are paused until %s to let the block clear", until.UTC().Format(time.RFC3339))
	}
	identity := requestIdentity(token, req.Header)
	requestBody := transformStartPlanRequest(req.RawBody, identity)
	captchaParam := strings.TrimSpace(req.Header.Get(aliyunCaptchaHeader))
	if consumer, ok := c.Tokens.(captchaConsumer); ok {
		// Browser proofs are one-use credentials. Account managers consume a
		// cached proof before sending so concurrent requests cannot reuse the
		// same Aliyun certifyId and trigger an unusual-activity block.
		param, sourceErr := consumer.TakeCaptchaVerifyParam(ctx)
		if sourceErr == nil {
			captchaParam = param
		} else if captchaParam == "" {
			return nil, sourceErr
		}
	} else if source, ok := c.Tokens.(captchaSource); ok {
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
	buildRequest := func(param string) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(requestBody))
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
		httpReq.Header.Set("X-Platform", runtime.GOOS+"-"+zcodeArch())
		httpReq.Header.Set("X-Release-Channel", "production")
		httpReq.Header.Set("X-Client-Language", zcodeLanguage)
		httpReq.Header.Set("X-Client-Timezone", "UTC")
		httpReq.Header.Set("X-Os-Category", runtime.GOOS)
		// Static analysis of the official runtimes (desktop appfull/ and the
		// zcode.cjs agent runtime): model requests build headers via the
		// gin/fin builders, which include X-Os-Version (host os.release())
		// but NOT X-Device-Mid — that header only exists on the non-model
		// endpoints that use buildZCodeSourceHeadersFromContext. The OS
		// version is pinned rather than read from the proxy host so every
		// replica presents the same stable value.
		httpReq.Header.Set("X-Os-Version", zcodeOSVersion)
		httpReq.Header.Set("X-Request-Id", randomUUID())
		httpReq.Header.Set("X-ZCode-Session-Type", "main")
		httpReq.Header.Set("X-ZCode-Trace-Id", randomUUID())
		accept := "application/json"
		if req.Streaming {
			accept = "text/event-stream"
		}
		httpReq.Header.Set("Accept", accept)
		httpReq.Header.Set("Anthropic-Version", anthropicVersion)
		if param != "" {
			httpReq.Header.Set(aliyunCaptchaHeader, param)
			httpReq.Header.Set(aliyunCaptchaRegionHeader, aliyunCaptchaRegion)
		}
		copyRuntimeHeaders(httpReq.Header, req.Header)
		// Correlation IDs are generated by the official client for each model
		// request. Reapply them after copying the narrow runtime-header set so
		// an inbound client cannot reuse its own IDs upstream and look like a
		// replaying or non-ZCode client. The session ID is the one attribution
		// value that intentionally remains stable for the proxy session.
		httpReq.Header.Set("X-Request-Id", randomUUID())
		httpReq.Header.Set("X-ZCode-Trace-Id", randomUUID())
		httpReq.Header.Set("X-Session-Id", identity.SessionID)
		httpReq.Header.Set("X-Query-Id", randomUUID())
		return httpReq, nil
	}
	httpReq, err := buildRequest(captchaParam)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to ZCode failed: %w", err)
	}
	var inspection rejectionInspection
	inspection, resp.Body = inspectRejection(resp.StatusCode, resp.Body)
	if inspection.unusualActivity {
		c.markUnusualActivity()
	}
	if inspection.captcha {
		if invalidator, ok := c.Tokens.(captchaInvalidator); ok {
			invalidator.InvalidateCaptcha(captchaParam)
		}
		if refresher, ok := c.Tokens.(captchaRefresher); ok {
			freshParam, refreshErr := refresher.RefreshCaptchaVerifyParam(ctx, captchaParam)
			if refreshErr == nil && freshParam != "" && freshParam != captchaParam {
				_ = resp.Body.Close()
				retryReq, buildErr := buildRequest(freshParam)
				if buildErr != nil {
					return nil, buildErr
				}
				resp, err = c.HTTP.Do(retryReq)
				if err != nil {
					return nil, fmt.Errorf("retry request to ZCode after CAPTCHA refresh failed: %w", err)
				}
				inspection, resp.Body = inspectRejection(resp.StatusCode, resp.Body)
				if inspection.unusualActivity {
					c.markUnusualActivity()
				}
				if inspection.captcha {
					if invalidator, ok := c.Tokens.(captchaInvalidator); ok {
						invalidator.InvalidateCaptcha(freshParam)
					}
				}
			}
		}
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

// unusualActivityBlock reports the time until which model requests should
// pause after a code-3012 unusual-activity rejection.
func (c *Client) unusualActivityBlock() (time.Time, bool) {
	c.blockedMu.Lock()
	defer c.blockedMu.Unlock()
	if c.blockedUntil.IsZero() || time.Now().After(c.blockedUntil) {
		return time.Time{}, false
	}
	return c.blockedUntil, true
}

func (c *Client) markUnusualActivity() {
	c.blockedMu.Lock()
	defer c.blockedMu.Unlock()
	c.blockedUntil = time.Now().Add(unusualActivityCooldown)
}

// copyRuntimeHeaders forwards only request correlation headers. Credentials,
// client identity, and hop-by-hop headers are never copied from the inbound
// request: a stable identity is required by ZCode's unusual-activity checks.
func copyRuntimeHeaders(dst, src http.Header) {
	for name, values := range src {
		if !isRuntimeHeader(name) || strings.EqualFold(name, aliyunCaptchaHeader) || strings.EqualFold(name, aliyunCaptchaRegionHeader) {
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
		strings.ToLower(aliyunCaptchaRegionHeader),
		"x-request-id",
		"x-query-id",
		"x-session-id",
		"x-zcode-trace-id":
		return true
	default:
		return false
	}
}

func zcodeArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

// normalizedAttribution strips the official client's internal prefixes from an
// inbound attribution value, mirroring wrt/Sko: a prefix is removed only when
// something follows it, so a bare prefix stays intact.
func normalizedAttribution(value string, prefixes []string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return value[len(prefix):]
		}
	}
	return value
}

// requestIdentity derives the device/session attribution for one request: the
// stable per-token device mid, and the session id the inbound client asked for
// (prefix-normalized) or a separate stable proxy-session UUID when the client
// sent none. Device and session IDs must not collapse to the same value: the
// official client treats them as distinct pieces of attribution.
func requestIdentity(token string, header http.Header) zcodeIdentity {
	deviceMid := deviceMID(token)
	return zcodeIdentity{
		DeviceMid: deviceMid,
		SessionID: normalizedAttribution(header.Get("X-Session-Id"), zcodeSessionPrefixes, sessionID(token)),
	}
}

// PreviewRequest exposes the body Send would put on the wire so the admin
// request inspector shows the transformed request instead of the inbound one.
// The identity here is derived from the configured key; browser token sources
// are not consulted because resolving them can perform IO, which is Send's
// job — deployments using a token source preview a placeholder device mid.
func (c *Client) PreviewRequest(req *backend.Request) ([]byte, error) {
	return transformStartPlanRequest(req.RawBody, requestIdentity(c.Key, req.Header)), nil
}

var _ backend.RequestPreviewer = (*Client)(nil)

func randomUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		sum := sha256.Sum256([]byte(time.Now().UTC().String()))
		copy(raw[:], sum[:16])
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// deviceMID returns a stable, non-secret UUID-shaped identifier for a ZCode
// session. It prevents unrelated inbound client identities from making a
// single proxy process appear as a constantly changing device.
func deviceMID(token string) string {
	return derivedUUID("llm-proxy/zcode/device/" + strings.TrimSpace(token))
}

// sessionID returns a stable UUID in a separate namespace from deviceMID.
// When an inbound client omits X-Session-Id, this gives the gateway the
// distinct session attribution emitted by ZCode instead of repeating the
// device ID in both fields.
func sessionID(token string) string {
	return derivedUUID("llm-proxy/zcode/session/" + strings.TrimSpace(token))
}

func derivedUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// rejectionInspection classifies a buffered ZCode error response: a bad
// CAPTCHA proof that a fresh one can replace, and/or an unusual-activity
// block against the account itself.
type rejectionInspection struct {
	captcha         bool
	unusualActivity bool
}

// inspectRejection identifies the ZCode responses that make the current
// proof unusable or that flag the account itself. Error responses are
// buffered and restored so the normal server path still relays ZCode's
// original body to the client.
func inspectRejection(status int, body io.ReadCloser) (rejectionInspection, io.ReadCloser) {
	var inspection rejectionInspection
	if status != http.StatusBadRequest && status != http.StatusMethodNotAllowed {
		return inspection, body
	}
	b, err := io.ReadAll(io.LimitReader(body, 1<<20))
	_ = body.Close()
	replay := io.NopCloser(bytes.NewReader(b))
	if err != nil {
		return inspection, replay
	}
	var envelope struct {
		Code json.Number `json:"code"`
	}
	if err := json.Unmarshal(b, &envelope); err == nil {
		switch envelope.Code.String() {
		case "3007":
			// 3007 is the CAPTCHA challenge: the proof is bad and a fresh
			// one can recover the request.
			inspection.captcha = true
		case "3012":
			// 3012 marks the session as unusual activity. A fresh proof does
			// not lift it, so preserve the proof and let the caller back off
			// instead of retrying.
			inspection.unusualActivity = true
		}
	}
	// Aliyun's edge security layer emits an HTML 405 page instead of ZCode's
	// JSON challenge when it blocks the request. Treat that page the same way
	// so a fresh solver proof can recover the request.
	if status == http.StatusMethodNotAllowed && isAliyunBlockPage(b) {
		inspection.captcha = true
	}
	return inspection, replay
}

func isAliyunBlockPage(body []byte) bool {
	lower := bytes.ToLower(body)
	return bytes.Contains(lower, []byte("<title>405</title>")) &&
		bytes.Contains(lower, []byte("request has been blocked"))
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
