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

// PlanUsage fetches the account's current plan usage and limits. ZCode gates
// the endpoint behind the same stable client identity as Start Plan model
// requests, but it needs no CAPTCHA verification parameter.
func (m *Manager) PlanUsage(ctx context.Context) ([]PlanUsage, error) {
	token, err := m.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.Issuer, "/")+"/api/v1/zcode-plan/billing/current?app_version="+zcodeAppVersion, nil)
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
	if release := kernelRelease(); release != "" {
		req.Header.Set("X-Os-Version", release)
	}
	req.Header.Set("X-Device-Mid", deviceMID(token))

	resp, err := m.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ZCode plan usage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ZCode plan usage response: %w", err)
	}
	var envelope struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Plans []struct {
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
			} `json:"plans"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode ZCode plan usage response (HTTP %d)", resp.StatusCode)
	}
	code := numericCode(envelope.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != 0 {
		message := strings.TrimSpace(envelope.Msg)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("ZCode plan usage request failed (HTTP %d, code %v): %s", resp.StatusCode, envelope.Code, message)
	}
	plans := make([]PlanUsage, 0, len(envelope.Data.Plans))
	for _, plan := range envelope.Data.Plans {
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
	return plans, nil
}
