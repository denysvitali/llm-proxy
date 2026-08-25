package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpdateHubNotifiesWebSocketClient(t *testing.T) {
	hub := newUpdateHub()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := acceptWebSocket(t, w, r)
		if err != nil {
			return
		}
		client := hub.add(conn)
		defer hub.remove(client)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
	})

	dialCtx, cancelDial := context.WithTimeout(context.Background(), time.Second)
	defer cancelDial()
	conn, _, err := dialWebSocket(t, dialCtx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws")
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer closeWebSocket(conn)

	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()

	hub.notify()
	data, err := readWebSocketMessage(conn, readCtx)
	if err != nil {
		t.Fatalf("read update event: %v", err)
	}
	if string(data) != string(statsUpdatedEvent) {
		t.Fatalf("event = %q, want %q", data, statsUpdatedEvent)
	}
}

func TestUpdateHubRejectsAfterClose(t *testing.T) {
	hub := newUpdateHub()
	hub.close()
	client := hub.add(nil)
	if client != nil {
		t.Fatal("closed hub accepted client")
	}
}
