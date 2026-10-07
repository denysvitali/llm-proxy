package minimaxcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type schedulerOutcomeHook struct{ outcomes chan<- string }

func (h schedulerOutcomeHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h schedulerOutcomeHook) Fire(entry *logrus.Entry) error {
	if outcome, ok := entry.Data["outcome"].(string); ok {
		h.outcomes <- outcome
	}
	return nil
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func autoCheckinManager(t *testing.T, status func() map[string]any, claimStatus int) (*Manager, *atomic.Int32, *httptest.Server) {
	t.Helper()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			writeJSON(t, w, map[string]any{"data": status()})
		case "/minimax-cloud/api/v1/signin/claim":
			posts.Add(1)
			if claimStatus != http.StatusOK {
				http.Error(w, "private upstream response detail", claimStatus)
				return
			}
			panel := testPanel()
			panel["days"].([]any)[0].(map[string]any)["status"] = 3
			writeJSON(t, w, map[string]any{"data": map[string]any{"claim_id": "private-claim", "claim_result": 1, "day_no": 1, "points": 100, "expire_at_ms": 1770000000000, "panel": panel}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	manager := NewManager(filepath.Join(t.TempDir(), "auth.json"))
	manager.AccountBaseURL = server.URL
	if err := manager.Store.Save(&Credentials{AccessToken: "secret-account-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return manager, &posts, server
}

func activePanel() map[string]any { return testPanel() }
func claimedPanel() map[string]any {
	p := testPanel()
	p["days"].([]any)[0].(map[string]any)["status"] = 3
	return p
}

func runCheckinUntilOutcome(t *testing.T, manager *Manager, interval time.Duration, want string, log *logrus.Logger) {
	t.Helper()
	outcomes := make(chan string, 8)
	log.AddHook(schedulerOutcomeHook{outcomes})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, log, interval); close(finished) }()
	select {
	case got := <-outcomes:
		if got != want {
			cancel()
			<-finished
			t.Fatalf("automatic check-in outcome=%q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		cancel()
		<-finished
		t.Fatal("automatic check-in did not reach expected request")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic check-in did not stop after cancellation")
	}
}

func TestRunAutoCheckinStartupAndCancellation(t *testing.T) {
	manager, posts, _ := autoCheckinManager(t, activePanel, http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, logrus.New(), time.Hour); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for posts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if posts.Load() != 1 {
		cancel()
		<-done
		t.Fatalf("startup POST count=%d", posts.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on cancellation")
	}
}

func TestRunAutoCheckinCancellationDuringEligibilityDoesNotCreateMarker(t *testing.T) {
	statusEntered := make(chan struct{})
	var posts atomic.Int32
	manager := accountTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/api/user/info":
			identityResponse(w)
		case "/minimax-cloud/api/v1/signin/status":
			close(statusEntered)
			<-r.Context().Done()
		case "/minimax-cloud/api/v1/signin/claim":
			posts.Add(1)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, logrus.New(), time.Hour); close(done) }()
	select {
	case <-statusEntered:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("eligibility request did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop after eligibility cancellation")
	}
	if posts.Load() != 0 {
		t.Fatalf("canceled eligibility sent %d claim POSTs", posts.Load())
	}
	if _, err := os.Stat(manager.Store.Path + ".checkin"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled eligibility created marker: %v", err)
	}
}

func TestRunAutoCheckinSignedOutThenLateLogin(t *testing.T) {
	manager, posts, server := autoCheckinManager(t, activePanel, http.StatusOK)
	path := manager.Store.Path
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	manager.Store = &Store{Path: path}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, logrus.New(), 20*time.Millisecond); close(done) }()
	time.Sleep(30 * time.Millisecond)
	if posts.Load() != 0 {
		t.Fatal("signed-out worker sent a claim")
	}
	if err := manager.Store.Save(&Credentials{AccessToken: "secret-account-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for posts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if posts.Load() != 1 {
		t.Fatalf("late login POST count=%d", posts.Load())
	}
	server.Close()
}

func TestRunAutoCheckinUnavailableAndAlreadyClaimedLeaveNoMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status func() map[string]any
	}{
		{"unavailable", func() map[string]any {
			p := testPanel()
			for _, d := range p["days"].([]any) {
				d.(map[string]any)["status"] = 1
			}
			return p
		}},
		{"already claimed", claimedPanel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, _, _ := autoCheckinManager(t, tc.status, http.StatusOK)
			logger := logrus.New()
			want := "unavailable"
			if tc.name == "already claimed" {
				want = "already_claimed"
			}
			runCheckinUntilOutcome(t, manager, time.Hour, want, logger)
			if _, err := os.Stat(manager.Store.Path + ".checkin"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("marker directory exists or could not be read: %v", err)
			}
		})
	}
}

func TestRunAutoCheckinSharedMarkerPreventsSecondManagerPost(t *testing.T) {
	manager, posts, server := autoCheckinManager(t, activePanel, http.StatusOK)
	second := NewManager(manager.Store.Path)
	second.AccountBaseURL = server.URL
	second.Store = &Store{Path: manager.Store.Path}
	outcomes := make(chan string, 8)
	loggerOne, loggerTwo := logrus.New(), logrus.New()
	loggerOne.SetOutput(io.Discard)
	loggerTwo.SetOutput(io.Discard)
	loggerOne.AddHook(schedulerOutcomeHook{outcomes})
	loggerTwo.AddHook(schedulerOutcomeHook{outcomes})
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondDone := make(chan struct{})
	go func() { second.runAutoCheckin(secondCtx, loggerTwo, time.Hour); close(secondDone) }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, loggerOne, time.Hour); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	alreadyAttempted := false
	for (!alreadyAttempted || posts.Load() != 1) && time.Now().Before(deadline) {
		select {
		case outcome := <-outcomes:
			alreadyAttempted = alreadyAttempted || outcome == "already_claimed"
		case <-time.After(time.Millisecond):
		}
	}
	if posts.Load() != 1 || !alreadyAttempted {
		cancel()
		cancelSecond()
		<-done
		<-secondDone
		t.Fatalf("POST count=%d", posts.Load())
	}
	cancel()
	cancelSecond()
	<-done
	<-secondDone
	if posts.Load() != 1 {
		t.Fatalf("shared marker allowed %d POSTs", posts.Load())
	}
}

func TestRunAutoCheckinUncertainResponsePersistsMarker(t *testing.T) {
	manager, posts, _ := autoCheckinManager(t, activePanel, http.StatusBadGateway)
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan struct{})
	go func() { manager.runAutoCheckin(ctx, logrus.New(), time.Hour); close(firstDone) }()
	deadline := time.Now().Add(3 * time.Second)
	for posts.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if posts.Load() != 1 {
		cancel()
		<-firstDone
		t.Fatalf("first attempt POST count=%d", posts.Load())
	}
	time.Sleep(10 * time.Millisecond) // Let the 502 response reach the worker.
	cancel()
	<-firstDone
	second := NewManager(manager.Store.Path)
	second.AccountBaseURL = manager.AccountBaseURL
	second.Store = &Store{Path: manager.Store.Path}
	runCheckinUntilOutcome(t, second, time.Hour, "already_claimed", logrus.New())
	if posts.Load() != 1 {
		t.Fatalf("uncertain result was retried, POST count=%d", posts.Load())
	}
	if _, err := os.Stat(filepath.Join(manager.Store.Path+".checkin", time.Now().UTC().Format("2006-01-02")+"-"+hex.EncodeToString(sha256Sum("personal-user")))); err != nil {
		t.Fatalf("uncertain attempt marker missing: %v", err)
	}
}

func sha256Sum(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func TestCheckinMarkersUseUTCDateAndSeparateAccounts(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "auth.json")
	day := time.Date(2026, 10, 7, 23, 30, 0, 0, time.FixedZone("west", -7*60*60))
	if err := createCheckinMarker(storePath, "personal-user", day); err != nil {
		t.Fatal(err)
	}
	if err := createCheckinMarker(storePath, "personal-user", day.UTC().Add(24*time.Hour)); err != nil {
		t.Fatalf("next UTC day was blocked: %v", err)
	}
	if err := createCheckinMarker(storePath, "second-user", day); err != nil {
		t.Fatalf("second account was blocked: %v", err)
	}
	if err := createCheckinMarker(storePath, "personal-user", day.UTC()); !errors.Is(err, ErrAutoCheckinAlreadyAttempted) {
		t.Fatalf("same UTC day error=%v", err)
	}
	entries, err := os.ReadDir(storePath + ".checkin")
	if err != nil || len(entries) != 3 {
		t.Fatalf("marker entries=%d err=%v", len(entries), err)
	}
	info, err := os.Stat(storePath + ".checkin")
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("directory mode=%v err=%v", info.Mode().Perm(), err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("marker mode=%v err=%v", info.Mode().Perm(), err)
		}
	}
}

func TestRunAutoCheckinLogsCategoriesWithoutUpstreamText(t *testing.T) {
	var output synchronizedBuffer
	manager, posts, _ := autoCheckinManager(t, activePanel, http.StatusBadGateway)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	log := logrus.New()
	log.SetOutput(&output)
	go func() { manager.runAutoCheckin(ctx, log, time.Hour); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for (!strings.Contains(output.String(), "outcome=failed") || posts.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	text := output.String()
	if !strings.Contains(text, "outcome=failed") || strings.Contains(text, "private upstream response detail") || strings.Contains(text, "personal-user") || strings.Contains(text, "secret-account-token") {
		t.Fatalf("unexpected scheduler log contents: %q", text)
	}
}
