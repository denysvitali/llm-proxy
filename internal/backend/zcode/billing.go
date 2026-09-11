package zcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
)

// PlanUsage is one plan reported by ZCode's billing/current endpoint: the
// plan identity plus the units granted and consumed for the current period.
// A plan without an active entitlement carries Kind "unavailable" and a
// Reason instead of unit totals.
type PlanUsage struct {
	PlanID         string  `json:"plan_id"`
	Name           string  `json:"name,omitempty"`
	Status         string  `json:"status,omitempty"`
	Kind           string  `json:"kind,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	TotalUnits     float64 `json:"total_units,omitempty"`
	UsedUnits      float64 `json:"used_units,omitempty"`
	AvailableUnits float64 `json:"available_units,omitempty"`
	PeriodStart    int64   `json:"period_start,omitempty"`
	PeriodEnd      int64   `json:"period_end,omitempty"`
}

// PlanBalance is one quota bucket reported by ZCode's billing/balance
// endpoint. Units are model tokens (or the provider's equivalent meter unit),
// and ExpiresAt is a Unix timestamp in seconds.
type PlanBalance struct {
	EntitlementID  string   `json:"entitlement_id,omitempty"`
	PlanID         string   `json:"plan_id,omitempty"`
	ShowName       string   `json:"show_name,omitempty"`
	Meter          string   `json:"meter,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	TotalUnits     float64  `json:"total_units,omitempty"`
	UsedUnits      float64  `json:"used_units,omitempty"`
	RemainingUnits float64  `json:"remaining_units,omitempty"`
	AvailableUnits float64  `json:"available_units,omitempty"`
	ReservedUnits  float64  `json:"reserved_units,omitempty"`
	ExpiresAt      int64    `json:"expires_at,omitempty"`
}

// PlanQuota is the normalized response from ZCode's billing/balance
// endpoint. Plans identify the active entitlement; Balances contain the
// quota counters the desktop app displays.
type PlanQuota struct {
	Plans    []PlanUsage   `json:"plans"`
	Balances []PlanBalance `json:"balances"`
}

type planUsagePayload struct {
	PlanID         string        `json:"plan_id"`
	Name           string        `json:"name"`
	Status         string        `json:"status"`
	Kind           string        `json:"kind"`
	Reason         string        `json:"reason"`
	TotalUnits     flexibleFloat `json:"total_units"`
	UsedUnits      flexibleFloat `json:"used_units"`
	AvailableUnits flexibleFloat `json:"available_units"`
	PeriodStart    int64         `json:"period_start"`
	PeriodEnd      int64         `json:"period_end"`
}

type planBalancePayload struct {
	EntitlementID  string        `json:"entitlement_id"`
	PlanID         string        `json:"plan_id"`
	ShowName       string        `json:"show_name"`
	Meter          string        `json:"meter"`
	Capabilities   []string      `json:"capabilities"`
	TotalUnits     flexibleFloat `json:"total_units"`
	UsedUnits      flexibleFloat `json:"used_units"`
	RemainingUnits flexibleFloat `json:"remaining_units"`
	AvailableUnits flexibleFloat `json:"available_units"`
	ReservedUnits  flexibleFloat `json:"reserved_units"`
	ExpiresAt      flexibleFloat `json:"expires_at"`
}

type planBillingEnvelope struct {
	Code any    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Plans    []planUsagePayload   `json:"plans"`
		Balances []planBalancePayload `json:"balances"`
	} `json:"data"`
}

// PlanUsage fetches the account's current plan usage and limits. ZCode gates
// the endpoint behind the same stable client identity as Start Plan model
// requests, but it needs no CAPTCHA verification parameter.
func (m *Manager) PlanUsage(ctx context.Context) ([]PlanUsage, error) {
	envelope, err := m.fetchPlanBilling(ctx, "/api/v1/zcode-plan/billing/current?app_version="+zcodeAppVersion, "plan usage")
	if err != nil {
		return nil, err
	}
	plans := normalizePlanUsage(envelope.Data.Plans)
	return plans, nil
}

// PlanQuota fetches the account's live quota buckets from the same
// billing/balance endpoint used by the ZCode desktop client. Unlike
// PlanUsage, this includes remaining/expiry counters for each entitlement.
func (m *Manager) PlanQuota(ctx context.Context) (PlanQuota, error) {
	envelope, err := m.fetchPlanBilling(ctx, "/api/v1/zcode-plan/billing/balance?app_version="+zcodeAppVersion, "plan balance")
	if err != nil {
		return PlanQuota{}, err
	}
	return PlanQuota{
		Plans:    normalizePlanUsage(envelope.Data.Plans),
		Balances: normalizePlanBalances(envelope.Data.Balances),
	}, nil
}

func (m *Manager) fetchPlanBilling(ctx context.Context, path, label string) (planBillingEnvelope, error) {
	token, err := m.AccessToken(ctx)
	if err != nil {
		return planBillingEnvelope{}, err
	}
	req, err := m.newPlanBillingRequest(ctx, path, token)
	if err != nil {
		return planBillingEnvelope{}, err
	}
	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return planBillingEnvelope{}, fmt.Errorf("request ZCode %s: %w", label, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return planBillingEnvelope{}, fmt.Errorf("read ZCode %s response: %w", label, err)
	}
	var envelope planBillingEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return planBillingEnvelope{}, fmt.Errorf("decode ZCode %s response (HTTP %d)", label, resp.StatusCode)
	}
	code := numericCode(envelope.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 {
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return planBillingEnvelope{}, fmt.Errorf("ZCode %s request failed (HTTP %d, code %v): %s", label, resp.StatusCode, envelope.Code, message)
	}
	return envelope, nil
}

func (m *Manager) newPlanBillingRequest(ctx context.Context, path, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.Issuer, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", bearerToken(token))
	req.Header.Set("Accept", "application/json")
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
	return req, nil
}

func normalizePlanUsage(payload []planUsagePayload) []PlanUsage {
	plans := make([]PlanUsage, 0, len(payload))
	for _, plan := range payload {
		plans = append(plans, PlanUsage{
			PlanID:         plan.PlanID,
			Name:           plan.Name,
			Status:         plan.Status,
			Kind:           plan.Kind,
			Reason:         plan.Reason,
			TotalUnits:     float64(plan.TotalUnits),
			UsedUnits:      float64(plan.UsedUnits),
			AvailableUnits: float64(plan.AvailableUnits),
			PeriodStart:    plan.PeriodStart,
			PeriodEnd:      plan.PeriodEnd,
		})
	}
	return plans
}

func normalizePlanBalances(payload []planBalancePayload) []PlanBalance {
	balances := make([]PlanBalance, 0, len(payload))
	for _, balance := range payload {
		balances = append(balances, PlanBalance{
			EntitlementID:  balance.EntitlementID,
			PlanID:         balance.PlanID,
			ShowName:       balance.ShowName,
			Meter:          balance.Meter,
			Capabilities:   append([]string(nil), balance.Capabilities...),
			TotalUnits:     float64(balance.TotalUnits),
			UsedUnits:      float64(balance.UsedUnits),
			RemainingUnits: float64(balance.RemainingUnits),
			AvailableUnits: float64(balance.AvailableUnits),
			ReservedUnits:  float64(balance.ReservedUnits),
			ExpiresAt:      int64(balance.ExpiresAt),
		})
	}
	return balances
}
