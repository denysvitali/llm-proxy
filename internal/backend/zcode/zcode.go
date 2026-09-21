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
	"strconv"
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

	// zcodeAppVersion identifies the current open-source ZCode client build.
	// Keep this in sync with ZCode's package.json because the plan gateway uses
	// it for client capability and billing responses.
	zcodeAppVersion = "3.14.0"
	zcodeLanguage   = "en-US"

	// unusualActivityCooldown is how long model requests pause after the plan
	// gateway reports code 3012 ("request has been blocked due to unusual
	// activity"). The block targets the account itself, outlives CAPTCHA
	// proofs, and escalates when blocked sessions keep hammering the gateway,
	// so the proxy backs off instead of retrying.
	unusualActivityCooldown = 15 * time.Minute
	// unusualActivityCooldownMax bounds the backoff growth: observed blocks
	// outlive the fixed cooldown by far (a session stayed blocked 4 h after a
	// 3012 on 2026-09-02), and repeated probes while blocked only deepen the
	// block, so consecutive 3012s double the pause up to this ceiling.
	unusualActivityCooldownMax = 6 * time.Hour

	aliyunCaptchaHeader       = "X-Aliyun-Captcha-Verify-Param"
	aliyunCaptchaRegionHeader = "X-Aliyun-Captcha-Verify-Region"
	aliyunCaptchaRegion       = "sgp"

	// gatewayEnvelopePeek bounds how much of a failed response body is buffered
	// to look for a plan-gateway error envelope. An envelope is small; anything
	// longer is not one, so the remainder is streamed on instead of held.
	gatewayEnvelopePeek = 64 << 10
)

// zcodeSessionPrefixes are the internal prefixes the official client strips
// (wrt/Sko) before putting session attribution on the wire. The resulting value
// is still treated as client-controlled input and is converted to an opaque
// proxy identifier before it reaches ZCode.
var (
	zcodeSessionPrefixes = []string{"sess_", "subagent_agent_"}
	zcodeQueryPrefixes   = []string{"query_"}
)

// CAPTCHA is not part of the open-source client's normal model request. These
// narrow interfaces are only used if the live gateway challenges a request
// with code 3007 and the proxy has an optional claim-flow proof available.
type captchaSource interface {
	CaptchaVerifyParam(context.Context) (string, error)
}

type captchaConsumer interface {
	TakeCaptchaVerifyParam(context.Context) (string, error)
}

type captchaInvalidator interface {
	InvalidateCaptcha(string)
}

// defaultModels are the models enabled for the Start Plan by the current
// builtin provider catalog.
var defaultModels = []string{"glm-5.3-flash", "glm-5.2", "glm-5-turbo"}

// Client sends requests to the ZCode plan gateway using either a configured
// JWT or, preferably, a TokenSource populated by the browser sign-in flow.
type Client struct {
	BaseURL string
	Key     string
	Tokens  backend.TokenSource
	HTTP    *http.Client

	blockedMu      sync.Mutex
	blockedUntil   time.Time
	blockedStrikes int
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
		// The error is Terminal: the pause is a deadline, not a blip, so the
		// retry budget would only stall the client through the whole backoff
		// before reaching this same answer.
		return nil, backend.Terminal(fmt.Errorf("ZCode plan gateway rejected the session for unusual activity (code 3012); requests are paused until %s to let the block clear", until.UTC().Format(time.RFC3339)))
	}
	identity := requestIdentity(token, req.Header)
	requestBody := transformStartPlanRequest(req.RawBody, identity)
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
		httpReq.Header.Set("X-Client-Timezone", zcodeClientTimezone())
		httpReq.Header.Set("X-Os-Category", zcodeOSCategory())
		// The open-source client reports the host OS release and does not send
		// X-Device-Mid on model requests; that header is reserved for billing
		// and plan-claim endpoints.
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
		// replaying or non-ZCode client. The official builder emits query/session
		// attribution only when its agent context provides those values; do not
		// manufacture either header for ordinary API clients.
		httpReq.Header.Set("X-Request-Id", randomUUID())
		httpReq.Header.Set("X-ZCode-Trace-Id", randomUUID())
		httpReq.Header.Del("X-Session-Id")
		httpReq.Header.Del("X-Query-Id")
		httpReq.Header.Set("X-ZCode-Session-Type", identity.SessionType)
		if identity.SessionID != "" {
			httpReq.Header.Set("X-Session-Id", identity.SessionID)
		}
		if identity.QueryID != "" {
			httpReq.Header.Set("X-Query-Id", identity.QueryID)
		}
		return httpReq, nil
	}
	requestCaptcha := strings.TrimSpace(req.Header.Get(aliyunCaptchaHeader))
	httpReq, err := buildRequest("")
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request to ZCode failed: %w", err)
	}
	normalizeZCodeResponse(resp)
	var inspection rejectionInspection
	inspection, resp.Body = inspectRejection(resp.StatusCode, resp.Body)
	if inspection.unusualActivity {
		c.markUnusualActivity()
	}
	if inspection.captcha {
		captchaParam := c.optionalModelCaptcha(ctx, requestCaptcha)
		if captchaParam != "" {
			_ = resp.Body.Close()
			retryReq, buildErr := buildRequest(captchaParam)
			if buildErr != nil {
				return nil, buildErr
			}
			resp, err = c.HTTP.Do(retryReq)
			if err != nil {
				return nil, fmt.Errorf("retry request to ZCode after security challenge failed: %w", err)
			}
			normalizeZCodeResponse(resp)
			inspection, resp.Body = inspectRejection(resp.StatusCode, resp.Body)
			if inspection.unusualActivity {
				c.markUnusualActivity()
			}
			if inspection.captcha {
				if invalidator, ok := c.Tokens.(captchaInvalidator); ok {
					invalidator.InvalidateCaptcha(captchaParam)
				}
			}
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		// The gateway answered normally again: any past unusual-activity
		// strikes are forgotten so the next block starts from the base pause.
		c.clearUnusualActivity()
	}
	return &backend.Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: resp.Body}, nil
}

func (c *Client) optionalModelCaptcha(ctx context.Context, clientParam string) string {
	if consumer, ok := c.Tokens.(captchaConsumer); ok {
		if param, err := consumer.TakeCaptchaVerifyParam(ctx); err == nil {
			return strings.TrimSpace(param)
		}
	} else if source, ok := c.Tokens.(captchaSource); ok {
		if param, err := source.CaptchaVerifyParam(ctx); err == nil {
			return strings.TrimSpace(param)
		}
	}
	return strings.TrimSpace(clientParam)
}

// normalizeZCodeResponse converts the plan gateway's JSON error envelope into
// the HTTP status that the gateway intended to send. The gateway does not
// always pair its envelope with a matching status. Some rejections have
// reached the proxy as HTTP 200 with a small {"code":...,"msg":...} body;
// passing those through makes an Anthropic client report "body is JSON but
// not a Message" and can cause it to retry the same blocked request.
//
// The envelope can also arrive behind an error status that hides its meaning:
// a spent plan quota (code 1005) has been observed as HTTP 502, which the
// server's retry loop reads as a transient gateway fault and retries. That is
// the one shape this backend must never produce — retrying a business verdict
// means hammering an account the gateway is already throttling, which is what
// escalates a quota rejection into the code-3012 unusual-activity block.
//
// Successful model responses are left byte-for-byte unchanged. SSE responses
// are not inspected here because their body must remain readable as a stream;
// a gateway error returned for a streaming request uses application/json and
// is therefore safe to buffer and classify.
func normalizeZCodeResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "json") {
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			return
		}
		// An envelope behind HTTP 200 means the response is not the success it
		// claims to be, so the mapped status replaces it even when the code is
		// unknown and falls back to 502.
		if status, ok := zcodeGatewayErrorStatus(body); ok {
			resp.StatusCode = status
			resp.Status = fmt.Sprintf("%d %s", status, http.StatusText(status))
		}
		return
	}
	// A non-success response already reports a failure, so it is only peeked
	// at: enough to read an envelope, with the rest of the body streamed on
	// unchanged. Content-Type is deliberately not consulted — the envelope is
	// identified by parsing it, and a gateway that mislabels a JSON error body
	// would otherwise slip past.
	peek, err := io.ReadAll(io.LimitReader(resp.Body, gatewayEnvelopePeek))
	if err != nil {
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(peek))
		return
	}
	resp.Body = prefixedBody{io.MultiReader(bytes.NewReader(peek), resp.Body), resp.Body}
	// Only a definitive business verdict replaces the status the gateway sent.
	// The unknown-code fallback maps to 502 — the very class the retry loop
	// treats as a transient fault — so applying it here would turn a relayed
	// 4xx into exactly the retry storm this function exists to prevent.
	if status, ok := zcodeGatewayErrorStatus(peek); ok && status != http.StatusBadGateway {
		resp.StatusCode = status
		resp.Status = fmt.Sprintf("%d %s", status, http.StatusText(status))
	}
}

// prefixedBody replays an already-buffered prefix before the untouched
// remainder of an upstream body, while still closing that body.
type prefixedBody struct {
	io.Reader
	io.Closer
}

func zcodeGatewayErrorStatus(body []byte) (int, bool) {
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Success *bool           `json:"success"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, false
	}
	if len(envelope.Code) == 0 {
		if envelope.Success == nil || *envelope.Success {
			return 0, false
		}
		return http.StatusBadGateway, true
	}
	if strings.TrimSpace(envelope.Msg) == "" && (envelope.Success == nil || *envelope.Success) {
		return 0, false
	}
	var code int
	if err := json.Unmarshal(envelope.Code, &code); err != nil {
		var textCode string
		if stringErr := json.Unmarshal(envelope.Code, &textCode); stringErr != nil {
			return 0, false
		}
		code, err = strconv.Atoi(strings.TrimSpace(textCode))
		if err != nil {
			return 0, false
		}
	}
	if code == 0 {
		if envelope.Success != nil && !*envelope.Success {
			return http.StatusBadGateway, true
		}
		return 0, false
	}
	switch code {
	case 1005:
		// The plan's quota is spent for the current billing period. Classify
		// this as a client-side limit so the proxy relays it immediately instead
		// of treating a permanent business verdict as a retryable 502 — the
		// gateway has sent this code behind both HTTP 200 and HTTP 502.
		return http.StatusTooManyRequests, true
	case 1006:
		return http.StatusUnauthorized, true
	case 3001, 3006:
		return http.StatusBadRequest, true
	case 3007:
		return http.StatusForbidden, true
	case 3002, 3008, 3009, 3010:
		return http.StatusTooManyRequests, true
	case 3012:
		return http.StatusMethodNotAllowed, true
	default:
		return http.StatusBadGateway, true
	}
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

// markUnusualActivity arms the post-3012 cooldown. Consecutive rejections
// double the pause, capped at unusualActivityCooldownMax, because the plan
// gateway's block escalates when a blocked session keeps probing and outlives
// a fixed 15-minute pause in practice. A later successful model request
// clears the strikes.
func (c *Client) markUnusualActivity() {
	c.blockedMu.Lock()
	defer c.blockedMu.Unlock()
	pause := unusualActivityCooldown
	for i := 0; i < c.blockedStrikes && pause < unusualActivityCooldownMax; i++ {
		pause *= 2
	}
	if pause > unusualActivityCooldownMax {
		pause = unusualActivityCooldownMax
	}
	c.blockedStrikes++
	c.blockedUntil = time.Now().Add(pause)
}

// clearUnusualActivity resets the cooldown and its strike count once a model
// request succeeds again, restoring the initial 15-minute pause for any
// future block.
func (c *Client) clearUnusualActivity() {
	c.blockedMu.Lock()
	defer c.blockedMu.Unlock()
	c.blockedUntil = time.Time{}
	c.blockedStrikes = 0
}

// copyRuntimeHeaders forwards only request correlation headers. Credentials,
// client identity, and hop-by-hop headers are never copied from the inbound
// request: a stable identity is required by ZCode's unusual-activity checks.
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
	case "x-request-id",
		"x-query-id",
		"x-session-id",
		"x-zcode-trace-id",
		"x-zcode-session-type":
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

// requestIdentity derives the device/session attribution for one request. The
// stable device ID is scoped to the configured ZCode token. If the client
// supplies session or query attribution, it is normalized and then one-way
// derived into a UUID so ZCode can retain affinity without learning the
// client's identifiers. Missing attribution stays missing, matching the
// official model-request builder instead of creating synthetic context.
func requestIdentity(token string, header http.Header) zcodeIdentity {
	deviceMid := deviceMID(token)
	rawSession := strings.TrimSpace(header.Get("X-Session-Id"))
	clientSession := normalizedAttribution(rawSession, zcodeSessionPrefixes, "")
	sessionType := normalizeSessionType(header.Get("X-ZCode-Session-Type"))
	if sessionType == "" {
		sessionType = "main"
	}
	if strings.HasPrefix(rawSession, "subagent_agent_") && len(rawSession) > len("subagent_agent_") {
		sessionType = "subagent"
	}
	query := normalizedAttribution(header.Get("X-Query-Id"), zcodeQueryPrefixes, "")
	if clientSession != "" {
		clientSession = derivedUUID("llm-proxy/zcode/client-session/" + deviceMid + "/" + clientSession)
	}
	if query != "" {
		query = derivedUUID("llm-proxy/zcode/client-query/" + deviceMid + "/" + query)
	}
	return zcodeIdentity{
		DeviceMid:   deviceMid,
		SessionID:   clientSession,
		QueryID:     query,
		SessionType: sessionType,
	}
}

func normalizeSessionType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "main":
		return "main"
	case "subagent":
		return "subagent"
	case "other":
		return "other"
	default:
		return ""
	}
}

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

func derivedUUID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// rejectionInspection classifies a buffered ZCode error response that marks
// the account as temporarily blocked for unusual activity.
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
	if status != http.StatusBadRequest && status != http.StatusForbidden && status != http.StatusMethodNotAllowed {
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
			inspection.captcha = true
		case "3012":
			// 3012 marks the session as unusual activity, so let the caller
			// back off instead of retrying.
			inspection.unusualActivity = true
		}
	}
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
