package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/denysvitali/llm-proxy/internal/config"
)

// newUpdateTestServer builds a bare Server with no backends; only the
// live-update endpoints are exercised.
func newUpdateTestServer(t *testing.T) *Server {
	t.Helper()
	return New(&config.Config{}, quietLogger(), nil, nil)
}

func TestUpdateHubNotifiesWebSocketClient(t *testing.T) {
	s := newUpdateTestServer(t)
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)

	dialCtx, cancelDial := context.WithTimeout(context.Background(), time.Second)
	defer cancelDial()
	conn, _, err := dialWebSocket(t, dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/updates/ws")
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = closeWebSocket(conn) }()

	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()

	s.updates.notify()
	data, err := readWebSocketMessage(conn, readCtx)
	if err != nil {
		t.Fatalf("read update event: %v", err)
	}
	if string(data) != string(statsUpdatedEvent) {
		t.Fatalf("event = %q, want %q", data, statsUpdatedEvent)
	}
}

func TestUpdateHubNotifiesSSEClient(t *testing.T) {
	s := newUpdateTestServer(t)
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/updates/sse", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get sse stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream", contentType)
	}

	s.updates.notify()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read sse data line: %v", err)
	}
	if line != "data: "+string(statsUpdatedEvent)+"\n" {
		t.Fatalf("sse line = %q, want stats-updated event", line)
	}
}

func TestUpdateHubClosedRejectsSubscribers(t *testing.T) {
	hub := newUpdateHub()
	hub.close()
	events, unsub := hub.subscribe()
	defer unsub()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("closed hub delivered an open channel")
		}
	default:
		t.Fatal("closed hub must hand out a closed channel")
	}
}
