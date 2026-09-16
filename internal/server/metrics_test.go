package server

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposesRuntimeCollectors(t *testing.T) {
	// Independent registries must coexist without duplicate registrations.
	for range 2 {
		m := newMetrics()
		m.observe(http.MethodGet, "/health", http.StatusOK, time.Millisecond)
		rec := httptest.NewRecorder()
		m.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		want := []string{"go_goroutines", "go_memstats_alloc_bytes", "llm_proxy_requests_total"}
		// The process collector supports Linux and Windows.
		if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
			want = append(want, "process_resident_memory_bytes", "process_cpu_seconds_total")
		}
		for _, name := range want {
			if !strings.Contains(rec.Body.String(), "\n"+name+" ") && !strings.Contains(rec.Body.String(), "\n"+name+"{") {
				t.Errorf("metrics output missing sample %q", name)
			}
		}
	}
}
