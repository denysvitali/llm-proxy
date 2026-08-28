package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserLoginAndRefresh(t *testing.T) {
	var refreshCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+r.Host {
			t.Errorf("origin = %q", r.Header.Get("Origin"))
		}
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch r.URL.Path {
		case "/v2/plugin/auth/state":
			if r.URL.Query().Get("platform") != "CLI" {
				t.Errorf("platform = %q", r.URL.Query().Get("platform"))
			}
			data = map[string]any{"state": "state-1", "authUrl": "https://login.example/authorize"}
		case "/v2/plugin/auth/token":
			data = map[string]any{"accessToken": "first-token", "refreshToken": "refresh-token", "expiresIn": 1, "domain": "public"}
		case "/v2/plugin/login/account":
			if r.Header.Get("Authorization") != "Bearer first-token" {
				t.Errorf("account authorization = %q", r.Header.Get("Authorization"))
			}
			data = map[string]any{"uid": "user-1", "enterpriseId": "enterprise-1", "nickname": "Buddy"}
		case "/v2/plugin/auth/token/refresh":
			refreshCalls++
			if r.Header.Get("X-Refresh-Token") != "refresh-token" {
				t.Errorf("refresh token header = %q", r.Header.Get("X-Refresh-Token"))
			}
			data = map[string]any{"accessToken": "refreshed-token", "refreshToken": "next-refresh", "expiresIn": 3600}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	m := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	m.BaseURL = server.URL
	state, url, err := m.StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state != "state-1" || url != "https://login.example/authorize" {
		t.Fatalf("StartLogin = %q, %q", state, url)
	}
	done, err := m.PollLogin(context.Background(), state)
	if err != nil || !done {
		t.Fatalf("PollLogin = %v, %v", done, err)
	}
	credentials, err := m.Credentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if credentials.AccessToken != "refreshed-token" || credentials.UserID != "user-1" || refreshCalls != 1 {
		t.Fatalf("Credentials = %#v, refresh calls=%d", credentials, refreshCalls)
	}
}

func TestCredentialsDoesNotRefreshFreshToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	m := NewManager(path)
	if err := m.save(storedSession{Auth: Credentials{AccessToken: "fresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}, Account: account{UID: "u"}}); err != nil {
		t.Fatal(err)
	}
	c, err := m.Credentials(context.Background())
	if err != nil || c.AccessToken != "fresh" || c.UserID != "u" {
		t.Fatalf("Credentials = %#v, %v", c, err)
	}
}
