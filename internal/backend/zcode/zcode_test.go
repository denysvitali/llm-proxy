package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

func TestSupportsAndDefaults(t *testing.T) {
	client := New("", "token")
	if client.BaseURL != defaultBaseURL {
		t.Errorf("New(\"\") BaseURL = %q, want %q", client.BaseURL, defaultBaseURL)
	}
	if got := New("https://example.test/api/v1/zcode-plan/", "token").BaseURL; got != "https://example.test/api/v1/zcode-plan" {
		t.Errorf("New trailing slash BaseURL = %q", got)
	}
	for _, test := range []struct {
		kind backend.Kind
		want bool
	}{
		{backend.KindAnthropic, true},
		{backend.KindOpenAIChat, false},
		{backend.KindOpenAIResponses, false},
		{"", false},
	} {
		if got := client.Supports(test.kind); got != test.want {
			t.Errorf("Supports(%q) = %v, want %v", test.kind, got, test.want)
		}
	}
}

func TestSendNativeEndpoints(t *testing.T) {
	const requestBody = `{"model":"glm-5.3-flash","messages":[]}`
	const responseBody = `{"id":"response-id"}`
	wantRequestBody := transformStartPlanRequest([]byte(requestBody), requestIdentity("secret", nil))
	for _, test := range []struct {
		name             string
		kind             backend.Kind
		path             string
		streaming        bool
		wantAccept       string
		wantAnthropicVer string
	}{
		{
			name:             "anthropic non-streaming",
			kind:             backend.KindAnthropic,
			path:             "/anthropic/v1/messages",
			wantAccept:       "application/json",
			wantAnthropicVer: anthropicVersion,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, test.path)
				}
				received, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if !bytes.Equal(received, wantRequestBody) {
					t.Errorf("request body = %q, want transformed body %q", received, wantRequestBody)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer secret" {
					t.Errorf("Authorization = %q, want %q", got, "Bearer secret")
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				if got := r.Header.Get("Accept"); got != test.wantAccept {
					t.Errorf("Accept = %q, want %q", got, test.wantAccept)
				}
				if got := r.Header.Get("Anthropic-Version"); got != test.wantAnthropicVer {
					t.Errorf("Anthropic-Version = %q, want %q", got, test.wantAnthropicVer)
				}
				_, _ = fmt.Fprint(w, responseBody)
			}))
			defer server.Close()

			response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
				Kind:      test.kind,
				Model:     "glm-5.3-flash",
				RawBody:   []byte(requestBody),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			defer func() { _ = response.Body.Close() }()
			received, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if !bytes.Equal(received, []byte(responseBody)) {
				t.Errorf("response body = %q, want %q", received, responseBody)
			}
		})
	}
}

func TestSendForwardsCaptchaAndRuntimeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{
			"X-Aliyun-Captcha-Verify-Param": "fresh-param",
			aliyunCaptchaRegionHeader:       aliyunCaptchaRegion,
			"X-ZCode-App-Version":           zcodeAppVersion,
			"X-ZCode-Agent":                 "glm",
			"User-Agent":                    "ZCode/" + zcodeAppVersion,
			"HTTP-Referer":                  "https://zcode.z.ai",
			"X-Title":                       "Z Code@electron",
			"X-Platform":                    runtime.GOOS + "-" + zcodeArch(),
			"X-Release-Channel":             "production",
			"X-Client-Language":             zcodeLanguage,
			"X-Client-Timezone":             "UTC",
			"X-Os-Category":                 runtime.GOOS,
			"X-Os-Version":                  zcodeOSVersion,
			"X-ZCode-Session-Type":          "main",
			"X-Session-Id":                  sessionID("secret"),
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := r.Header.Get("X-Request-Id"); got == "" {
			t.Error("X-Request-Id is empty")
		} else if got == "reused-request-id" {
			t.Error("X-Request-Id reused the inbound client value")
		}
		if got := r.Header.Get("X-ZCode-Trace-Id"); got == "" {
			t.Error("X-ZCode-Trace-Id is empty")
		} else if got == "reused-trace-id" {
			t.Error("X-ZCode-Trace-Id reused the inbound client value")
		}
		if got := r.Header.Get("X-Query-Id"); got == "" {
			t.Error("X-Query-Id is empty")
		} else if got == "reused-query-id" {
			t.Error("X-Query-Id reused the inbound client value")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("X-ZCode-Api-Key"); got != "" {
			t.Errorf("X-ZCode-Api-Key was forwarded: %q", got)
		}
		// The official model-request header builders (gin/fin in the ZCode
		// runtimes) do not include X-Device-Mid; only claim/billing do.
		if got := r.Header.Get("X-Device-Mid"); got != "" {
			t.Errorf("X-Device-Mid = %q, want omitted from model requests", got)
		}
		if got := r.Header.Get(aliyunCaptchaRegionHeader); got != aliyunCaptchaRegion {
			t.Errorf("%s = %q, want %q", aliyunCaptchaRegionHeader, got, aliyunCaptchaRegion)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	response, err := New(server.URL, "secret").Send(context.Background(), &backend.Request{
		Kind: backend.KindAnthropic,
		Header: http.Header{
			"X-Aliyun-Captcha-Verify-Param":  []string{"fresh-param"},
			"X-ZCode-App-Version":            []string{"3.7.7"},
			"X-Title":                        []string{"Z Code@test"},
			"X-Device-Mid":                   []string{"untrusted-device"},
			"X-Platform":                     []string{"untrusted-platform"},
			"X-Aliyun-Captcha-Verify-Region": []string{"untrusted-region"},
			"X-ZCode-Api-Key":                []string{"must-not-forward"},
			"X-Request-Id":                   []string{"reused-request-id"},
			"X-ZCode-Trace-Id":               []string{"reused-trace-id"},
			"X-Query-Id":                     []string{"reused-query-id"},
		},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestSendReplacesClientMetadataWithDeviceIdentity(t *testing.T) {
	// Claude Code's inbound body carries its own account and session
	// identifiers in metadata.user_id; the wire body must replace them with
	// the official device identity shape instead of forwarding them.
	const claudeCodeBody = `{"model":"glm-5.3-flash","metadata":{"user_id":"user_5f3a_account_6c9f1d2e-account-uuid_session_1b2c3d4e-session-uuid"},"messages":[{"role":"user","content":"hello"}]}`
	var upstreamBody []byte
	var upstreamSessionHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		upstreamSessionHeader = r.Header.Get("X-Session-Id")
		_, _ = fmt.Fprint(w, `{"type":"message","content":[]}`)
	}))
	defer server.Close()

	client := New(server.URL, "secret")
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(claudeCodeBody),
		Header:  http.Header{"X-Session-Id": []string{"sess_proxy-session-1"}},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	var sent map[string]any
	if err := json.Unmarshal(upstreamBody, &sent); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	metadata, ok := sent["metadata"].(map[string]any)
	if !ok || len(metadata) != 1 {
		t.Fatalf("metadata = %#v, want exactly the user_id key", sent["metadata"])
	}
	wantUserID := zcodeMetadataUserID(zcodeIdentity{DeviceMid: deviceMID("secret"), SessionID: "proxy-session-1"})
	if got := metadata["user_id"]; got != wantUserID {
		t.Errorf("metadata.user_id = %v, want %v", got, wantUserID)
	}
	if strings.Contains(string(upstreamBody), "account-uuid") || strings.Contains(string(upstreamBody), "user_5f3a") {
		t.Errorf("upstream body leaks client identifiers: %s", upstreamBody)
	}
	if upstreamSessionHeader != "proxy-session-1" {
		t.Errorf("X-Session-Id = %q, want prefix-stripped proxy-session-1", upstreamSessionHeader)
	}
}

func TestPreviewRequestMatchesSentBody(t *testing.T) {
	const claudeCodeBody = `{"model":"glm-5.3-flash","metadata":{"user_id":"user_5f3a_account_6c9d-session-uuid"},"messages":[]}`
	var sentBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentBody, _ = io.ReadAll(r.Body)
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "secret")
	preview, err := client.PreviewRequest(&backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(claudeCodeBody),
		Header:  http.Header{"X-Session-Id": []string{"sess_preview-session"}},
	})
	if err != nil {
		t.Fatalf("PreviewRequest() error = %v", err)
	}
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(claudeCodeBody),
		Header:  http.Header{"X-Session-Id": []string{"sess_preview-session"}},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
	if !bytes.Equal(preview, sentBody) {
		t.Errorf("preview body = %s, want the body Send put on the wire: %s", preview, sentBody)
	}
}

func TestNormalizedAttributionStripsInternalPrefixes(t *testing.T) {
	for _, test := range []struct {
		name     string
		value    string
		prefixes []string
		want     string
	}{
		{name: "session prefix", value: "sess_session-1", prefixes: zcodeSessionPrefixes, want: "session-1"},
		{name: "subagent prefix", value: "subagent_agent_worker-1", prefixes: zcodeSessionPrefixes, want: "worker-1"},
		{name: "query prefix", value: "query_query-1", prefixes: zcodeQueryPrefixes, want: "query-1"},
		{name: "bare prefix falls back", value: "sess_", prefixes: zcodeSessionPrefixes, want: "sess_"},
		{name: "unknown value untouched", value: "client-session", prefixes: zcodeSessionPrefixes, want: "client-session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizedAttribution(test.value, test.prefixes, "fallback"); got != test.want {
				t.Errorf("normalizedAttribution() = %q, want %q", got, test.want)
			}
		})
	}
	if got := normalizedAttribution(" ", zcodeSessionPrefixes, "fallback"); got != "fallback" {
		t.Errorf("empty attribution = %q, want fallback", got)
	}
}

func TestDeviceMIDIsStableAndSessionSpecific(t *testing.T) {
	first := deviceMID("session-one")
	if got := deviceMID(" session-one "); got != first {
		t.Fatalf("deviceMID() = %q after whitespace, want stable %q", got, first)
	}
	if got := deviceMID("session-two"); got == first {
		t.Fatalf("deviceMID() = %q for distinct sessions, want distinct identifiers", got)
	}
	if len(first) != 36 || first[8] != '-' || first[13] != '-' || first[18] != '-' || first[23] != '-' {
		t.Fatalf("deviceMID() = %q, want UUID shape", first)
	}
	if got := sessionID("session-one"); got == first {
		t.Fatalf("sessionID() = %q, want a value distinct from deviceMID %q", got, first)
	}
}

type invalidatingTokenAndCaptchaSource struct {
	invalidated string
}

func (s *invalidatingTokenAndCaptchaSource) AccessToken(context.Context) (string, error) {
	return "session-token", nil
}

func (s *invalidatingTokenAndCaptchaSource) CaptchaVerifyParam(context.Context) (string, error) {
	return "source-param", nil
}

func (s *invalidatingTokenAndCaptchaSource) InvalidateCaptcha(param string) {
	s.invalidated = param
}

func TestSendPreservesCaptchaOnUnusualActivityRejection(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get(aliyunCaptchaHeader); got != "source-param" {
			t.Errorf("captcha header = %q, want source-param", got)
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = fmt.Fprint(w, `{"code":3012,"msg":"request has been blocked due to unusual activity."}`)
	}))
	defer server.Close()

	source := &invalidatingTokenAndCaptchaSource{}
	client := New(server.URL, "unused")
	client.Tokens = source
	response, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if string(body) != `{"code":3012,"msg":"request has been blocked due to unusual activity."}` {
		t.Errorf("response body = %q, want original rejection", body)
	}
	if source.invalidated != "" {
		t.Errorf("invalidated captcha = %q on 3012, want proof preserved", source.invalidated)
	}

	// The rejection arms a cooldown: the next request fails fast without
	// reaching ZCode, so a blocked session stops hammering the gateway and
	// server-level fallback routes can take over.
	second, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic})
	if err == nil || !strings.Contains(err.Error(), "unusual activity") {
		t.Fatalf("second Send() error = %v, want unusual-activity cooldown error", err)
	}
	if second != nil {
		t.Fatal("second Send() returned a response during the cooldown")
	}
	if requests != 1 {
		t.Errorf("upstream requests = %d during cooldown, want 1", requests)
	}

	// Once the cooldown expires, requests reach ZCode again.
	client.blockedUntil = time.Now().Add(-time.Second)
	third, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic})
	if err != nil {
		t.Fatalf("Send() after cooldown error = %v", err)
	}
	_ = third.Body.Close()
	if requests != 2 || third.Status != http.StatusMethodNotAllowed {
		t.Fatalf("requests = %d status = %d after cooldown, want 2 and 405", requests, third.Status)
	}
}

type countingCaptchaConsumer struct {
	taken int
}

func (s *countingCaptchaConsumer) AccessToken(context.Context) (string, error) {
	return "session-token", nil
}

func (s *countingCaptchaConsumer) TakeCaptchaVerifyParam(context.Context) (string, error) {
	s.taken++
	return fmt.Sprintf("proof-%d", s.taken), nil
}

func TestSendCooldownDoesNotConsumeCaptchaProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = fmt.Fprint(w, `{"code":3012,"msg":"request has been blocked due to unusual activity."}`)
	}))
	defer server.Close()

	consumer := &countingCaptchaConsumer{}
	client := New(server.URL, "unused")
	client.Tokens = consumer
	response, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic}); err == nil {
		t.Fatal("second Send() during cooldown succeeded, want fail-fast error")
	}
	if consumer.taken != 1 {
		t.Errorf("browser proofs consumed = %d, want 1 (cooldown must not burn proofs)", consumer.taken)
	}
}

func TestInspectRejectionClassification(t *testing.T) {
	const blockPage = `<!doctype html><title>405</title><p>Sorry, your request has been blocked as it may cause potential threats to the server's security.</p>`
	for _, test := range []struct {
		name                string
		status              int
		body                string
		wantCaptcha         bool
		wantUnusualActivity bool
	}{
		{name: "captcha challenge", status: http.StatusBadRequest, body: `{"code":3007,"msg":"captcha verify failed"}`, wantCaptcha: true},
		{name: "unusual activity on 405", status: http.StatusMethodNotAllowed, body: `{"code":3012,"msg":"request has been blocked due to unusual activity."}`, wantUnusualActivity: true},
		{name: "unusual activity on 400", status: http.StatusBadRequest, body: `{"code":3012,"msg":"request has been blocked due to unusual activity."}`, wantUnusualActivity: true},
		{name: "aliyun html block page", status: http.StatusMethodNotAllowed, body: blockPage, wantCaptcha: true},
		{name: "other error code", status: http.StatusMethodNotAllowed, body: `{"code":1302,"msg":"rate limit"}`},
		{name: "success untouched", status: http.StatusOK, body: `{"type":"message"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspection, replay := inspectRejection(test.status, io.NopCloser(strings.NewReader(test.body)))
			replayed, err := io.ReadAll(replay)
			if err != nil {
				t.Fatalf("read replay: %v", err)
			}
			if string(replayed) != test.body {
				t.Errorf("replayed body = %q, want %q", replayed, test.body)
			}
			if inspection.captcha != test.wantCaptcha {
				t.Errorf("captcha = %v, want %v", inspection.captcha, test.wantCaptcha)
			}
			if inspection.unusualActivity != test.wantUnusualActivity {
				t.Errorf("unusualActivity = %v, want %v", inspection.unusualActivity, test.wantUnusualActivity)
			}
		})
	}
}

func TestSendInvalidatesCaptchaOnVerificationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"code":3007,"msg":"captcha verify failed"}`)
	}))
	defer server.Close()

	source := &invalidatingTokenAndCaptchaSource{}
	client := New(server.URL, "unused")
	client.Tokens = source
	response, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
	if source.invalidated != "source-param" {
		t.Errorf("invalidated captcha = %q, want source-param", source.invalidated)
	}
}

type refreshingTokenAndCaptchaSource struct {
	invalidated []string
	refreshed   int
}

func (s *refreshingTokenAndCaptchaSource) AccessToken(context.Context) (string, error) {
	return "session-token", nil
}

func (s *refreshingTokenAndCaptchaSource) CaptchaVerifyParam(context.Context) (string, error) {
	return "first-param", nil
}

func (s *refreshingTokenAndCaptchaSource) InvalidateCaptcha(param string) {
	s.invalidated = append(s.invalidated, param)
}

func (s *refreshingTokenAndCaptchaSource) RefreshCaptchaVerifyParam(context.Context, string) (string, error) {
	s.refreshed++
	return "fresh-param", nil
}

func TestSendRetriesCaptchaChallengeWithFreshSolverProof(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		wantParam := "first-param"
		if requests == 2 {
			wantParam = "fresh-param"
		}
		if got := r.Header.Get(aliyunCaptchaHeader); got != wantParam {
			t.Errorf("request %d CAPTCHA = %q, want %q", requests, got, wantParam)
		}
		if requests == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"code":3007,"msg":"captcha verify failed"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"type":"message","content":[]}`)
	}))
	defer server.Close()

	source := &refreshingTokenAndCaptchaSource{}
	client := New(server.URL, "unused")
	client.Tokens = source
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(`{"model":"glm-5.3-flash","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Status != http.StatusOK || requests != 2 || source.refreshed != 1 {
		t.Fatalf("status=%d requests=%d refreshed=%d", response.Status, requests, source.refreshed)
	}
	if !reflect.DeepEqual(source.invalidated, []string{"first-param"}) {
		t.Fatalf("invalidated = %#v", source.invalidated)
	}
}

func TestSendRetriesAliyunBlockPageWithFreshSolverProof(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		wantParam := "first-param"
		if requests == 2 {
			wantParam = "fresh-param"
		}
		if got := r.Header.Get(aliyunCaptchaHeader); got != wantParam {
			t.Errorf("request %d CAPTCHA = %q, want %q", requests, got, wantParam)
		}
		if requests == 1 {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = fmt.Fprint(w, `<!doctype html><title>405</title><p>Sorry, your request has been blocked as it may cause potential threats to the server's security.</p>`)
			return
		}
		_, _ = fmt.Fprint(w, `{"type":"message","content":[]}`)
	}))
	defer server.Close()

	source := &refreshingTokenAndCaptchaSource{}
	client := New(server.URL, "unused")
	client.Tokens = source
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(`{"model":"glm-5.3-flash","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Status != http.StatusOK || requests != 2 || source.refreshed != 1 {
		t.Fatalf("status=%d requests=%d refreshed=%d", response.Status, requests, source.refreshed)
	}
}

func TestModels(t *testing.T) {
	models, err := New("", "secret").Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	want := []string{"glm-5.3-flash"}
	if !reflect.DeepEqual(models, want) {
		t.Errorf("Models() = %#v, want %#v", models, want)
	}
	models[0] = "changed"
	models, _ = New("", "secret").Models(context.Background())
	if models[0] != want[0] {
		t.Errorf("Models() returned mutable package data: %#v", models)
	}
}

func TestSendRejectsMissingKeyAndUnsupportedKind(t *testing.T) {
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer server.Close()

	client := New(server.URL, "")
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindAnthropic}); err == nil || !strings.Contains(err.Error(), "no ZCode session") {
		t.Fatalf("Send without key error = %v, want missing-key error", err)
	}

	client.Key = "secret"
	if _, err := client.Send(context.Background(), &backend.Request{Kind: backend.KindOpenAIResponses}); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Send unsupported kind error = %v, want unsupported-kind error", err)
	}
	if reached {
		t.Error("upstream called for invalid credentials or unsupported kind")
	}
}

type staticTokenSource string

func (s staticTokenSource) AccessToken(context.Context) (string, error) {
	return string(s), nil
}

type staticTokenAndCaptchaSource struct{}

func (staticTokenAndCaptchaSource) AccessToken(context.Context) (string, error) {
	return "session-token", nil
}

func (staticTokenAndCaptchaSource) CaptchaVerifyParam(context.Context) (string, error) {
	return "source-param", nil
}

func TestSendUsesTokenSourceInsteadOfConfiguredKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Errorf("Authorization = %q, want session token", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "stale-configured-key")
	client.Tokens = staticTokenSource("session-token")
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		RawBody: []byte(`{"model":"glm-5.3-flash"}`),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestSendUsesCaptchaSourceWhenClientDidNotProvideOne(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(aliyunCaptchaHeader); got != "source-param" {
			t.Errorf("captcha header = %q, want source-param", got)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	client := New(server.URL, "stale-configured-key")
	client.Tokens = staticTokenAndCaptchaSource{}
	response, err := client.Send(context.Background(), &backend.Request{
		Kind:    backend.KindAnthropic,
		Header:  http.Header{aliyunCaptchaHeader: []string{"stale-client-param"}},
		RawBody: []byte(`{"model":"glm-5.3-flash"}`),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_ = response.Body.Close()
}

func TestBearerTokenAcceptsPrefixedKey(t *testing.T) {
	if got := bearerToken("Bearer secret"); got != "Bearer secret" {
		t.Errorf("bearerToken() = %q, want unchanged bearer token", got)
	}
}
