package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/backend"
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

// ChatGPT can reject an access token before its JWT expiry (401
// token_expired). Send refreshes once and retries; a refresh that fails is an
// error naming the dashboard sign-in, and upstream is called only once.
func TestSendRefreshesTokenRevokedBeforeExpiry(t *testing.T) {
	accountID := "workspace-revoked"
	fresh := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "n": "fresh"})
	idToken := testJWT(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID}})
	for _, refreshOK := range []bool{true, false} {
		var refreshes, calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/oauth/token":
				refreshes.Add(1)
				if !refreshOK {
					w.WriteHeader(http.StatusUnauthorized)
					writeTestJSON(w, map[string]string{"error": "invalid_grant"})
					return
				}
				writeTestJSON(w, map[string]string{"access_token": fresh, "refresh_token": "next-refresh", "id_token": idToken})
			case "/responses":
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+fresh {
					w.WriteHeader(http.StatusUnauthorized)
					writeTestJSON(w, map[string]any{"error": map[string]string{"code": "token_expired"}})
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"output\":[]}}\n\n"))
			default:
				http.NotFound(w, r)
			}
		}))
		manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
		manager.Issuer = server.URL
		manager.HTTPClient = server.Client()
		// The stored token is unexpired by its own claims but revoked upstream.
		revoked := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "n": "revoked"})
		if err := manager.Store.Save(&Credentials{AccessToken: revoked, RefreshToken: "old-refresh", AccountID: accountID}); err != nil {
			t.Fatal(err)
		}
		client := New(server.URL, manager)
		client.HTTP = server.Client()
		resp, err := client.Send(context.Background(), &backend.Request{RawBody: []byte(`{"model":"gpt","input":"hi"}`), Header: http.Header{}})
		if refreshOK {
			if err != nil || resp.Status != http.StatusOK || refreshes.Load() != 1 || calls.Load() != 2 {
				t.Fatalf("Send = %#v, %v; refreshes=%d calls=%d", resp, err, refreshes.Load(), calls.Load())
			}
			saved, _ := manager.Store.Load()
			if saved.AccessToken != fresh || saved.RefreshToken != "next-refresh" {
				t.Fatalf("refreshed session not saved: %#v", saved)
			}
			// A request still holding the revoked token reuses the saved
			// session instead of refreshing again.
			if again, err := manager.ForceRefresh(context.Background(), revoked); err != nil || again.AccessToken != fresh || refreshes.Load() != 1 {
				t.Fatalf("ForceRefresh(stale) = %#v, %v; refreshes=%d", again, err, refreshes.Load())
			}
		} else if err == nil || !strings.Contains(err.Error(), "sign in again") || calls.Load() != 1 {
			t.Fatalf("failed refresh: resp=%#v err=%v calls=%d", resp, err, calls.Load())
		}
		server.Close()
	}
}

// Two proxy replicas share one session file. ChatGPT refresh tokens are
// single-use, so concurrent expiry must redeem the refresh token exactly once
// across processes, not once per replica.
func TestReplicasRedeemRefreshTokenOnce(t *testing.T) {
	accountID := "workspace-replicas"
	accessToken := testJWT(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != "old-refresh" || refreshes.Add(1) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			writeTestJSON(w, map[string]any{"error": map[string]string{"code": "refresh_token_reused"}})
			return
		}
		time.Sleep(50 * time.Millisecond)
		writeTestJSON(w, map[string]string{"access_token": accessToken, "refresh_token": "new-refresh"})
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "auth.json")
	replicas := []*Manager{NewManager(path), NewManager(path), NewManager(path)}
	for _, m := range replicas {
		m.Issuer, m.HTTPClient = server.URL, server.Client()
	}
	if err := replicas[0].Store.Save(&Credentials{AccessToken: "old", RefreshToken: "old-refresh", AccountID: accountID, ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, len(replicas))
	for _, m := range replicas {
		go func() {
			credentials, err := m.Credentials(context.Background())
			if err == nil && credentials.AccessToken != accessToken {
				err = errors.New("stale access token " + credentials.AccessToken)
			}
			errs <- err
		}()
	}
	for range replicas {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want 1", got)
	}
}

func TestRefreshErrorNamesOAuthCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeTestJSON(w, map[string]any{"error": map[string]string{"code": "refresh_token_reused", "message": "secret-ish description"}})
	}))
	defer server.Close()
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.Issuer, manager.HTTPClient = server.URL, server.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "old", RefreshToken: "r", AccountID: "a", ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Credentials(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 401 refresh_token_reused") || strings.Contains(err.Error(), "description") {
		t.Fatalf("Credentials error = %v", err)
	}
	if !backend.IsTerminal(err) {
		t.Fatalf("rejected refresh is retryable: %v", err)
	}
}

// A token endpoint that is down (5xx) is transient: the request stays
// retryable, unlike a rejected refresh token.
func TestRefreshServerErrorStaysRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.Issuer, manager.HTTPClient = server.URL, server.Client()
	if err := manager.Store.Save(&Credentials{AccessToken: "old", RefreshToken: "r", AccountID: "a", ExpiresAt: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Credentials(context.Background()); err == nil || backend.IsTerminal(err) {
		t.Fatalf("Credentials error = %v, want retryable", err)
	}
}
