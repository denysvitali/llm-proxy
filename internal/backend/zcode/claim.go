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
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
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

// PreviewEntitlement keeps one grant's identity and quota metadata together.
// These fields mirror getManualClaimPlanPreviews in the official ZCode client.
type PreviewEntitlement struct {
	EntitlementID string `json:"entitlement_id"`
	ShowName      string `json:"show_name,omitempty"`
	GrantUnits    int64  `json:"grant_units,omitempty"`
	UnitType      string `json:"unit_type,omitempty"`
	Period        string `json:"period,omitempty"`
}

// PreviewPlan is one claimable offer from ZCode's billing preview endpoint.
// Fields mirror the parts of the official payload an operator needs to pick
// an offer to claim.
type PreviewPlan struct {
	// EntitlementDetails is authoritative for automatic selection. The legacy
	// flattened fields below remain for existing manual-preview API consumers.
	EntitlementDetails []PreviewEntitlement `json:"entitlement_details,omitempty"`
	PlanID             string               `json:"plan_id"`
	Name               string               `json:"name,omitempty"`
	Description        string               `json:"description,omitempty"`
	Priority           int                  `json:"priority,omitempty"`
	StartsAt           int64                `json:"starts_at,omitempty"`
	EndsAt             int64                `json:"ends_at,omitempty"`
	Entitlements       []string             `json:"entitlements,omitempty"`
	GrantUnits         int64                `json:"grant_units,omitempty"`
	UnitType           string               `json:"unit_type,omitempty"`
	Period             string               `json:"period,omitempty"`
}

// ClaimPlan claims a plan advertised by ZCode's billing preview endpoint.
// ZCode requires the OAuth JWT, a fresh Aliyun verification parameter, and
// the same stable client/device identity used by Start Plan model requests.
func (m *Manager) ClaimPlan(ctx context.Context, planID string) (ClaimOutcome, error) {
	return m.claimPlan(ctx, planID, "", nil)
}

// before runs immediately before the POST, after authentication and CAPTCHA
// preparation. The stored session cannot change during the mutation.
func (m *Manager) claimPlan(ctx context.Context, planID, expectedToken string, before func() error) (ClaimOutcome, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return ClaimOutcome{}, fmt.Errorf("ZCode plan ID is required")
	}
	token, err := m.AccessToken(ctx)
	if err != nil {
		return ClaimOutcome{}, err
	}
	captcha, err := m.TakeCaptchaVerifyParam(ctx)
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
	req.Header.Set("X-Client-Timezone", zcodeClientTimezone())
	req.Header.Set("X-Os-Category", zcodeOSCategory())
	req.Header.Set("X-Os-Version", zcodeOSVersion)
	req.Header.Set("X-Device-Mid", m.DeviceMIDForToken(token))
	req.Header.Set(aliyunCaptchaHeader, captcha)
	req.Header.Set(aliyunCaptchaRegionHeader, aliyunCaptchaRegion)

	m.Store.mu.Lock()
	defer m.Store.mu.Unlock()
	current, err := m.Store.loadUnlocked()
	if err != nil || current == nil || current.AccessToken != token || (expectedToken != "" && token != expectedToken) {
		return ClaimOutcome{}, fmt.Errorf("ZCode session changed before claim")
	}
	if err := ctx.Err(); err != nil {
		return ClaimOutcome{}, err
	}
	if before != nil {
		if err := before(); err != nil {
			return ClaimOutcome{}, err
		}
	}
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
	req.Header.Set("X-Client-Timezone", zcodeClientTimezone())
	req.Header.Set("X-Os-Category", zcodeOSCategory())
	req.Header.Set("X-Os-Version", zcodeOSVersion)
	req.Header.Set("X-Device-Mid", m.DeviceMIDForToken(token))

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ZCode plan preview: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Only fixed classifications and aggregate counts belong in this log. Never
	// attach upstream messages, payloads, account identity, or offer metadata.
	// A count of -1 means decoding never established that count, not zero offers.
	diagnostic := logrus.Fields{
		"app_version": zcodeAppVersion, "http_status": resp.StatusCode,
		"envelope_code": -1, "data_state": "unknown", "plans_state": "unknown",
		"plan_count": -1, "accepted_count": 0, "invalid_id_count": -1,
		"outcome": "read_error",
	}
	defer func() { logrus.WithFields(diagnostic).Info("ZCode plan preview completed") }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ZCode plan preview response: %w", err)
	}
	diagnostic["outcome"] = "decode_error"
	var envelope struct {
		Code any             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ZCode plan preview response (HTTP %d)", resp.StatusCode)
	}
	diagnostic["data_state"] = previewJSONState(envelope.Data)
	var data struct {
		Plans json.RawMessage `json:"plans"`
	}
	dataErr := json.Unmarshal(envelope.Data, &data)
	if len(envelope.Data) > 0 && dataErr == nil {
		diagnostic["plans_state"] = previewJSONState(data.Plans)
	}
	code := numericCode(envelope.Code)
	diagnostic["envelope_code"] = code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 {
		diagnostic["outcome"] = "rejected"
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("ZCode plan preview failed (code %v, HTTP %d): %s", envelope.Code, resp.StatusCode, message)
	}
	// The official client rejects missing data rather than presenting a failed
	// or malformed preview as an account with no available offers.
	if previewJSONState(envelope.Data) != "present" {
		diagnostic["outcome"] = "missing_data"
		return nil, fmt.Errorf("ZCode plan preview response missing data (HTTP %d)", resp.StatusCode)
	}
	var upstreamPlans []struct {
		PlanID       string               `json:"plan_id"`
		Name         string               `json:"name"`
		Description  string               `json:"description"`
		Priority     int                  `json:"priority"`
		StartsAt     int64                `json:"starts_at"`
		EndsAt       int64                `json:"ends_at"`
		Entitlements []PreviewEntitlement `json:"entitlements"`
	}
	if dataErr != nil {
		return nil, fmt.Errorf("decode ZCode plan preview response (HTTP %d)", resp.StatusCode)
	}
	if len(data.Plans) > 0 {
		if err := json.Unmarshal(data.Plans, &upstreamPlans); err != nil {
			return nil, fmt.Errorf("decode ZCode plan preview response (HTTP %d)", resp.StatusCode)
		}
	}
	diagnostic["plan_count"] = len(upstreamPlans)
	invalidIDs := 0
	plans := make([]PreviewPlan, 0, len(upstreamPlans))
	for _, plan := range upstreamPlans {
		if strings.TrimSpace(plan.PlanID) == "" {
			invalidIDs++
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
			preview.EntitlementDetails = append(preview.EntitlementDetails, entitlement)
			if entitlement.GrantUnits > 0 {
				preview.GrantUnits = entitlement.GrantUnits
				preview.UnitType = entitlement.UnitType
				preview.Period = entitlement.Period
			}
		}
		plans = append(plans, preview)
	}
	diagnostic["accepted_count"] = len(plans)
	diagnostic["invalid_id_count"] = invalidIDs
	diagnostic["outcome"] = "ok"
	return plans, nil
}

// previewJSONState reports presence without ever returning upstream content.
func previewJSONState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "missing"
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
	}
	return "present"
}

func numericCode(code any) int {
	switch value := code.(type) {
	case float64:
		parsed := int(value)
		if float64(parsed) == value {
			return parsed
		}
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	// Missing or malformed result codes must never imply success.
	return -1
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
	case 3012:
		return "risk_blocked"
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
