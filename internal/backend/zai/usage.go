package zai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The monitor endpoints are what Z.ai's own usage page calls. They are not
// part of the published API, so parsing is lenient: unknown limit types and
// units are skipped or left without a duration rather than failing the read.
const (
	quotaPath        = "/monitor/usage/quota/limit"
	subscriptionPath = "/biz/subscription/list"
)

// ErrUsageUnauthorized reports a rejected or revoked API key.
var ErrUsageUnauthorized = errors.New("z.ai rejected the API key")

// Usage is the Coding Plan quota state.
type Usage struct {
	// Plan is the subscription's product name (e.g. "GLM Coding Pro");
	// empty when the subscription list could not be read.
	Plan      string    `json:"plan,omitempty"`
	Level     string    `json:"level,omitempty"`
	Windows   []Window  `json:"windows"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// Window is one quota window. Kind is "prompts" for the token/credit plan
// allowance and "tools" for the MCP tool-call allowance (web search, web
// reader, zread). Counts are nil when Z.ai reports only a percentage.
type Window struct {
	Kind            string      `json:"kind"`
	DurationSeconds int64       `json:"durationSeconds,omitempty"`
	UsedPercent     float64     `json:"usedPercent"`
	Used            *float64    `json:"used,omitempty"`
	Limit           *float64    `json:"limit,omitempty"`
	Remaining       *float64    `json:"remaining,omitempty"`
	ResetsAt        *time.Time  `json:"resetsAt,omitempty"`
	Details         []ToolUsage `json:"details,omitempty"`
}

type ToolUsage struct {
	Name  string  `json:"name"`
	Usage float64 `json:"usage"`
}

// Usage reads the quota windows and, best effort, the plan name.
func (c *Client) Usage(ctx context.Context) (Usage, error) {
	if c.Key == "" {
		return Usage{}, fmt.Errorf("zai backend has no API key configured")
	}
	var (
		plan string
		wg   sync.WaitGroup
	)
	wg.Go(func() { plan = c.planName(ctx) })
	var quota quotaData
	err := c.monitorGet(ctx, quotaPath, &quota)
	wg.Wait()
	if err != nil {
		return Usage{}, err
	}
	usage := Usage{Plan: plan, Level: quota.Level, Windows: make([]Window, 0, len(quota.Limits)), FetchedAt: time.Now().UTC()}
	for _, limit := range quota.Limits {
		if window, ok := limit.window(); ok {
			usage.Windows = append(usage.Windows, window)
		}
	}
	return usage, nil
}

func (c *Client) planName(ctx context.Context) string {
	var subs []struct {
		ProductName string `json:"productName"`
		Status      string `json:"status"`
	}
	if err := c.monitorGet(ctx, subscriptionPath, &subs); err != nil {
		return ""
	}
	name := ""
	for _, sub := range subs {
		if strings.TrimSpace(sub.ProductName) == "" {
			continue
		}
		if name == "" || strings.EqualFold(sub.Status, "VALID") {
			name = strings.TrimSpace(sub.ProductName)
		}
		if strings.EqualFold(sub.Status, "VALID") {
			break
		}
	}
	return name
}

// monitorGet decodes the {code, msg, success, data} envelope into out.
// Community clients disagree on whether these endpoints want the bare key or
// a Bearer token in Authorization, so a 401 on one retries with the other.
func (c *Client) monitorGet(ctx context.Context, path string, out any) error {
	status, body, err := c.get(ctx, path, c.Key)
	if err == nil && status == http.StatusUnauthorized {
		status, body, err = c.get(ctx, path, "Bearer "+c.Key)
	}
	if err != nil {
		return fmt.Errorf("z.ai usage: %w", err)
	}
	var env struct {
		Code    int             `json:"code"`
		Msg     string          `json:"msg"`
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	decodeErr := json.Unmarshal(body, &env)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || (decodeErr == nil && env.Code == http.StatusUnauthorized):
		return ErrUsageUnauthorized
	case status < 200 || status >= 300:
		return &HTTPError{Status: status, Body: body}
	case decodeErr != nil:
		return fmt.Errorf("decode z.ai %s: %w", path, decodeErr)
	case !env.Success:
		msg := strings.TrimSpace(env.Msg)
		if msg == "" {
			msg = fmt.Sprintf("code %d", env.Code)
		}
		return fmt.Errorf("z.ai usage: %s", msg)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode z.ai %s: %w", path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path, authorization string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, err
}

type quotaData struct {
	Level  string       `json:"level"`
	Limits []quotaLimit `json:"limits"`
}

type quotaLimit struct {
	Type          string   `json:"type"`
	Name          string   `json:"name"` // older payloads carried the type here
	Unit          int      `json:"unit"`
	Number        int64    `json:"number"`
	Percentage    float64  `json:"percentage"`
	Usage         *float64 `json:"usage"` // the window's limit, despite the name
	CurrentValue  *float64 `json:"currentValue"`
	Remaining     *float64 `json:"remaining"`
	NextResetTime *int64   `json:"nextResetTime"` // Unix milliseconds
	UsageDetails  []struct {
		ModelCode string  `json:"modelCode"`
		Usage     float64 `json:"usage"`
	} `json:"usageDetails"`
}

// unitSeconds maps Z.ai's undocumented unit codes, as observed in captured
// responses (5-hour = unit 3 × 5, weekly = unit 6 × 1, monthly = unit 5 × 1).
var unitSeconds = map[int]int64{3: 3600, 4: 86400, 5: 30 * 86400, 6: 7 * 86400}

func (l quotaLimit) window() (Window, bool) {
	kind := l.Type
	if kind == "" {
		kind = l.Name
	}
	var w Window
	switch kind {
	case "TOKENS_LIMIT", "CREDIT_LIMIT": // renamed to CREDIT_LIMIT in 2026
		w.Kind = "prompts"
	case "TIME_LIMIT":
		w.Kind = "tools"
	default:
		return Window{}, false
	}
	if seconds, ok := unitSeconds[l.Unit]; ok && l.Number > 0 {
		w.DurationSeconds = seconds * l.Number
	}
	w.UsedPercent = l.Percentage
	w.Used, w.Limit, w.Remaining = l.CurrentValue, l.Usage, l.Remaining
	// Counts are more precise than the integer percentage when present.
	if w.Used != nil && w.Limit != nil && *w.Limit > 0 {
		w.UsedPercent = *w.Used * 100 / *w.Limit
	}
	if l.NextResetTime != nil && *l.NextResetTime > 0 {
		at := time.UnixMilli(*l.NextResetTime).UTC()
		w.ResetsAt = &at
	}
	for _, d := range l.UsageDetails {
		w.Details = append(w.Details, ToolUsage{Name: d.ModelCode, Usage: d.Usage})
	}
	return w, true
}
