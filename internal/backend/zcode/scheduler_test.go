package zcode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func claimTestTime() time.Time { return time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC) }
func claimTestToken(subject string) string {
	payload, _ := json.Marshal(map[string]string{"sub": subject})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
func claimTestOffer(_ time.Time) PreviewPlan {
	return PreviewPlan{PlanID: "discovered-offer", Name: "Daily Free GLM-5.3-Flash", EntitlementDetails: []PreviewEntitlement{{EntitlementID: "glm-5.3-flash", GrantUnits: 300_000_000, UnitType: "tokens", Period: "daily"}}}
}
func TestDailyFreeOffer(t *testing.T) {
	now := claimTestTime()
	for _, tc := range []struct {
		name   string
		change func(*PreviewPlan)
		when   time.Time
		want   bool
	}{
		{"opens at eleven", func(*PreviewPlan) {}, now, true},
		{"before opening", func(*PreviewPlan) {}, now.Add(-time.Second), false},
		{"later same day", func(*PreviewPlan) {}, now.Add(12 * time.Hour), true},
		{"next day opens independently", func(*PreviewPlan) {}, now.Add(24 * time.Hour), true},
		{"local clock before UTC opening", func(*PreviewPlan) {}, now.Add(-time.Second).In(time.FixedZone("east", 8*3600)), false},
		{"missing metadata", func(p *PreviewPlan) { p.Name = "" }, now, false},
		{"not explicit free", func(p *PreviewPlan) { p.Name = "Daily GLM-5.3-Flash" }, now, false},
		{"not free", func(p *PreviewPlan) { p.Name = "Not Free GLM-5.3-Flash" }, now, false},
		{"paid", func(p *PreviewPlan) { p.Description = "paid subscription" }, now, false},
		{"wrong model", func(p *PreviewPlan) { p.EntitlementDetails[0].EntitlementID = "glm-5" }, now, false},
		{"wrong grant", func(p *PreviewPlan) { p.EntitlementDetails[0].GrantUnits = 1 }, now, false},
		{"wrong unit", func(p *PreviewPlan) { p.EntitlementDetails[0].UnitType = "credits" }, now, false},
		{"wrong period", func(p *PreviewPlan) { p.EntitlementDetails[0].Period = "monthly" }, now, false},
		{"no usage timestamps", func(*PreviewPlan) {}, now, true},
		{"usage timestamps do not gate claims", func(p *PreviewPlan) {
			p.StartsAt = now.Add(4 * time.Hour).Unix()
			p.EndsAt = now.Add(14 * time.Hour).Unix()
		}, now, true},
		{"display model and units", func(p *PreviewPlan) {
			p.EntitlementDetails[0].EntitlementID = "opaque-model-id"
			p.EntitlementDetails[0].ShowName = "GLM-5.3 Flash"
			p.EntitlementDetails[0].UnitType = "units"
		}, now, true},
		{"model separators", func(p *PreviewPlan) { p.EntitlementDetails[0].EntitlementID = " GLM_5.3_Flash " }, now, true},
		{"other flash variant", func(p *PreviewPlan) { p.EntitlementDetails[0].EntitlementID = "glm-5.3-flash-plus" }, now, false},
		{"wrong version punctuation", func(p *PreviewPlan) { p.EntitlementDetails[0].EntitlementID = "glm-53-flash" }, now, false},
		{"missing daily period", func(p *PreviewPlan) { p.EntitlementDetails[0].Period = "" }, now, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := claimTestOffer(now)
			tc.change(&p)
			if got := dailyFreeOffer(p, tc.when); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

// Fixtures below exercise conservative selection, not an observed live offer.
// The reference account currently returns an empty preview.
func TestDailyFreeOfferKeepsEntitlementMetadataTogether(t *testing.T) {
	now := claimTestTime()
	valid := claimTestOffer(now).EntitlementDetails[0]
	wrongModel := valid
	wrongModel.EntitlementID = "another-model"
	wrongGrant := valid
	wrongGrant.GrantUnits = 1
	wrongUnit := valid
	wrongUnit.UnitType = "credits"
	wrongPeriod := valid
	wrongPeriod.Period = "monthly"
	for _, tc := range []struct {
		name   string
		grants []PreviewEntitlement
		want   bool
	}{
		{"valid with unrelated grant", []PreviewEntitlement{valid, wrongModel}, true},
		{"model and grant split", []PreviewEntitlement{wrongGrant, wrongModel}, false},
		{"unit and period split", []PreviewEntitlement{wrongUnit, wrongPeriod}, false},
		{"no structured grant", nil, false},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", tc.name, reverse), func(t *testing.T) {
				grants := append([]PreviewEntitlement(nil), tc.grants...)
				if reverse {
					for i, j := 0, len(grants)-1; i < j; i, j = i+1, j-1 {
						grants[i], grants[j] = grants[j], grants[i]
					}
				}
				p := claimTestOffer(now)
				m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						t.Errorf("unexpected mutation: %s", r.Method)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"plans": []any{map[string]any{
						"plan_id": p.PlanID, "name": p.Name, "starts_at": p.StartsAt, "ends_at": p.EndsAt, "entitlements": grants,
					}}}})
				})
				plans, err := m.PreviewPlans(context.Background())
				if err != nil || len(plans) != 1 {
					t.Fatalf("preview: %v, plans=%d", err, len(plans))
				}
				// Even a legacy flattened summary that looks eligible must not
				// allow unrelated structured grants to be combined.
				plans[0].GrantUnits, plans[0].UnitType, plans[0].Period = valid.GrantUnits, valid.UnitType, valid.Period
				if got := dailyFreeOffer(plans[0], now); got != tc.want {
					t.Fatalf("eligible=%v, want %v", got, tc.want)
				}
			})
		}
	}
}

func autoClaimTestManager(t *testing.T, handler http.HandlerFunc) *Manager {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	m := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	m.Issuer, m.HTTPClient = upstream.URL, upstream.Client()
	if err := m.Store.Save(&Credentials{AccessToken: claimTestToken("account-a")}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetCaptchaVerifyParam("test-proof"); err != nil {
		t.Fatal(err)
	}
	return m
}
func writeClaimPreview(w http.ResponseWriter, now time.Time) {
	p := claimTestOffer(now)
	// Shape preserved by the official client: no usage timestamps, an opaque
	// entitlement ID, a model display name, and the observed "units" spelling.
	p.EntitlementDetails[0].EntitlementID = "opaque-entitlement-id"
	p.EntitlementDetails[0].ShowName = "GLM-5.3 Flash"
	p.EntitlementDetails[0].UnitType = "units"
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"plans": []any{map[string]any{
		"plan_id": p.PlanID, "name": p.Name,
		"entitlements": p.EntitlementDetails,
	}}}})
}
func TestAutoClaimDurableSuppression(t *testing.T) {
	for _, response := range []string{`{"code":0}`, `{"code":1003}`, `{"code":3012,"msg":"private upstream detail"}`, `not-json`} {
		t.Run(response, func(t *testing.T) {
			var posts, previews atomic.Int32
			m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					previews.Add(1)
					writeClaimPreview(w, claimTestTime())
					return
				}
				posts.Add(1)
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["plan_id"] != "discovered-offer" {
					t.Error("did not use preview plan ID")
				}
				_, _ = io.WriteString(w, response)
			})
			err := m.autoClaim(context.Background(), claimTestTime())
			if response == `{"code":0}` && err != nil {
				t.Fatal(err)
			}
			if response != `{"code":0}` && err == nil {
				t.Fatal("expected rejection or uncertain error")
			}
			// A new manager models restart; no new CAPTCHA or preview should be needed.
			restarted := NewManager(m.Store.Path)
			restarted.Issuer, restarted.HTTPClient = m.Issuer, m.HTTPClient
			if err := restarted.autoClaim(context.Background(), claimTestTime()); !errors.Is(err, errAutoClaimAttempted) {
				t.Fatalf("repeat: %v", err)
			}
			if posts.Load() != 1 || previews.Load() != 1 {
				t.Fatalf("posts %d previews %d", posts.Load(), previews.Load())
			}
			path := autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime())
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if stat.Mode().Perm() != 0600 {
				t.Fatalf("permissions %o", stat.Mode().Perm())
			}
		})
	}
}
func TestAutoClaimPreparationFailuresDoNotMark(t *testing.T) {
	var posts atomic.Int32
	previewError := true
	m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if previewError {
				_, _ = io.WriteString(w, `{"code":3001,"msg":"sensitive"}`)
			} else {
				writeClaimPreview(w, claimTestTime())
			}
			return
		}
		posts.Add(1)
		_, _ = io.WriteString(w, `{"code":0}`)
	})
	if err := m.autoClaim(context.Background(), claimTestTime()); err == nil {
		t.Fatal("expected preview failure")
	}
	previewError = false
	m.InvalidateCaptcha("test-proof")
	if err := m.autoClaim(context.Background(), claimTestTime()); err == nil {
		t.Fatal("expected CAPTCHA failure")
	}
	if _, err := os.Stat(autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("premature marker: %v", err)
	}
	_ = m.SetCaptchaVerifyParam("fresh-proof")
	if err := m.autoClaim(context.Background(), claimTestTime()); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("posts = %d", posts.Load())
	}
}
func TestAutoClaimAccountChanges(t *testing.T) {
	var m *Manager
	var posts atomic.Int32
	m = autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = m.Store.Save(&Credentials{AccessToken: claimTestToken("account-b")})
			writeClaimPreview(w, claimTestTime())
			return
		}
		posts.Add(1)
		_, _ = io.WriteString(w, `{"code":0}`)
	})
	if err := m.autoClaim(context.Background(), claimTestTime()); err == nil {
		t.Fatal("expected account change failure")
	}
	if posts.Load() != 0 {
		t.Fatal("claimed for changed account")
	}
	if _, err := os.Stat(autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old account marked")
	}
	if autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime()) == autoClaimMarkerPath(m.Store.Path, "account-b", claimTestTime()) {
		t.Fatal("accounts share marker")
	}
	if autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime()) == autoClaimMarkerPath(m.Store.Path, "account-a", claimTestTime().Add(24*time.Hour)) {
		t.Fatal("days share marker")
	}
}
func TestAutoClaimMarkerExclusive(t *testing.T) {
	path := autoClaimMarkerPath(filepath.Join(t.TempDir(), "auth"), "user", claimTestTime())
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := createAutoClaimMarker(path)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, errAutoClaimAttempted) {
				t.Errorf("marker: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successes = %d", successes.Load())
	}
}
func TestAutoClaimSchedulerRetriesPreviewAndCancelsHTTP(t *testing.T) {
	var requests atomic.Int32
	blocked := make(chan struct{})
	m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"code":3001}`)
			return
		}
		close(blocked)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := logrus.New()
	log.SetOutput(io.Discard)
	done := make(chan struct{})
	go func() { defer close(done); m.runAutoClaim(ctx, log, time.Millisecond, claimTestTime) }()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not retry on tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop in-flight HTTP")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestAutoClaimNoEligibleOrAmbiguousPreview(t *testing.T) {
	for _, body := range []string{
		`{"code":0,"data":{"plans":null}}`,
		`{"code":0,"data":{"plans":[{"plan_id":"unverified","name":"Free GLM-5.3-Flash"}]}}`,
	} {
		var posts atomic.Int32
		m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts.Add(1)
			}
			_, _ = io.WriteString(w, body)
		})
		if err := m.autoClaim(context.Background(), claimTestTime()); !errors.Is(err, errAutoClaimUnavailable) {
			t.Fatalf("unexpected result: %v", err)
		}
		if posts.Load() != 0 {
			t.Fatal("claimed unverified offer")
		}
	}
	var posts atomic.Int32
	m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			return
		}
		recorder := httptest.NewRecorder()
		writeClaimPreview(recorder, claimTestTime())
		var envelope struct {
			Code int `json:"code"`
			Data struct {
				Plans []json.RawMessage `json:"plans"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
		envelope.Data.Plans = append(envelope.Data.Plans, envelope.Data.Plans[0])
		_ = json.NewEncoder(w).Encode(envelope)
	})
	if err := m.autoClaim(context.Background(), claimTestTime()); !errors.Is(err, errAutoClaimUnavailable) {
		t.Fatalf("ambiguous result: %v", err)
	}
	if posts.Load() != 0 {
		t.Fatal("claimed ambiguous offer")
	}
}

type claimLogHook struct{ entries chan struct{} }

func (h claimLogHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h claimLogHook) Fire(*logrus.Entry) error {
	select {
	case h.entries <- struct{}{}:
	default:
	}
	return nil
}

func TestAutoClaimSchedulerLifecycleAndSafeLogs(t *testing.T) {
	var requests atomic.Int32
	m := autoClaimTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"code":3001,"msg":"sensitive-upstream-detail"}`)
	})
	var logs bytes.Buffer
	log := logrus.New()
	log.SetOutput(&logs)
	logged := make(chan struct{}, 1)
	log.AddHook(claimLogHook{logged})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		defer close(done)
		m.runAutoClaim(ctx, log, time.Hour, func() time.Time { calls.Add(1); return claimTestTime() })
	}()
	select {
	case <-logged:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not perform startup check")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	if calls.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("startup calls %d requests %d", calls.Load(), requests.Load())
	}
	if !strings.Contains(logs.String(), "failed") || strings.Contains(logs.String(), "sensitive") || strings.Contains(logs.String(), "account-a") || strings.Contains(logs.String(), "test-proof") {
		t.Fatalf("unsafe or missing log: %s", logs.String())
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	m.runAutoClaim(canceled, log, time.Millisecond, claimTestTime)
	if requests.Load() != 1 {
		t.Fatal("canceled scheduler sent request")
	}
	_ = os.Remove(m.Store.Path)
	if err := m.autoClaim(context.Background(), claimTestTime()); !errors.Is(err, errAutoClaimUnavailable) {
		t.Fatalf("signed out: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatal("signed-out scheduler sent request")
	}
}
