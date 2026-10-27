package codex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionDiscoveryRefreshAndFailure(t *testing.T) {
	count := 0
	version := "0.165.0"
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Header.Get("Authorization") != "" || r.Header.Get("ChatGPT-Account-Id") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("credentials sent to registry: %v", r.Header)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
	}))
	defer server.Close()
	v := &versionDiscovery{client: server.Client(), endpoint: server.URL}
	if got := v.latest(t.Context()); got != version {
		t.Fatalf("discovered %q", got)
	}
	version = "0.166.0"
	if got := v.latest(t.Context()); got != "0.165.0" || count != 1 {
		t.Fatalf("cache = %q, requests = %d", got, count)
	}
	v.until = time.Time{}
	if got := v.latest(t.Context()); got != version || count != 2 {
		t.Fatalf("refresh = %q, requests = %d", got, count)
	}
	status = http.StatusServiceUnavailable
	v.until = time.Time{}
	if got := v.latest(t.Context()); got != version {
		t.Fatalf("failure discarded last known version: %q", got)
	}
	_ = v.latest(t.Context())
	if count != 3 {
		t.Fatalf("failure not cached: requests = %d", count)
	}
	v.version = ""
	v.until = time.Time{}
	if got := v.latest(t.Context()); got != clientVersion {
		t.Fatalf("fallback = %q", got)
	}
}

func TestVersionDiscoveryRejectsInvalidVersions(t *testing.T) {
	for _, version := range []string{"", "v0.165.0", "0.165", "0.165.0-alpha.1", "0.165.0+build", "01.165.0", "0.165.0?x=y", " 0.165.0"} {
		t.Run(version, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"version":"`+version+`"}`)
			}))
			defer server.Close()
			v := &versionDiscovery{client: server.Client(), endpoint: server.URL}
			if got := v.latest(t.Context()); got != clientVersion {
				t.Fatalf("invalid version accepted: %q", got)
			}
		})
	}
}

func TestVersionDiscoveryCancellation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"version":"0.166.0"}`)
	}))
	defer server.Close()
	v := &versionDiscovery{client: server.Client(), endpoint: server.URL}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if got := v.latest(ctx); got != clientVersion {
		t.Fatalf("cancelled discovery = %q", got)
	}
	if got := v.latest(t.Context()); got != "0.166.0" {
		t.Fatalf("healthy discovery after cancelled caller = %q", got)
	}
}
