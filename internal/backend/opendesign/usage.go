package opendesign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend/upstream"
)

// DefaultAccountAPIURL is the Open Design account (control-plane) API. It is
// distinct from the amr-link inference endpoint and takes the account
// control key, not an sk-amr API key.
const DefaultAccountAPIURL = "https://amr-api.open-design.ai"

// ErrUsageUnauthorized reports a rejected or revoked control key.
var ErrUsageUnauthorized = errors.New("open design rejected the account control key")

// PlanWindow is one Coding Plan allowance window (e.g. 5 hours, 7 days).
// Credit amounts are Open Design billing credits, not USD.
type PlanWindow struct {
	PolicyID         string     `json:"policyId"`
	DurationSeconds  int64      `json:"durationSeconds"`
	ResetMode        string     `json:"resetMode"`
	UsedCredits      int64      `json:"usedCredits"`
	RemainingCredits int64      `json:"remainingCredits"`
	LimitCredits     int64      `json:"limitCredits"`
	WindowStart      *time.Time `json:"windowStart,omitempty"`
	ResetsAt         *time.Time `json:"resetsAt,omitempty"`
}

// Usage is a workspace's plan state as reported by funding-check.
type Usage struct {
	WorkspaceID string       `json:"workspaceId"`
	Tier        string       `json:"tier,omitempty"`
	Eligible    bool         `json:"eligible"`
	Funding     string       `json:"funding"`
	BalanceUSD  string       `json:"balanceUsd"`
	Windows     []PlanWindow `json:"windows"`
	// CreditsPerUSD converts credits to dollars; zero when the billing
	// summary did not state a usable rate.
	CreditsPerUSD float64   `json:"creditsPerUsd,omitempty"`
	GeneratedAt   time.Time `json:"generatedAt"`
}

// UsageClient reads Coding Plan usage from the account API.
type UsageClient struct {
	APIURL      string
	ControlKey  string
	WorkspaceID string
	HTTP        *http.Client

	mu         sync.Mutex
	discovered string
}

// NewUsageClient returns a client for the account API; an empty workspaceID
// selects the account's default workspace on first use.
func NewUsageClient(apiURL, controlKey, workspaceID string) *UsageClient {
	if apiURL == "" {
		apiURL = DefaultAccountAPIURL
	}
	return &UsageClient{
		APIURL:      strings.TrimRight(apiURL, "/"),
		ControlKey:  controlKey,
		WorkspaceID: strings.TrimSpace(workspaceID),
		HTTP:        upstream.NewClient(15 * time.Second),
	}
}

// Usage fetches the workspace's plan windows and, best effort, the
// credits-per-USD rate needed to show dollar amounts.
func (c *UsageClient) Usage(ctx context.Context) (Usage, error) {
	workspace, err := c.workspace(ctx)
	if err != nil {
		return Usage{}, err
	}
	var raw fundingCheck
	if err := c.get(ctx, "/api/v1/workspaces/"+url.PathEscape(workspace)+"/billing/funding-check", &raw); err != nil {
		return Usage{}, err
	}
	usage, err := raw.usage(workspace)
	if err != nil {
		return Usage{}, err
	}
	var summary struct {
		CreditsPerUSD json.Number `json:"creditsPerUsd"`
	}
	// The rate only adds dollar labels; percentages stand without it.
	if c.get(ctx, "/api/v1/billing/summary", &summary) == nil {
		if rate, err := summary.CreditsPerUSD.Float64(); err == nil && rate > 0 {
			usage.CreditsPerUSD = rate
		}
	}
	return usage, nil
}

// workspace resolves the configured workspace, or the account's default
// active one. An ambiguous directory needs explicit configuration.
func (c *UsageClient) workspace(ctx context.Context) (string, error) {
	if c.WorkspaceID != "" {
		return c.WorkspaceID, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discovered != "" {
		return c.discovered, nil
	}
	var directory struct {
		Items []struct {
			WorkspaceID    string `json:"workspaceId"`
			WorkspaceType  string `json:"workspaceType"`
			MemberStatus   string `json:"memberStatus"`
			LifecycleState string `json:"lifecycleState"`
			IsDefault      bool   `json:"isDefault"`
		} `json:"items"`
	}
	if err := c.get(ctx, "/api/v1/workspaces", &directory); err != nil {
		return "", err
	}
	var active, personal []string
	for _, item := range directory.Items {
		if item.WorkspaceID == "" || item.MemberStatus != "active" || (item.LifecycleState != "" && item.LifecycleState != "active") {
			continue
		}
		if item.IsDefault {
			c.discovered = item.WorkspaceID
			return c.discovered, nil
		}
		active = append(active, item.WorkspaceID)
		if item.WorkspaceType == "personal" {
			personal = append(personal, item.WorkspaceID)
		}
	}
	switch {
	case len(active) == 1:
		c.discovered = active[0]
	case len(personal) == 1:
		c.discovered = personal[0]
	case len(active) == 0:
		return "", errors.New("open design account has no active workspace")
	default:
		return "", errors.New("open design account has several workspaces; set workspace_id")
	}
	return c.discovered, nil
}

func (c *UsageClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.ControlKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("open design account API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("open design account API: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUsageUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Body: body}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode open design %s: %w", path, err)
	}
	return nil
}

// fundingCheck mirrors the schemaVersion 1 funding-check body. Credits are
// decimal strings so large allowances survive JSON number precision.
type fundingCheck struct {
	SchemaVersion int       `json:"schemaVersion"`
	WorkspaceID   string    `json:"workspaceId"`
	GeneratedAt   time.Time `json:"generatedAt"`
	BalanceUSD    string    `json:"balanceUsd"`
	Funding       string    `json:"funding"`
	CodingPlan    *struct {
		Eligible bool    `json:"eligible"`
		Tier     *string `json:"tier"`
		Windows  []struct {
			PolicyID         string     `json:"policyId"`
			DurationSeconds  int64      `json:"durationSeconds"`
			ResetMode        string     `json:"resetMode"`
			UsedCredits      string     `json:"usedCredits"`
			RemainingCredits string     `json:"remainingCredits"`
			LimitCredits     string     `json:"limitCredits"`
			WindowStart      *time.Time `json:"windowStart"`
			ResetsAt         *time.Time `json:"resetsAt"`
		} `json:"windows"`
	} `json:"codingPlan"`
}

func (f fundingCheck) usage(workspace string) (Usage, error) {
	if f.SchemaVersion != 1 {
		return Usage{}, fmt.Errorf("open design funding-check schemaVersion %d is unsupported", f.SchemaVersion)
	}
	if f.WorkspaceID != workspace || f.CodingPlan == nil {
		return Usage{}, errors.New("open design funding-check response is incomplete")
	}
	usage := Usage{
		WorkspaceID: workspace,
		Eligible:    f.CodingPlan.Eligible,
		Funding:     f.Funding,
		BalanceUSD:  f.BalanceUSD,
		GeneratedAt: f.GeneratedAt,
		Windows:     make([]PlanWindow, 0, len(f.CodingPlan.Windows)),
	}
	if f.CodingPlan.Tier != nil {
		usage.Tier = *f.CodingPlan.Tier
	}
	for _, w := range f.CodingPlan.Windows {
		used, errUsed := credits(w.UsedCredits)
		remaining, errRemaining := credits(w.RemainingCredits)
		limit, errLimit := credits(w.LimitCredits)
		if err := errors.Join(errUsed, errRemaining, errLimit); err != nil || limit <= 0 || remaining > limit || w.DurationSeconds <= 0 {
			// One malformed window would make every percentage suspect.
			return Usage{}, fmt.Errorf("open design plan window %q is malformed", w.PolicyID)
		}
		usage.Windows = append(usage.Windows, PlanWindow{
			PolicyID: w.PolicyID, DurationSeconds: w.DurationSeconds, ResetMode: w.ResetMode,
			UsedCredits: used, RemainingCredits: remaining, LimitCredits: limit,
			WindowStart: w.WindowStart, ResetsAt: w.ResetsAt,
		})
	}
	return usage, nil
}

func credits(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid credit amount %q", value)
	}
	return n, nil
}
