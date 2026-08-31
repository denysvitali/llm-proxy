package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestStoreSavesCredentialsPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	store := &Store{Path: path}
	if err := store.Save(&Credentials{AccessToken: "secret", AccountID: "account"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	loaded, err := store.Load()
	if err != nil || loaded.AccessToken != "secret" || loaded.AccountID != "account" {
		t.Fatalf("Load = %#v, %v", loaded, err)
	}
}

func TestLoginDevicePollsExchangesAndSaves(t *testing.T) {
	accountID := "workspace-123"
	idToken := testJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID}})
	accessToken := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeTestJSON(w, map[string]any{"device_auth_id": "device-1", "user_code": "ABCD-EFGH", "interval": "0"})
		case "/api/accounts/deviceauth/token":
			if polls.Add(1) == 1 {
				http.Error(w, "pending", http.StatusNotFound)
				return
			}
			writeTestJSON(w, map[string]string{"authorization_code": "code", "code_challenge": "challenge", "code_verifier": "verifier"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code_verifier") != "verifier" || r.Form.Get("grant_type") != "authorization_code" {
				t.Errorf("token form = %v, error = %v", r.Form, err)
			}
			writeTestJSON(w, map[string]string{"access_token": accessToken, "refresh_token": "refresh", "id_token": idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "codex-auth.json"))
	manager.Issuer = server.URL
	manager.HTTPClient = server.Client()
	var messages []string
	if err := manager.LoginDevice(t.Context(), func(message string) { messages = append(messages, message) }); err != nil {
		t.Fatalf("LoginDevice: %v", err)
	}
	if polls.Load() != 2 {
		t.Fatalf("polls = %d, want 2", polls.Load())
	}
	if strings.Join(messages, "\n") == "" || !strings.Contains(strings.Join(messages, "\n"), "ABCD-EFGH") {
		t.Fatalf("messages = %#v", messages)
	}
	credentials, err := manager.Credentials(t.Context())
	if err != nil || credentials.AccountID != accountID || credentials.AccessToken != accessToken {
		t.Fatalf("Credentials = %#v, %v", credentials, err)
	}
}

func TestCredentialsRefreshesExpiredToken(t *testing.T) {
	accountID := "workspace-refresh"
	accessToken := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	idToken := testJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "refresh_token" || body["refresh_token"] != "old-refresh" {
			t.Errorf("refresh body = %#v", body)
		}
		writeTestJSON(w, map[string]string{"access_token": accessToken, "refresh_token": "new-refresh", "id_token": idToken})
	}))
	defer server.Close()

	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.Issuer = server.URL
	manager.HTTPClient = server.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "old", RefreshToken: "old-refresh", AccountID: accountID, ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	credentials, err := manager.Credentials(context.Background())
	if err != nil || credentials.AccessToken != accessToken || credentials.RefreshToken != "new-refresh" {
		t.Fatalf("Credentials = %#v, %v", credentials, err)
	}
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
