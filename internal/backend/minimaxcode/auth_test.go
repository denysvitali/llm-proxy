package minimaxcode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginAndRefresh(t *testing.T) {
	var challenge string
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected OAuth request %s %v", r.Method, r.Header)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_id") != clientID {
			t.Errorf("client_id = %q", r.Form.Get("client_id"))
		}
		switch {
		case r.URL.Path == "/oauth2/device/code":
			if r.Form.Get("scope") != scope || r.Form.Get("audience") != audience || r.Form.Get("code_challenge_method") != "S256" {
				t.Errorf("device form = %v", r.Form)
			}
			challenge = r.Form.Get("code_challenge")
			_, _ = w.Write([]byte(`{"device_code":"device-1","user_code":"ABCD","verification_uri":"https://account.minimax.io/device","expires_in":300,"interval":1}`))
		case r.URL.Path == "/oauth2/token" && r.Form.Get("grant_type") == deviceGrant:
			if r.Form.Get("device_code") != "device-1" {
				t.Errorf("device_code = %q", r.Form.Get("device_code"))
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				t.Error("PKCE verifier does not match challenge")
			}
			_, _ = w.Write([]byte(`{"access_token":"first","refresh_token":"refresh-1","token_type":"Bearer","expires_in":3600}`))
		case r.URL.Path == "/oauth2/token" && r.Form.Get("grant_type") == "refresh_token":
			calls++
			if r.Form.Get("refresh_token") != "refresh-1" || r.Form.Get("audience") != audience {
				t.Errorf("refresh form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"second","token_type":"Bearer","expires_in":3600}`))
		default:
			t.Errorf("unexpected OAuth path and form: %s %v", r.URL.Path, r.Form)
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.OAuthBaseURL = upstream.URL
	var messages []string
	if err := manager.LoginDevice(context.Background(), func(message string) { messages = append(messages, message) }); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || !strings.Contains(messages[0], "https://account.minimax.io/device") || messages[1] != "Code: ABCD" {
		t.Errorf("login messages = %v", messages)
	}
	if !manager.HasSession() {
		t.Fatal("session was not saved")
	}
	creds, err := manager.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	creds.ExpiresAt = time.Now().Unix()
	if err := manager.Store.Save(creds); err != nil {
		t.Fatal(err)
	}
	token, err := manager.AccessToken(context.Background())
	if err != nil || token != "second" || calls != 1 {
		t.Errorf("refreshed token = %q, calls = %d, err = %v", token, calls, err)
	}
}

func TestTokenValidation(t *testing.T) {
	for _, body := range []string{
		`{"access_token":"x","token_type":"Bearer","expires_in":0}`,
		`{"access_token":"x","token_type":"Basic","expires_in":3600}`,
	} {
		if _, err := credentialsFromToken([]byte(body), ""); err == nil {
			t.Errorf("accepted bad token %s", body)
		}
	}
	data, _ := json.Marshal(url.Values{"x": {"y"}})
	if _, err := credentialsFromToken(data, ""); err == nil {
		t.Fatal("accepted unrelated JSON")
	}
}

func TestDeviceLoginWithUserCodePolling(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/oauth2/device/code":
			_, _ = fmt.Fprintf(w, `{"user_code":"EFGH","verification_url":"https://account.minimax.io/device","expired_in":%d,"interval":1000}`, time.Now().Add(time.Minute).UnixMilli())
		case "/oauth2/token":
			if r.Form.Get("user_code") != "EFGH" || r.Form.Get("device_code") != "" {
				t.Errorf("legacy poll form = %v", r.Form)
			}
			_, _ = w.Write([]byte(`{"access_token":"legacy","refresh_token":"refresh","token_type":"Bearer","expires_in":3600}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.OAuthBaseURL = upstream.URL
	if err := manager.LoginDevice(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if token, err := manager.AccessToken(context.Background()); err != nil || token != "legacy" {
		t.Errorf("token = %q, err = %v", token, err)
	}
}
