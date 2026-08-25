package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreSaveLoadRefreshMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	store := &Store{Path: path}
	token := &Token{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}
	if err := store.Save(token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0600); got != want {
		t.Errorf("mode = %o, want %o", got, want)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "access" || loaded.RefreshToken != "refresh" || loaded.Issuer != Issuer || loaded.ClientID != ClientID {
		t.Errorf("loaded token = %+v", loaded)
	}
	if loaded.ExpiresAt <= float64(time.Now().Unix()) {
		t.Errorf("ExpiresAt = %v, should be in the future", loaded.ExpiresAt)
	}
}

func TestImportGrokChoosesLatestSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	data := map[string]any{
		"old": map[string]any{"key": "old", "expires_at": "2026-01-01T00:00:00Z"},
		"new": map[string]any{"key": "new", "refresh_token": "refresh", "expires_at": "2027-01-01T00:00:00Z"},
	}
	b, _ := json.Marshal(data)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	token, err := ImportGrok(path)
	if err != nil {
		t.Fatalf("ImportGrok: %v", err)
	}
	if token.AccessToken != "new" || token.RefreshToken != "refresh" || token.Source != path {
		t.Errorf("token = %+v", token)
	}
}

func TestManagerWithoutSessionExplainsWebLogin(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	_, err := m.AccessToken(t.Context())
	if err == nil || !strings.Contains(err.Error(), "dashboard") {
		t.Fatalf("AccessToken error = %v, want dashboard sign-in guidance", err)
	}
}
