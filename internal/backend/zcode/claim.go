package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
)

// ClaimOutcome is the normalized result of a ZCode manual-plan claim. It
// deliberately excludes the account JWT and CAPTCHA verification parameter.
type ClaimOutcome struct {
	OK          bool   `json:"ok"`
	PlanID      string `json:"plan_id"`
	FailureKind string `json:"failure_kind,omitempty"`
	Code        any    `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
	StartsAt    int64  `json:"starts_at,omitempty"`
	EndsAt      int64  `json:"ends_at,omitempty"`
}

// ClaimPlan claims a plan advertised by ZCode's billing preview endpoint.
// ZCode requires the OAuth JWT, a fresh Aliyun verification parameter, and
// the same stable client/device identity used by Start Plan model requests.
func (m *Manager) ClaimPlan(ctx context.Context, planID string) (ClaimOutcome, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return ClaimOutcome{}, fmt.Errorf("ZCode plan ID is required")
	}
	token, err := m.AccessToken(ctx)
	if err != nil {
		return ClaimOutcome{}, err
	}
	captcha, err := m.CaptchaVerifyParam(ctx)
	if err != nil {
		return ClaimOutcome{}, err
	}
	body, err := json.Marshal(map[string]string{"plan_id": planID})
	if err != nil {
		return ClaimOutcome{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.Issuer, "/")+"/api/v1/zcode-plan/billing/claim", bytes.NewReader(body))
	if err != nil {
		return ClaimOutcome{}, err
	}
	req.Header.Set("Authorization", bearerToken(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZCode/"+zcodeAppVersion)
	req.Header.Set("X-ZCode-App-Version", zcodeAppVersion)
	req.Header.Set("X-Title", "Z Code@electron")
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai")
	req.Header.Set("X-Platform", runtime.GOOS+"-"+zcodeArch())
	req.Header.Set("X-Release-Channel", "production")
	req.Header.Set("X-Client-Language", "en")
	req.Header.Set("X-Client-Timezone", "UTC")
	req.Header.Set("X-Os-Category", runtime.GOOS)
	if release := kernelRelease(); release != "" {
		req.Header.Set("X-Os-Version", release)
	}
	req.Header.Set("X-Device-Mid", deviceMID(token))
	req.Header.Set(aliyunCaptchaHeader, captcha)
	req.Header.Set(aliyunCaptchaRegionHeader, aliyunCaptchaRegion)

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return ClaimOutcome{}, fmt.Errorf("request ZCode plan claim: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ClaimOutcome{}, fmt.Errorf("read ZCode plan claim response: %w", err)
	}
	var envelope struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Plan struct {
				StartsAt int64 `json:"starts_at"`
				EndsAt   int64 `json:"ends_at"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ClaimOutcome{}, fmt.Errorf("decode ZCode plan claim response (HTTP %d)", resp.StatusCode)
	}
	code := numericCode(envelope.Code)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && code == 0 {
		return ClaimOutcome{OK: true, PlanID: planID, StartsAt: envelope.Data.Plan.StartsAt, EndsAt: envelope.Data.Plan.EndsAt}, nil
	}
	if code == 3007 {
		m.InvalidateCaptchaContext(ctx, captcha)
	}
	message := strings.TrimSpace(envelope.Msg)
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return ClaimOutcome{
		PlanID:      planID,
		FailureKind: classifyClaimCode(code, resp.StatusCode),
		Code:        envelope.Code,
		Message:     message,
		StartsAt:    envelope.Data.Plan.StartsAt,
		EndsAt:      envelope.Data.Plan.EndsAt,
	}, nil
}

func numericCode(code any) int {
	switch value := code.(type) {
	case float64:
		return int(value)
	case string:
		var parsed int
		_, _ = fmt.Sscanf(value, "%d", &parsed)
		return parsed
	default:
		return 0
	}
}

func classifyClaimCode(code, status int) string {
	switch code {
	case 1001:
		return "not_found"
	case 1002:
		return "unavailable"
	case 1003:
		return "already_claimed"
	case 1004:
		return "ineligible"
	case 1005:
		return "quota_exhausted"
	case 3001:
		return "invalid_request"
	case 3007:
		return "captcha"
	case 401:
		return "login_required"
	}
	if status == http.StatusUnauthorized {
		return "login_required"
	}
	if status >= 400 {
		return "http_error"
	}
	return "unknown"
}
