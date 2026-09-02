package zcode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// PreviewPlan is one claimable offer from ZCode's billing preview endpoint.
// Fields mirror the parts of the official payload an operator needs to pick
// an offer to claim.
type PreviewPlan struct {
	PlanID       string   `json:"plan_id"`
	Name         string   `json:"name,omitempty"`
	Description  string   `json:"description,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	StartsAt     int64    `json:"starts_at,omitempty"`
	EndsAt       int64    `json:"ends_at,omitempty"`
	Entitlements []string `json:"entitlements,omitempty"`
	GrantUnits   int64    `json:"grant_units,omitempty"`
	UnitType     string   `json:"unit_type,omitempty"`
	Period       string   `json:"period,omitempty"`
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
	req.Header.Set("X-Client-Language", zcodeLanguage)
	req.Header.Set("X-Client-Timezone", "UTC")
	req.Header.Set("X-Os-Category", runtime.GOOS)
	req.Header.Set("X-Os-Version", zcodeOSVersion)
	req.Header.Set("X-Device-Mid", deviceMID(token))
	req.Header.Set(aliyunCaptchaHeader, captcha)
	req.Header.Set(aliyunCaptchaRegionHeader, aliyunCaptchaRegion)

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return ClaimOutcome{}, fmt.Errorf("request ZCode plan claim: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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

// PreviewPlans lists the offers ZCode's billing preview endpoint advertises
// for the signed-in account. The gateway rejects the preview with code 3001
// unless the request carries a UUID-format X-Device-Mid, so the same stable
// identity ClaimPlan uses is sent here. No CAPTCHA proof is involved: the
// official client only attaches one to the claim itself.
func (m *Manager) PreviewPlans(ctx context.Context) ([]PreviewPlan, error) {
	token, err := m.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	platform := runtime.GOOS + "-" + zcodeArch()
	previewURL := strings.TrimRight(m.Issuer, "/") +
		"/api/v1/zcode-plan/billing/preview?app_version=" + url.QueryEscape(zcodeAppVersion) +
		"&platform=" + url.QueryEscape(platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, previewURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", bearerToken(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZCode/"+zcodeAppVersion)
	req.Header.Set("X-ZCode-App-Version", zcodeAppVersion)
	req.Header.Set("X-Title", "Z Code@electron")
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai")
	req.Header.Set("X-Platform", platform)
	req.Header.Set("X-Release-Channel", "production")
	req.Header.Set("X-Client-Language", zcodeLanguage)
	req.Header.Set("X-Client-Timezone", "UTC")
	req.Header.Set("X-Os-Category", runtime.GOOS)
	req.Header.Set("X-Os-Version", zcodeOSVersion)
	req.Header.Set("X-Device-Mid", deviceMID(token))

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ZCode plan preview: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ZCode plan preview response: %w", err)
	}
	var envelope struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Plans []struct {
				PlanID       string `json:"plan_id"`
				Name         string `json:"name"`
				Description  string `json:"description"`
				Priority     int    `json:"priority"`
				StartsAt     int64  `json:"starts_at"`
				EndsAt       int64  `json:"ends_at"`
				Entitlements []struct {
					EntitlementID string `json:"entitlement_id"`
					ShowName      string `json:"show_name"`
					GrantUnits    int64  `json:"grant_units"`
					UnitType      string `json:"unit_type"`
					Period        string `json:"period"`
				} `json:"entitlements"`
			} `json:"plans"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ZCode plan preview response (HTTP %d)", resp.StatusCode)
	}
	code := numericCode(envelope.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 {
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("ZCode plan preview failed (code %v, HTTP %d): %s", envelope.Code, resp.StatusCode, message)
	}
	plans := make([]PreviewPlan, 0, len(envelope.Data.Plans))
	for _, plan := range envelope.Data.Plans {
		if strings.TrimSpace(plan.PlanID) == "" {
			continue
		}
		preview := PreviewPlan{
			PlanID:      plan.PlanID,
			Name:        plan.Name,
			Description: plan.Description,
			Priority:    plan.Priority,
			StartsAt:    plan.StartsAt,
			EndsAt:      plan.EndsAt,
		}
		for _, entitlement := range plan.Entitlements {
			if entitlement.EntitlementID == "" {
				continue
			}
			preview.Entitlements = append(preview.Entitlements, entitlement.EntitlementID)
			if entitlement.GrantUnits > 0 {
				preview.GrantUnits = entitlement.GrantUnits
				preview.UnitType = entitlement.UnitType
				preview.Period = entitlement.Period
			}
		}
		plans = append(plans, preview)
	}
	return plans, nil
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
