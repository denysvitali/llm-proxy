package zcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
)

func TestClaimPlanSendsCurrentZCodeIdentity(t *testing.T) {
	t.Helper()
	const token = "test-zcode-jwt"
	const proof = "fresh-captcha-proof"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/zcode-plan/billing/claim" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		wantHeaders := map[string]string{
			"Authorization":           "Bearer " + token,
			"Content-Type":            "application/json",
			"User-Agent":              "ZCode/" + zcodeAppVersion,
			"X-ZCode-App-Version":     zcodeAppVersion,
			"X-Title":                 "Z Code@electron",
			"HTTP-Referer":            "https://zcode.z.ai",
			"X-Platform":              runtime.GOOS + "-" + zcodeArch(),
			"X-Device-Mid":            deviceMID(token),
			aliyunCaptchaHeader:       proof,
			aliyunCaptchaRegionHeader: aliyunCaptchaRegion,
		}
		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if got := r.Header.Get("X-ZCode-Agent"); got != "" {
			t.Errorf("X-ZCode-Agent = %q, want omitted", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body["plan_id"]; got != "current-plan" {
			t.Fatalf("plan_id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"plan":{"starts_at":123,"ends_at":456}}}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: token}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetCaptchaVerifyParam(proof); err != nil {
		t.Fatal(err)
	}
	outcome, err := manager.ClaimPlan(context.Background(), "current-plan")
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.OK || outcome.PlanID != "current-plan" || outcome.StartsAt != 123 || outcome.EndsAt != 456 {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestClaimPlanClassifiesAndInvalidatesRejectedCaptcha(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":3007,"msg":"captcha invalid"}`))
	}))
	defer upstream.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = upstream.URL
	manager.HTTPClient = upstream.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "test-zcode-jwt"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetCaptchaVerifyParam("rejected-proof"); err != nil {
		t.Fatal(err)
	}
	outcome, err := manager.ClaimPlan(context.Background(), "current-plan")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.OK || outcome.FailureKind != "captcha" || numericCode(outcome.Code) != 3007 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := manager.CaptchaVerifyParam(context.Background()); err == nil {
		t.Fatal("rejected CAPTCHA proof was not invalidated")
	}
}
