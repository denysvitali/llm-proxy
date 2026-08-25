package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/auth"
)

// swapStdout captures what the command prints to stdout/stderr (commands use
// fmt.Fprint directly, so we replace the real handles rather than cobra's).
// The returned finish must be called before reading the buffers: it closes the
// write ends so the copy goroutines see EOF and finish.
func swapStdout(t *testing.T) (stdout, stderr *bytes.Buffer, finish func()) {
	t.Helper()
	var so, se bytes.Buffer
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&so, rOut)
		_, _ = io.Copy(&se, rErr)
		close(done)
	}()

	return &so, &se, func() {
		_ = wOut.Close()
		_ = wErr.Close()
		<-done
		os.Stdout, os.Stderr = oldOut, oldErr
	}
}

func newStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "keys.json")
}

func TestKeysCreateUser(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "alice", "--store", store})
	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("keys create-user: %v", err)
	}
}

func TestKeysCreateUserDuplicateErrors(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "alice", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("first create-user: %v", err)
	}

	rootCmd.SetArgs([]string{"keys", "create-user", "alice", "--store", store})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("duplicate create-user returned nil error, want already-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want %q", err, "already exists")
	}
}

func TestKeysCreateKeyReturnsPlaintext(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "alice", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create-user: %v", err)
	}

	stdout, stderr, finish := swapStdout(t)
	defer finish()
	rootCmd.SetArgs([]string{"keys", "create", "alice", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("keys create: %v", err)
	}
	finish()

	plain := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(plain, "llx_") {
		t.Errorf("create output = %q, want llx_-prefixed plaintext", plain)
	}
	if !strings.Contains(stderr.String(), "not recoverable") {
		t.Errorf("stderr = %q, want recovery warning", stderr.String())
	}
}

func TestKeysCreateKeyForUnknownUserErrors(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create", "nobody", "--store", store})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("create for unknown user returned nil error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want %q", err, "not found")
	}
}

func TestKeysList(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "bob", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create-user: %v", err)
	}

	stdout, _, finish := swapStdout(t)
	defer finish()
	rootCmd.SetArgs([]string{"keys", "list", "bob", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("keys list: %v", err)
	}
	finish()

	out := stdout.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") || !strings.Contains(out, "CREATED") || !strings.Contains(out, "STATUS") {
		t.Errorf("list header missing in output = %q", out)
	}
}

func TestKeysListUnknownUserErrors(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "list", "nobody", "--store", store})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("list unknown user returned nil error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want %q", err, "not found")
	}
}

func TestKeysSetStateDisable(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "carol", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create-user: %v", err)
	}

	stdout, _, finish := swapStdout(t)
	defer finish()
	rootCmd.SetArgs([]string{"keys", "create", "carol", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	finish()
	plain := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(plain, "llx_") {
		t.Fatalf("create output = %q, want llx_ prefix", plain)
	}

	s, err := auth.NewStore(store)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	keys, err := s.ListKeys("carol")
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	keyID := keys[0].ID
	if keys[0].Disabled {
		t.Fatal("key should start active")
	}

	stdout2, _, finish2 := swapStdout(t)
	defer finish2()
	rootCmd.SetArgs([]string{"keys", "set-state", "carol", keyID, "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set-state disable: %v", err)
	}
	finish2()
	if got := strings.TrimSpace(stdout2.String()); got != "key "+keyID+" disabled" {
		t.Errorf("set-state output = %q, want %q", got, "key "+keyID+" disabled")
	}

	s2, err := auth.NewStore(store)
	if err != nil {
		t.Fatalf("re-open store after disable: %v", err)
	}
	keys, err = s2.ListKeys("carol")
	if err != nil {
		t.Fatalf("list keys after disable: %v", err)
	}
	if !keys[0].Disabled {
		t.Error("key still active after disable")
	}
}

func TestKeysSetStateReEnable(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	rootCmd.SetArgs([]string{"keys", "create-user", "dave", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create-user: %v", err)
	}

	stdout, _, finish := swapStdout(t)
	defer finish()
	rootCmd.SetArgs([]string{"keys", "create", "dave", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	finish()
	plain := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(plain, "llx_") {
		t.Fatalf("create output = %q, want llx_ prefix", plain)
	}

	s, err := auth.NewStore(store)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	keys, err := s.ListKeys("dave")
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	keyID := keys[0].ID

	rootCmd.SetArgs([]string{"keys", "set-state", "dave", keyID, "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set-state disable: %v", err)
	}

	stdout2, _, finish2 := swapStdout(t)
	defer finish2()
	rootCmd.SetArgs([]string{"keys", "set-state", "dave", keyID, "--disable=false", "--store", store})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("set-state re-enable: %v", err)
	}
	finish2()
	if got := strings.TrimSpace(stdout2.String()); got != "key "+keyID+" enabled" {
		t.Errorf("set-state output = %q, want %q", got, "key "+keyID+" enabled")
	}

	s3, err := auth.NewStore(store)
	if err != nil {
		t.Fatalf("re-open store after re-enable: %v", err)
	}
	keys, err = s3.ListKeys("dave")
	if err != nil {
		t.Fatalf("list keys after re-enable: %v", err)
	}
	if keys[0].Disabled {
		t.Error("key still disabled after re-enable")
	}
}

func TestKeysSetStateUnknownUserOrKeyErrors(t *testing.T) {
	rootCmd.SetArgs(nil)
	store := newStorePath(t)
	for _, args := range [][]string{
		{"keys", "set-state", "nobody", "k1", "--store", store},
	} {
		rootCmd.SetArgs(args)
		if err := rootCmd.Execute(); err == nil {
			t.Errorf("set-state %v returned nil error", args)
		}
	}
}
