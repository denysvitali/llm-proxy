package server

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	minimaxcode "github.com/denysvitali/llm-proxy/internal/backend/minimaxcode"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

func TestAccountSchedulerMetricsExposeObservedChecks(t *testing.T) {
	manager := minimaxcode.NewManager(filepath.Join(t.TempDir(), "absent.json"))
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.RunAutoCheckin(ctx, logger) }()
	deadline := time.Now().Add(2 * time.Second)
	for manager.AutoCheckinStatus().LastCheckUnix == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	registry := prometheus.NewRegistry()
	registry.MustRegister(newAccountSchedulerCollector(AccountProviders{MiniMaxCode: manager}))
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`llm_proxy_account_scheduler_checks_total{provider="minimax-code",reason="signed_out"} 1`,
		`llm_proxy_account_scheduler_last_timestamp_seconds{event="check",provider="minimax-code"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing metric %q in %s", want, body)
		}
	}
	if strings.Contains(body, `event="success"`) || strings.Contains(body, `event="attempt"`) {
		t.Fatalf("unobserved events should be absent: %s", body)
	}
}
