package minimaxcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func accountTestManager(t *testing.T, handler http.HandlerFunc) *Manager {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.AccountBaseURL = server.URL
	manager.PlatformBaseURL = server.URL
	if err := manager.Store.Save(&Credentials{AccessToken: "secret-account-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	return manager
}
func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
func testPanel() map[string]any {
	days := make([]any, 7)
	for i := range days {
		status := 1
		if i == 0 {
			status = 2
		}
		days[i] = map[string]any{"day_no": i + 1, "points": 100, "bonus_points": 50, "status": status, "is_today": i == 0}
	}
	return map[string]any{"scene": 1, "days": days}
}
func identityResponse(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"data":{"userInfo":{"realUserID":"personal-user"}}}`))
}

func TestAccountPersonalQuotaAndAttribution(t *testing.T) {
	var requests atomic.Int32
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret-account-token" {
			t.Error("missing bearer authorization")
		}
		if r.URL.Path != "/v1/api/openplatform/coding_plan/remains" {
			millis, err := strconv.ParseInt(r.URL.Query().Get("unix"), 10, 64)
			if err != nil {
				t.Error(err)
			}
			second := strconv.FormatInt(millis/1000, 10)
			body := ""
			if r.Method == http.MethodPost {
				var decoded any
				if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
					t.Error(err)
				}
				encoded, _ := json.Marshal(decoded)
				body = string(encoded)
			}
			yyBody := body
			if yyBody == "" {
				yyBody = "{}"
			}
			if got, want := r.Header.Get("yy"), md5Hex(encodeURIComponent(r.URL.RequestURI())+"_"+yyBody+md5Hex(strconv.FormatInt(millis, 10))+"ooui"); got != want {
				t.Errorf("yy=%q want %q", got, want)
			}
			if got, want := r.Header.Get("x-signature"), md5Hex(second+"I*7Cf%WZ#S&%1RlZJ&C2"+body); got != want {
				t.Errorf("signature=%q want %q", got, want)
			}
			if r.URL.Path != "/v1/api/user/info" && r.URL.Query().Get("user_id") != "personal-user" {
				t.Error("wrong account identity")
			}
			if r.URL.Path == "/matrix/api/v1/commerce/get_membership_info" && body != `{"workspace_id":"personal"}` {
				t.Errorf("membership payload=%s", body)
			}
		}
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/matrix/api/v1/user/get_user_extra_info":
			_, _ = w.Write([]byte(`{"data":{"workspaces":[{"workspace_type":1,"workspace_id":"team","op_group_id":"wrong"},{"workspace_type":0,"workspace_id":"personal","has_token_plan":true,"op_group_id":"correct","token_plan_tier":"Plus","opcredit_balance":0}]}}`))
		case "/matrix/api/v1/commerce/get_membership_info":
			_, _ = w.Write([]byte(`{"data":{"token_plan_expires_at":1770000000000}}`))
		case "/v1/api/openplatform/coding_plan/remains":
			if r.Method != http.MethodGet || r.Header.Get("X-Group-Id") != "correct" {
				t.Error("quota must use personal workspace group")
			}
			_, _ = w.Write([]byte(`{"model_remains":[{"model_name":"M3","current_interval_total_count":100,"current_interval_usage_count":25,"current_weekly_status":3,"current_weekly_remaining_percent":0},{"model_name":"Video","current_interval_total_count":5,"current_interval_usage_count":0}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	usage, err := manager.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 || usage.QuotaState != "available" || usage.HasTokenPlan == nil || !*usage.HasTokenPlan || usage.CreditBalance == nil || *usage.CreditBalance != "0" || usage.Tier != "Plus" {
		t.Fatalf("usage=%+v requests=%d", usage, requests.Load())
	}
	if usage.Quota.FiveHour.RemainingPercent == nil || *usage.Quota.FiveHour.RemainingPercent != 25 || !usage.Quota.Weekly.Unlimited || usage.Quota.Weekly.RemainingPercent != nil || usage.Quota.Video == nil || *usage.Quota.Video.RemainingCount != 0 {
		t.Fatalf("quota=%+v", usage.Quota)
	}
}

func TestAccountMissingAndUnsubscribed(t *testing.T) {
	for _, test := range []struct {
		name, extra, scoped, wantState string
		credit                         *string
	}{
		{name: "null workspaces", extra: `{"data":{"workspaces":null}}`, wantState: "unavailable"},
		{name: "missing values", extra: `{"workspaces":[{"workspace_type":0,"workspace_id":0,"op_group_id":"g"}]}`, scoped: `{}`, wantState: "unavailable"},
		{name: "credits without subscription", extra: `{"workspaces":[{"workspace_type":0,"workspace_id":1,"has_token_plan":false,"op_credit_summary":{"total_remaining_amount":"700"}}]}`, scoped: `{}`, wantState: "not-subscribed", credit: strptr("700")},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/api/user/info":
					identityResponse(w)
				case "/matrix/api/v1/user/get_user_extra_info":
					_, _ = w.Write([]byte(test.extra))
				case "/matrix/api/v1/commerce/get_membership_info":
					_, _ = w.Write([]byte(test.scoped))
				case "/v1/api/openplatform/coding_plan/remains":
					_, _ = w.Write([]byte(`{"model_remains":null}`))
				default:
					t.Errorf("unexpected %s", r.URL.Path)
				}
			})
			usage, err := manager.Account(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if usage.QuotaState != test.wantState || usage.Quota != nil {
				t.Fatalf("usage=%+v", usage)
			}
			if test.credit == nil && usage.CreditBalance != nil || test.credit != nil && (usage.CreditBalance == nil || *usage.CreditBalance != *test.credit) {
				t.Errorf("credit=%v", usage.CreditBalance)
			}
		})
	}
}
func strptr(s string) *string { return &s }

func TestCheckinReadAndExplicitClaim(t *testing.T) {
	var posts atomic.Int32
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			if r.Method != http.MethodGet || r.URL.Query().Get("device_platform") != "web" || r.URL.Query().Get("desktop_version") != "0.5.5" {
				t.Error("incorrect public gateway request")
			}
			writeJSON(t, w, map[string]any{"data": testPanel()})
		case "/minimax-cloud/api/v1/signin/claim":
			posts.Add(1)
			if r.Method != http.MethodPost {
				t.Error("claim must be explicit POST")
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 0 {
				t.Error("claim payload must be {}")
			}
			panel := testPanel()
			panel["days"].([]any)[0].(map[string]any)["status"] = 3
			writeJSON(t, w, map[string]any{"data": map[string]any{"claim_id": "private-claim", "claim_result": 1, "day_no": 1, "points": 100, "expire_at_ms": 1770000000000, "panel": panel}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	panel, err := manager.CheckinStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 || panel.Days[0].Points != 100 || *panel.Days[0].BonusPoints != 50 {
		t.Fatal("status mutated or doubled included bonus")
	}
	claim, err := manager.ClaimCheckin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 || claim.ClaimResult != 1 || claim.Points != 100 || claim.Panel.Days[0].Status != 3 {
		t.Fatalf("claim=%+v posts=%d", claim, posts.Load())
	}
	encoded, _ := json.Marshal(claim)
	if strings.Contains(string(encoded), "private-claim") {
		t.Fatal("claim identifier exposed")
	}
}

func TestCheckinEligibilityAndMalformedPanels(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(map[string]any)
		want  error
	}{
		{"already claimed", func(p map[string]any) { p["days"].([]any)[0].(map[string]any)["status"] = 3 }, ErrCheckinAlreadyClaimed},
		{"null days", func(p map[string]any) { p["days"] = nil }, nil},
		{"duplicate day", func(p map[string]any) { p["days"].([]any)[1].(map[string]any)["day_no"] = 1 }, nil},
		{"missing points", func(p map[string]any) { delete(p["days"].([]any)[0].(map[string]any), "points") }, nil},
		{"multiple claimable", func(p map[string]any) { p["days"].([]any)[1].(map[string]any)["status"] = 2 }, nil},
		{"invalid bonus", func(p map[string]any) { p["days"].([]any)[0].(map[string]any)["bonus_points"] = -1 }, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/api/user/info" {
					identityResponse(w)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("unexpected mutation")
				}
				panel := testPanel()
				test.alter(panel)
				writeJSON(t, w, map[string]any{"data": panel})
			})
			_, err := manager.ClaimCheckin(context.Background())
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCheckinNoRetryAndConcurrentClaims(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var posts atomic.Int32
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			writeJSON(t, w, map[string]any{"data": testPanel()})
		case "/minimax-cloud/api/v1/signin/claim":
			posts.Add(1)
			close(entered)
			<-release
			http.Error(w, "secret-account-token private upstream text", http.StatusUnauthorized)
		}
	})
	done := make(chan error, 1)
	go func() { _, err := manager.ClaimCheckin(context.Background()); done <- err }()
	<-entered
	_, err := manager.ClaimCheckin(context.Background())
	if !errors.Is(err, ErrCheckinInProgress) {
		t.Errorf("concurrent error=%v", err)
	}
	close(release)
	err = <-done
	if err == nil || strings.Contains(err.Error(), "secret-account-token") || posts.Load() != 1 {
		t.Fatalf("error=%v posts=%d", err, posts.Load())
	}
}

func TestAccountFailureSanitizationBoundsAndCancellation(t *testing.T) {
	for _, body := range []string{`{"base_resp":{"status_code":42,"status_msg":"secret-account-token"}}`, `{"statusInfo":{"code":1000048,"message":"secret-account-token"}}`, strings.Repeat("x", accountResponseLimit+1), `null`, `{"secret-account-token":`} {
		manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) })
		_, err := manager.CheckinStatus(context.Background())
		if err == nil || strings.Contains(err.Error(), "secret-account-token") {
			t.Fatalf("error=%v", err)
		}
	}
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := manager.CheckinStatus(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}
func TestAccountDoesNotFollowRedirect(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, err := manager.CheckinStatus(context.Background())
	if err == nil || forwarded.Load() != 0 {
		t.Fatalf("error=%v forwarded=%d", err, forwarded.Load())
	}
}

func TestAccountMembershipFallbackKeepsCreditsWithoutUnscopedQuota(t *testing.T) {
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/matrix/api/v1/user/get_user_extra_info":
			http.Error(w, "unavailable", http.StatusNotFound)
		case "/matrix/api/v1/commerce/get_membership_info":
			_, _ = w.Write([]byte(`{"has_token_plan":true,"op_group_id":"possibly-team","opcredit_balance":"123"}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	})
	usage, err := manager.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.QuotaState != "unavailable" || usage.CreditBalance == nil || *usage.CreditBalance != "123" {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCheckinStopsWhenAccountChanges(t *testing.T) {
	var manager *Manager
	manager = accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			if err := manager.Store.Save(&Credentials{AccessToken: "another-account", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
				t.Error(err)
			}
			writeJSON(t, w, map[string]any{"data": testPanel()})
		default:
			t.Errorf("unexpected mutation: %s", r.URL.Path)
		}
	})
	_, err := manager.ClaimCheckin(context.Background())
	if err == nil || !strings.Contains(err.Error(), "account changed") {
		t.Fatalf("error=%v", err)
	}
}

func TestEncodeURIComponentMatchesPublicGateway(t *testing.T) {
	if got := encodeURIComponent("/path?x=hello world&literal=!'()*~"); got != "%2Fpath%3Fx%3Dhello%20world%26literal%3D!'()*~" {
		t.Errorf("encoding=%s", got)
	}
}

func TestCheckinDoesNotRetryInvalidClaimResponses(t *testing.T) {
	for _, body := range []string{`null`, `{"data":null}`, `{"data":{"claim_id":"id","claim_result":1,"day_no":1,"points":0,"expire_at_ms":0,"panel":null}}`, `{"base_resp":{"status_code":1,"status_msg":"secret-account-token"}}`} {
		var posts atomic.Int32
		manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/api/user/info":
				identityResponse(w)
			case "/minimax-cloud/api/v1/signin/status":
				writeJSON(t, w, map[string]any{"data": testPanel()})
			case "/minimax-cloud/api/v1/signin/claim":
				posts.Add(1)
				_, _ = fmt.Fprint(w, body)
			}
		})
		_, err := manager.ClaimCheckin(context.Background())
		if err == nil || posts.Load() != 1 || strings.Contains(err.Error(), "secret-account-token") {
			t.Fatalf("error=%v posts=%d", err, posts.Load())
		}
	}
}

func TestCheckinHidesReceiptWhenAccountChangesDuringClaim(t *testing.T) {
	var manager *Manager
	manager = accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			writeJSON(t, w, map[string]any{"data": testPanel()})
		case "/minimax-cloud/api/v1/signin/claim":
			if err := manager.Store.Save(&Credentials{AccessToken: "another-account", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
				t.Error(err)
			}
			writeJSON(t, w, map[string]any{"data": map[string]any{"claim_id": "old-account-receipt", "claim_result": 1, "day_no": 1, "points": 100, "expire_at_ms": 1770000000000, "panel": testPanel()}})
		}
	})
	claim, err := manager.ClaimCheckin(context.Background())
	if claim != nil || err == nil || !strings.Contains(err.Error(), "account changed") {
		t.Fatalf("claim=%+v error=%v", claim, err)
	}
}
