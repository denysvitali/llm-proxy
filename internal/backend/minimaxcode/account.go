package minimaxcode

// Account and check-in wire contracts are adapted from MiniMax Code 0.5.5,
// packages/tui/src/{account/matrix-account-client,checkin/http-gateway,runtime/public-gateway}.ts
// and packages/shared/src/daily-signin.ts at commit 0f6ad5229ff1f144c72dd15a4b2d520feb26cd3d.
// See MINIMAX-LICENSE for the upstream MIT license.

import (
	"context"
	"crypto/md5" // Public client attribution, not authentication; OAuth supplies authorization.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const accountOrigin = "https://agent.minimax.io"
const platformOrigin = "https://platform.minimax.io"
const accountResponseLimit = 1 << 20

var ErrCheckinInProgress = errors.New("MiniMax Code check-in is already in progress")
var ErrCheckinUnavailable = errors.New("MiniMax Code has no daily reward available to claim")
var ErrCheckinAlreadyClaimed = errors.New("MiniMax Code daily reward was already claimed today")

type AccountUsage struct {
	HasTokenPlan  *bool         `json:"has_token_plan,omitempty"`
	Tier          string        `json:"tier,omitempty"`
	ExpiresAtMS   *float64      `json:"expires_at_ms,omitempty"`
	CreditBalance *string       `json:"credit_balance,omitempty"`
	QuotaState    string        `json:"quota_state"`
	Quota         *AccountQuota `json:"quota,omitempty"`
}
type AccountQuota struct {
	FiveHour QuotaWindow `json:"five_hour"`
	Weekly   QuotaWindow `json:"weekly"`
	Video    *VideoQuota `json:"video,omitempty"`
}
type QuotaWindow struct {
	RemainingPercent *float64 `json:"remaining_percent,omitempty"`
	ResetAtMS        *float64 `json:"reset_at_ms,omitempty"`
	Unlimited        bool     `json:"unlimited"`
}
type VideoQuota struct {
	RemainingCount *float64 `json:"remaining_count,omitempty"`
	TotalCount     *float64 `json:"total_count,omitempty"`
	ResetAtMS      *float64 `json:"reset_at_ms,omitempty"`
	Unlimited      bool     `json:"unlimited"`
}
type CheckinPanel struct {
	Scene int          `json:"scene"`
	Days  []CheckinDay `json:"days"`
}
type CheckinDay struct {
	DayNo       int      `json:"day_no"`
	Points      float64  `json:"points"`
	BonusPoints *float64 `json:"bonus_points,omitempty"`
	Status      int      `json:"status"`
	IsToday     bool     `json:"is_today"`
}
type CheckinClaim struct {
	ClaimResult int          `json:"claim_result"`
	DayNo       int          `json:"day_no"`
	Points      float64      `json:"points"`
	ExpireAtMS  float64      `json:"expire_at_ms"`
	Panel       CheckinPanel `json:"panel"`
}

// Account reads personal workspace membership and quota. Missing quota remains
// unavailable; it must not be presented as zero remaining credits or usage.
func (m *Manager) Account(ctx context.Context) (*AccountUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, userID, err := m.accountIdentity(ctx)
	if err != nil {
		return nil, err
	}
	extra, err := m.accountRequest(ctx, "/matrix/api/v1/user/get_user_extra_info", http.MethodPost, "{}", token, userID, false)
	usage := &AccountUsage{QuotaState: "unavailable"}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Older accounts may lack workspace discovery. Membership still exposes
		// credits, but without a personal group we cannot safely scope quota.
		fallback, fallbackErr := m.accountRequest(ctx, "/matrix/api/v1/commerce/get_membership_info", http.MethodPost, "{}", token, userID, false)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		projectMembership(usage, fallback)
		if usage.HasTokenPlan != nil && !*usage.HasTokenPlan {
			usage.QuotaState = "not-subscribed"
		}
		return usage, nil
	}
	workspaces, _ := field(extra, "workspaces").([]any)
	var groupID string
	for _, value := range workspaces {
		workspace := object(value)
		kind := number(workspace["workspace_type"])
		id := workspace["workspace_id"]
		validID := nonempty(id) != "" || (number(id) != nil && *number(id) >= 0)
		if kind == nil || *kind != 0 || !validID {
			continue
		}
		projectMembership(usage, workspace)
		groupID = nonempty(workspace["op_group_id"])
		payload, _ := json.Marshal(map[string]any{"workspace_id": id})
		scoped, scopedErr := m.accountRequest(ctx, "/matrix/api/v1/commerce/get_membership_info", http.MethodPost, string(payload), token, userID, false)
		if scopedErr == nil {
			projectMembership(usage, scoped)
		}
		break
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if usage.HasTokenPlan != nil && !*usage.HasTokenPlan {
		usage.QuotaState = "not-subscribed"
		return usage, nil
	}
	if groupID == "" {
		return usage, nil
	}
	origin := m.PlatformBaseURL
	if origin == "" {
		origin = platformOrigin
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/v1/api/openplatform/coding_plan/remains", nil)
	if err != nil {
		return nil, errors.New("MiniMax Code quota URL is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Group-Id", groupID)
	body, err := m.readAccountResponse(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return usage, nil
	}
	remains, _ := body["model_remains"].([]any)
	for _, value := range remains {
		item := object(value)
		if item == nil {
			continue
		}
		if usage.Quota == nil {
			usage.Quota = &AccountQuota{FiveHour: quotaWindow(item, "current_interval", "end_time"), Weekly: quotaWindow(item, "current_weekly", "weekly_end_time")}
		}
		total := number(item["current_interval_total_count"])
		if strings.Contains(strings.ToLower(nonempty(item["model_name"])), "video") && total != nil && *total > 0 {
			usage.Quota.Video = &VideoQuota{RemainingCount: nonnegative(number(item["current_interval_usage_count"])), TotalCount: nonnegative(total), ResetAtMS: positive(number(item["end_time"])), Unlimited: isNumber(item["current_interval_status"], 3)}
		}
	}
	if usage.Quota != nil {
		usage.QuotaState = "available"
	}
	return usage, nil
}

func projectMembership(usage *AccountUsage, body map[string]any) {
	if value, ok := field(body, "has_token_plan").(bool); ok {
		// Either membership may positively establish a subscription.
		if usage.HasTokenPlan == nil || value || !*usage.HasTokenPlan {
			usage.HasTokenPlan = &value
		}
	}
	if value := nonempty(field(body, "token_plan_tier")); value != "" {
		usage.Tier = value
	}
	if value := positive(number(field(body, "token_plan_expires_at"))); value != nil {
		usage.ExpiresAtMS = value
	}
	credit := object(field(body, "op_credit_summary"))["total_remaining_amount"]
	if credit == nil {
		credit = field(body, "opcredit_balance")
	}
	switch value := credit.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			value = strings.TrimSpace(value)
			usage.CreditBalance = &value
		}
	case float64:
		text := strconv.FormatFloat(value, 'f', -1, 64)
		usage.CreditBalance = &text
	}
}
func quotaWindow(item map[string]any, prefix, reset string) QuotaWindow {
	window := QuotaWindow{ResetAtMS: positive(number(item[reset])), Unlimited: isNumber(item[prefix+"_status"], 3)}
	remaining := number(item[prefix+"_remaining_percent"])
	total, count := number(item[prefix+"_total_count"]), number(item[prefix+"_usage_count"])
	if remaining == nil && total != nil && *total > 0 && count != nil {
		value := *count / *total * 100
		remaining = &value
	}
	if remaining != nil && !window.Unlimited {
		value := math.Round(math.Min(100, math.Max(0, *remaining)))
		window.RemainingPercent = &value
	}
	return window
}

func (m *Manager) CheckinStatus(ctx context.Context) (*CheckinPanel, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, userID, err := m.accountIdentity(ctx)
	if err != nil {
		return nil, err
	}
	return m.checkinStatus(ctx, token, userID)
}
func (m *Manager) checkinStatus(ctx context.Context, token, userID string) (*CheckinPanel, error) {
	body, err := m.accountRequest(ctx, "/minimax-cloud/api/v1/signin/status", http.MethodGet, "", token, userID, true)
	if err != nil {
		return nil, err
	}
	return parseCheckinPanel(body["data"])
}

// ClaimCheckin is the only reward mutation. It validates fresh eligibility and
// sends at most one POST, even if the upstream response is lost or unauthorized.
func (m *Manager) ClaimCheckin(ctx context.Context) (*CheckinClaim, error) {
	return m.claimCheckin(ctx, nil)
}

// claimCheckin runs beforeClaim after fresh eligibility and account checks,
// immediately before the only reward mutation.
func (m *Manager) claimCheckin(ctx context.Context, beforeClaim func(userID string) error) (*CheckinClaim, error) {
	if !m.claimMu.TryLock() {
		return nil, ErrCheckinInProgress
	}
	defer m.claimMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, userID, err := m.accountIdentity(ctx)
	if err != nil {
		return nil, err
	}
	panel, err := m.checkinStatus(ctx, token, userID)
	if err != nil {
		return nil, err
	}
	claimable := false
	for _, day := range panel.Days {
		if day.IsToday && day.Status == 3 {
			return nil, ErrCheckinAlreadyClaimed
		}
		claimable = claimable || day.Status == 2
	}
	if !claimable {
		return nil, ErrCheckinUnavailable
	}
	// A new login must not redirect a pending claim to another account.
	current, err := m.Store.Load()
	if err != nil || current == nil || current.AccessToken != token {
		return nil, errors.New("MiniMax Code account changed; refresh before checking in")
	}
	if beforeClaim != nil {
		if err := beforeClaim(userID); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := m.accountRequest(ctx, "/minimax-cloud/api/v1/signin/claim", http.MethodPost, "{}", token, userID, true)
	if err != nil {
		return nil, err
	}
	current, err = m.Store.Load()
	if err != nil || current == nil || current.AccessToken != token {
		return nil, errors.New("MiniMax Code account changed; refresh to confirm check-in status")
	}
	data := object(body["data"])
	result, day, points, expiry := number(data["claim_result"]), number(data["day_no"]), number(data["points"]), number(data["expire_at_ms"])
	if nonempty(data["claim_id"]) == "" || result == nil || (*result != 1 && *result != 2) || day == nil || *day < 1 || *day > 7 || *day != math.Trunc(*day) || points == nil || *points < 0 || expiry == nil {
		return nil, errors.New("MiniMax Code returned an invalid check-in claim")
	}
	panel, err = parseCheckinPanel(data["panel"])
	if err != nil {
		return nil, err
	}
	return &CheckinClaim{ClaimResult: int(*result), DayNo: int(*day), Points: *points, ExpireAtMS: *expiry, Panel: *panel}, nil
}
func parseCheckinPanel(value any) (*CheckinPanel, error) {
	invalid := errors.New("MiniMax Code returned an invalid check-in panel")
	body := object(value)
	scene := number(body["scene"])
	days, ok := body["days"].([]any)
	if scene == nil || *scene < 0 || *scene > 4 || *scene != math.Trunc(*scene) || !ok || len(days) != 7 {
		return nil, invalid
	}
	panel := &CheckinPanel{Scene: int(*scene), Days: make([]CheckinDay, 0, 7)}
	seen := map[int]bool{}
	todayCount, claimableCount := 0, 0
	for _, value := range days {
		day := object(value)
		no, points, status := number(day["day_no"]), number(day["points"]), number(day["status"])
		today, validToday := day["is_today"].(bool)
		bonus := number(day["bonus_points"])
		if no == nil || *no < 1 || *no > 7 || *no != math.Trunc(*no) || seen[int(*no)] || points == nil || *points < 0 || status == nil || *status < 1 || *status > 4 || *status != math.Trunc(*status) || !validToday {
			return nil, invalid
		}
		if raw, present := day["bonus_points"]; present && raw != nil && (bonus == nil || *bonus < 0) {
			return nil, invalid
		}
		seen[int(*no)] = true
		if today {
			todayCount++
		}
		if *status == 2 {
			claimableCount++
		}
		panel.Days = append(panel.Days, CheckinDay{DayNo: int(*no), Points: *points, BonusPoints: bonus, Status: int(*status), IsToday: today})
	}
	if todayCount > 1 || claimableCount > 1 {
		return nil, invalid
	}
	return panel, nil
}

func (m *Manager) accountIdentity(ctx context.Context) (string, string, error) {
	token, err := m.AccessToken(ctx)
	if err != nil {
		return "", "", errors.New("MiniMax Code sign-in is unavailable; sign in again")
	}
	body, err := m.accountRequest(ctx, "/v1/api/user/info", http.MethodGet, "", token, "0", false)
	if err != nil {
		return "", "", err
	}
	info := object(field(body, "userInfo"))
	if info == nil {
		info = object(field(body, "user_info"))
	}
	userID := nonempty(info["realUserID"])
	if userID == "" {
		userID = nonempty(info["real_user_id"])
	}
	if userID == "" {
		return "", "", errors.New("MiniMax Code account identity is unavailable")
	}
	return token, userID, nil
}
func (m *Manager) accountRequest(ctx context.Context, path, method, body, token, userID string, public bool) (map[string]any, error) {
	origin := m.AccountBaseURL
	if origin == "" {
		origin = accountOrigin
	}
	endpoint, err := url.Parse(strings.TrimRight(origin, "/") + path)
	if err != nil {
		return nil, errors.New("MiniMax Code account URL is invalid")
	}
	now := time.Now().UnixMilli()
	query := url.Values{"device_platform": {"mcode"}, "biz_id": {"3"}, "app_id": {"3001"}, "version_code": {"22201"}, "unix": {strconv.FormatInt(now, 10)}, "timezone_offset": {"0"}, "sys_language": {"en"}, "lang": {"en"}, "device_id": {"0"}, "os_name": {runtime.GOOS}, "browser_name": {"mcode"}, "user_id": {userID}, "client": {"mcode"}}
	if public {
		query.Set("device_platform", "web")
		query.Set("is_desktop", "1")
		query.Set("desktop_version", "0.5.5")
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), strings.NewReader(body))
	if err != nil {
		return nil, errors.New("MiniMax Code account request is invalid")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MiniMaxCode")
	req.Header.Set("Authorization", "Bearer "+token)
	yyBody := body
	if yyBody == "" {
		yyBody = "{}"
	}
	second := strconv.FormatInt(now/1000, 10)
	req.Header.Set("yy", md5Hex(encodeURIComponent(endpoint.RequestURI())+"_"+yyBody+md5Hex(strconv.FormatInt(now, 10))+"ooui"))
	req.Header.Set("x-timestamp", second)
	req.Header.Set("x-signature", md5Hex(second+"I*7Cf%WZ#S&%1RlZJ&C2"+body))
	return m.readAccountResponse(req)
}
func (m *Manager) readAccountResponse(req *http.Request) (map[string]any, error) {
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	// Do not follow redirects carrying credentials or replay a reward mutation.
	boundedClient := *client
	boundedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := boundedClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, errors.New("MiniMax Code account request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MiniMax Code account request returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, accountResponseLimit+1))
	if err != nil || len(data) > accountResponseLimit {
		return nil, errors.New("MiniMax Code account response could not be read")
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil || body == nil {
		return nil, errors.New("MiniMax Code account response is invalid")
	}
	for _, item := range []struct{ parent, key string }{{"base_resp", "status_code"}, {"statusInfo", "code"}} {
		status := object(body[item.parent])[item.key]
		if status != nil && !isNumber(status, 0) {
			return nil, errors.New("MiniMax Code account request was rejected")
		}
	}
	return body, nil
}
func object(value any) map[string]any { result, _ := value.(map[string]any); return result }
func field(body map[string]any, key string) any {
	if value := body[key]; value != nil {
		return value
	}
	return object(body["data"])[key]
}
func nonempty(value any) string { result, _ := value.(string); return strings.TrimSpace(result) }
func number(value any) *float64 {
	result, ok := value.(float64)
	if !ok || math.IsNaN(result) || math.IsInf(result, 0) {
		return nil
	}
	return &result
}
func isNumber(value any, expected float64) bool {
	result := number(value)
	return result != nil && *result == expected
}
func positive(value *float64) *float64 {
	if value == nil || *value <= 0 {
		return nil
	}
	return value
}
func nonnegative(value *float64) *float64 {
	if value != nil && *value < 0 {
		zero := 0.0
		return &zero
	}
	return value
}
func md5Hex(value string) string { sum := md5.Sum([]byte(value)); return hex.EncodeToString(sum[:]) }
func encodeURIComponent(value string) string {
	// JavaScript encodeURIComponent leaves !'()* unescaped; QueryEscape does not.
	encoded := strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
	for _, pair := range [][2]string{{"%21", "!"}, {"%27", "'"}, {"%28", "("}, {"%29", ")"}, {"%2A", "*"}} {
		encoded = strings.ReplaceAll(encoded, pair[0], pair[1])
	}
	return encoded
}
