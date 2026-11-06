package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKeyStateTransitions(t *testing.T) {
	var k Key
	if got := k.state(); got != keyEnabled {
		t.Fatalf("new key state = %v, want enabled", got)
	}
	for _, step := range []keyState{keyDisabled, keyDisabled, keyEnabled, keyEnabled} {
		k.setState(step)
		if got := k.state(); got != step {
			t.Fatalf("after setState(%v) state = %v", step, got)
		}
		if k.Disabled != (step == keyDisabled) {
			t.Fatalf("Disabled = %v for state %v", k.Disabled, step)
		}
	}
	if keyEnabled.String() != "enabled" || keyDisabled.String() != "disabled" {
		t.Fatalf("unexpected String() values")
	}
}

func TestReloadIfChangedOutcomes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.reloadIfChanged(); got != reloadUnreadable {
		t.Fatalf("missing file outcome = %v, want unreadable", got)
	}
	if err := s.CreateUser("alice"); err != nil {
		t.Fatal(err)
	}
	if got := s.reloadIfChanged(); got != reloadUnchanged {
		t.Fatalf("own save outcome = %v, want unchanged", got)
	}
	before := s.loaded
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	bump(t, path, 1)
	if got := s.reloadIfChanged(); got != reloadMalformed {
		t.Fatalf("malformed outcome = %v, want malformed", got)
	}
	if s.loaded != before || len(s.Users()) != 1 {
		t.Fatalf("malformed reload changed state: %+v users=%v", s.loaded, s.Users())
	}
	if err := os.WriteFile(path, []byte(`[{"name":"bob","keys":[]},{"name":"carol","keys":[]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	bump(t, path, 2)
	if got := s.reloadIfChanged(); got != reloadApplied {
		t.Fatalf("valid change outcome = %v, want applied", got)
	}
	if users := s.Users(); len(users) != 2 || users[0] != "bob" {
		t.Fatalf("users after reload = %v", users)
	}
	if got := s.reloadIfChanged(); got != reloadUnchanged {
		t.Fatalf("second check outcome = %v, want unchanged", got)
	}
}

// bump gives path a distinct mtime so the generation changes even on coarse
// filesystem timestamps.
func bump(t *testing.T, path string, seconds int) {
	t.Helper()
	at := time.Now().Add(time.Duration(seconds) * time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
