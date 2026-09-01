package zcode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreSaveLoadUsesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "zcode-auth.json")
	store := &Store{Path: path}
	credentials := &Credentials{AccessToken: "header.payload.signature"}
	if err := store.Save(credentials); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("credential mode = %o, want 600", got)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != credentials.AccessToken {
		t.Errorf("Load() = %#v, want %#v", loaded, credentials)
	}
}

func TestLoginDevicePollsAndStoresOnlyZCodeToken(t *testing.T) {
	const sessionToken = "eyJhbGciOiJub25lIn0.eyJleHAiOjQwMDAwMDAwMDB9.signature"
	var pollCount atomic.Int32
	var initAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "" {
			t.Error("OAuth request has no Authorization header")
		} else if r.URL.Path == "/api/v1/oauth/cli/init" {
			initAuthorization = got
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode init body: %v", err)
			}
			if body["provider"] != "zai" {
				t.Errorf("provider = %q, want zai", body["provider"])
			}
			_, _ = fmt.Fprintf(w, `{"code":0,"msg":"","data":{"flow_id":"flow-1","authorize_url":"https://zcode.z.ai/oauth/authorize","expires_at":%d,"poll_interval_sec":0.001}}`, time.Now().Add(time.Minute).Unix())
			return
		}
		if r.URL.Path != "/api/v1/oauth/cli/poll/flow-1" {
			t.Errorf("OAuth path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != initAuthorization {
			t.Errorf("poll Authorization = %q, want init token", r.Header.Get("Authorization"))
		}
		if pollCount.Add(1) == 1 {
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"status":"pending"}}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"data":{"status":"ready","token":%q,"zai":{"access_token":"must-not-be-stored"}}}`, sessionToken)
	}))
	defer server.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = server.URL
	var messages []string
	if err := manager.LoginDevice(context.Background(), func(message string) { messages = append(messages, message) }); err != nil {
		t.Fatalf("LoginDevice() error = %v", err)
	}
	if len(messages) < 1 || !strings.HasPrefix(messages[0], "Open: https://") {
		t.Errorf("login messages = %#v, want browser URL", messages)
	}
	loaded, err := manager.Store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != sessionToken {
		t.Errorf("stored credentials = %#v, want only ZCode token", loaded)
	}
	contents, err := os.ReadFile(manager.Store.Path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(contents), "must-not-be-stored") {
		t.Error("stored credentials contain the Z.ai access token")
	}
}

func TestLoginDeviceRejectsUnsafeAuthorizeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"flow_id":"flow-1","authorize_url":"http://attacker.example/authorize"}}`)
	}))
	defer server.Close()
	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	manager.Issuer = server.URL
	if err := manager.LoginDevice(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "unsafe authorization URL") {
		t.Fatalf("LoginDevice() error = %v, want unsafe URL error", err)
	}
}

func TestAccessTokenRequiresUnexpiredSession(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "zcode-auth.json"))
	if _, err := manager.AccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("AccessToken() error = %v, want not-signed-in error", err)
	}
	if err := manager.Store.Save(&Credentials{AccessToken: jwtWithExpiration(time.Now().Add(-time.Hour))}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := manager.AccessToken(context.Background()); err == nil || !strings.Contains(err.Error(), "session expired") {
		t.Fatalf("expired AccessToken() error = %v, want expired error", err)
	}
}

func TestCaptchaVerifyParamIsShortLivedAndShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zcode-auth.json")
	manager := NewManager(path)
	if _, err := manager.CaptchaVerifyParam(context.Background()); err == nil || !strings.Contains(err.Error(), "CAPTCHA verification is required") {
		t.Fatalf("CaptchaVerifyParam() error = %v, want missing-verification error", err)
	}
	if err := manager.SetCaptchaVerifyParam(" fresh-param "); err != nil {
		t.Fatalf("SetCaptchaVerifyParam() error = %v", err)
	}
	got, err := manager.CaptchaVerifyParam(context.Background())
	if err != nil {
		t.Fatalf("CaptchaVerifyParam() error = %v", err)
	}
	if got != "fresh-param" {
		t.Errorf("CaptchaVerifyParam() = %q, want fresh-param", got)
	}
	otherReplica := NewManager(path)
	got, err = otherReplica.CaptchaVerifyParam(context.Background())
	if err != nil {
		t.Fatalf("other replica CaptchaVerifyParam() error = %v", err)
	}
	if got != "fresh-param" {
		t.Errorf("other replica CaptchaVerifyParam() = %q, want fresh-param", got)
	}
	stale, err := json.Marshal(captchaRecord{VerifyParam: "stale-param", IssuedAt: time.Now().Add(-captchaTTL)})
	if err != nil {
		t.Fatalf("marshal stale CAPTCHA record: %v", err)
	}
	if err := writePrivateFile(path+captchaFileSuffix, append(stale, '\n')); err != nil {
		t.Fatalf("write stale CAPTCHA record: %v", err)
	}
	if _, err := manager.CaptchaVerifyParam(context.Background()); err == nil || !strings.Contains(err.Error(), "CAPTCHA verification is required") {
		t.Fatalf("expired CaptchaVerifyParam() error = %v, want missing-verification error", err)
	}
	if err := manager.SetCaptchaVerifyParam(" "); err == nil {
		t.Error("SetCaptchaVerifyParam(whitespace) succeeded, want error")
	}
	manager.InvalidateCaptcha("fresh-param")
	if _, err := manager.CaptchaVerifyParam(context.Background()); err == nil || !strings.Contains(err.Error(), "CAPTCHA verification is required") {
		t.Fatalf("invalidated CaptchaVerifyParam() error = %v, want missing-verification error", err)
	}
	if _, err := os.Stat(path + captchaFileSuffix); !os.IsNotExist(err) {
		t.Fatalf("invalidated CAPTCHA file still exists at %s", path+captchaFileSuffix)
	}
	if _, err := os.Stat(manager.Store.Path); !os.IsNotExist(err) {
		t.Fatalf("CAPTCHA value was persisted in the credential file at %s", manager.Store.Path)
	}
}

func jwtWithExpiration(expiration time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiration.Unix())))
	return header + "." + payload + ".signature"
}
