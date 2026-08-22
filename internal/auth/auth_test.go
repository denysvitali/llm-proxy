package auth_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/auth"
)

func newTestStore(t *testing.T) (*auth.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	s, err := auth.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore(%q): unexpected error: %v", path, err)
	}
	return s, path
}

// readStore parses the on-disk store file.
func readStore(t *testing.T, path string) []auth.User {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var users []auth.User
	if err := json.Unmarshal(raw, &users); err != nil {
		t.Fatalf("unmarshal store %q: %v", raw, err)
	}
	return users
}

func TestNewStoreMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	s, err := auth.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore on missing file returned error: %v", err)
	}
	if got := s.Users(); len(got) != 0 {
		t.Fatalf("fresh store has users = %v, want empty", got)
	}
}

func TestNewStoreMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewStore(path); err == nil {
		t.Fatal("NewStore on malformed JSON returned nil error, want parse error")
	}
}

func TestCreateUserDuplicate(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.CreateUser("alice"); err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if err := s.CreateUser("alice"); err == nil {
		t.Fatal("duplicate CreateUser(alice) returned nil error, want already-exists error")
	}
	if got, want := s.Users(), []string{"alice"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Users() = %v, want %v", got, want)
	}
}

func TestCreateKeyPlaintextNotPersisted(t *testing.T) {
	s, path := newTestStore(t)
	if err := s.CreateUser("alice"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	k1, err := s.CreateKey("alice", "laptop")
	if err != nil {
		t.Fatalf("CreateKey(laptop): %v", err)
	}
	k2, err := s.CreateKey("alice", "phone")
	if err != nil {
		t.Fatalf("CreateKey(phone): %v", err)
	}
	for i, k := range []string{k1, k2} {
		if !strings.HasPrefix(k, "llx_") {
			t.Errorf("key %d = %q, want %q prefix", i+1, k, "llx_")
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	onDisk := string(raw)
	if strings.Contains(onDisk, k1) || strings.Contains(onDisk, k2) {
		t.Error("plaintext key found in on-disk store; only hashes may be persisted")
	}

	users := readStore(t, path)
	if len(users) != 1 || len(users[0].Keys) != 2 {
		t.Fatalf("on-disk store = %+v, want 1 user with 2 keys", users)
	}
	for _, k := range users[0].Keys {
		if k.Hash == "" || !strings.Contains(onDisk, k.Hash) {
			t.Errorf("key %q: hash %q missing from on-disk store", k.ID, k.Hash)
		}
	}

	if u, ok := s.Verify(k1); !ok || u != "alice" {
		t.Errorf("Verify(key1) = (%q, %v), want (alice, true)", u, ok)
	}
	if u, ok := s.Verify(k2); !ok || u != "alice" {
		t.Errorf("Verify(key2) = (%q, %v), want (alice, true)", u, ok)
	}
}

func TestVerify(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.CreateUser("alice"); err != nil {
		t.Fatalf("CreateUser(alice): %v", err)
	}
	if err := s.CreateUser("bob"); err != nil {
		t.Fatalf("CreateUser(bob): %v", err)
	}
	aliceKey, err := s.CreateKey("alice", "a")
	if err != nil {
		t.Fatalf("CreateKey(alice): %v", err)
	}
	bobKey, err := s.CreateKey("bob", "b")
	if err != nil {
		t.Fatalf("CreateKey(bob): %v", err)
	}

	if u, ok := s.Verify(aliceKey); !ok || u != "alice" {
		t.Errorf("Verify(aliceKey) = (%q, %v), want (alice, true)", u, ok)
	}
	if u, ok := s.Verify(bobKey); !ok || u != "bob" {
		t.Errorf("Verify(bobKey) = (%q, %v), want (bob, true)", u, ok)
	}
	// A wrong or empty key must never verify, and bob's key must not be
	// attributed to alice.
	for _, presented := range []string{"", "llx_deadbeef", aliceKey + "0", strings.ToUpper(bobKey)} {
		if u, ok := s.Verify(presented); ok {
			t.Errorf("Verify(%q) = (%q, true), want false", presented, u)
		}
	}
}

func TestDisableKeyRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.CreateUser("carol"); err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	key, err := s.CreateKey("carol", "cli")
	if err != nil {
		t.Fatalf("CreateKey(cli): %v", err)
	}
	keys, err := s.ListKeys("carol")
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListKeys(carol) = %v, %v; want 1 key", keys, err)
	}
	id := keys[0].ID

	// Unknown user and unknown key are errors.
	if err := s.DisableKey("nobody", id, true); err == nil {
		t.Error("DisableKey(unknown user) returned nil error")
	}
	if err := s.DisableKey("carol", "ffffffffffffffff", true); err == nil {
		t.Error("DisableKey(unknown key ID) returned nil error")
	}

	if u, ok := s.Verify(key); !ok || u != "carol" {
		t.Fatalf("Verify before disable = (%q, %v), want (carol, true)", u, ok)
	}
	if err := s.DisableKey("carol", id, true); err != nil {
		t.Fatalf("DisableKey(true): %v", err)
	}
	if _, ok := s.Verify(key); ok {
		t.Error("Verify(disabled key) succeeded, want false")
	}
	if err := s.DisableKey("carol", id, false); err != nil {
		t.Fatalf("DisableKey(false): %v", err)
	}
	if u, ok := s.Verify(key); !ok || u != "carol" {
		t.Errorf("Verify(re-enabled key) = (%q, %v), want (carol, true)", u, ok)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "keys.json") // exercises MkdirAll in save

	s1, err := auth.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s1.CreateUser("dave"); err != nil {
		t.Fatalf("CreateUser(dave): %v", err)
	}
	key, err := s1.CreateKey("dave", "main")
	if err != nil {
		t.Fatalf("CreateKey(main): %v", err)
	}
	keys, err := s1.ListKeys("dave")
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListKeys(dave) = %v, %v; want 1 key", keys, err)
	}
	keyID := keys[0].ID
	if err := s1.DisableKey("dave", keyID, true); err != nil {
		t.Fatalf("DisableKey(true): %v", err)
	}

	s2, err := auth.NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if got, want := s2.Users(), "dave"; len(got) != 1 || got[0] != want {
		t.Fatalf("reopened Users() = %v, want [%s]", got, want)
	}
	if u, ok := s2.Verify(key); ok {
		t.Errorf("reopened Verify(persisted-disabled key) = (%q, true), want false", u)
	}
	if err := s2.DisableKey("dave", keyID, false); err != nil {
		t.Fatalf("reopened DisableKey(false): %v", err)
	}
	if u, ok := s2.Verify(key); !ok || u != "dave" {
		t.Errorf("reopened Verify(key) = (%q, %v), want (dave, true)", u, ok)
	}
}

func TestSaveFilePerms(t *testing.T) {
	s, path := newTestStore(t)
	if err := s.CreateUser("eve"); err != nil {
		t.Fatalf("CreateUser(eve): %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fi.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("store file perms = %o, want %o", got, want)
	}
}

func TestHashKeyFormatAndSalting(t *testing.T) {
	h := auth.HashKey("hunter2")
	parts := strings.SplitN(h, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("hash %q missing ':' separator, want hex16salt:hex32sum", h)
	}
	if len(parts[0]) != 32 { // 16-byte salt
		t.Errorf("salt part %q is %d chars, want 32", parts[0], len(parts[0]))
	}
	if len(parts[1]) != 64 { // 32-byte sha256 sum
		t.Errorf("sum part %q is %d chars, want 64", parts[1], len(parts[1]))
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		t.Errorf("salt part %q not hex: %v", parts[0], err)
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		t.Errorf("sum part %q not hex: %v", parts[1], err)
	}

	if !auth.VerifyHash(h, "hunter2") {
		t.Error("VerifyHash(valid hash, matching plain) = false, want true")
	}
	if auth.VerifyHash(h, "hunter3") {
		t.Error("VerifyHash(valid hash, wrong plain) = true, want false")
	}

	// Same plaintext twice must produce different stored hashes (per-hash salt).
	h2 := auth.HashKey("hunter2")
	if h2 == h {
		t.Error("two HashKey calls on same plaintext produced identical hash; salt missing?")
	}
	if !auth.VerifyHash(h2, "hunter2") {
		t.Error("VerifyHash(second valid hash, matching plain) = false, want true")
	}
}

func TestVerifyHashGarbageNeverMatches(t *testing.T) {
	valid := auth.HashKey("pw")

	// Tampered-but-well-formed hash: flip the last sum nibble.
	last := valid[len(valid)-1]
	tamper := byte('a')
	if last == 'a' {
		tamper = 'b'
	}
	tampered := valid[:len(valid)-1] + string(tamper)
	if tampered == valid {
		t.Fatalf("tampering produced identical hash %q", tampered)
	}

	for _, stored := range []string{"", "x", "aa:bb", tampered} {
		if auth.VerifyHash(stored, "pw") {
			t.Errorf("VerifyHash(%q, \"pw\") = true, want false for garbage/tampered hash", stored)
		}
	}
}
