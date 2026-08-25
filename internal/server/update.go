package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const updateWriteTimeout = 5 * time.Second

var statsUpdatedEvent = []byte(`{"type":"stats-updated"}`)

// updateHub broadcasts lightweight stats-change notifications to dashboard
// clients over WebSocket or SSE. Clients refetch the existing JSON APIs, so
// events carry no request data.
type updateHub struct {
	mu     sync.Mutex
	subs   map[chan []byte]struct{}
	closed bool
}

func newUpdateHub() *updateHub {
	return &updateHub{subs: make(map[chan []byte]struct{})}
}

// subscribe registers a buffered event channel. The returned unsubscribe
// function is idempotent; the channel is closed either by it or by the hub
// shutting down, whichever happens first.
func (h *updateHub) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; !ok {
			return
		}
		delete(h.subs, ch)
		close(ch)
	}
	return ch, unsub
}

func (h *updateHub) notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for ch := range h.subs {
		select {
		case ch <- statsUpdatedEvent:
		default:
		}
	}
}

func (h *updateHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// handleUpdatesWebSocket serves the dashboard's live-update channel as a
// WebSocket. Connections that arrive over a transport the server cannot
// hijack (HTTP/2 and friends) fail in websocket.Accept; those clients are
// expected on the SSE twin below.
func (s *Server) handleUpdatesWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(1)

	// Reading detects client-side disconnects; nothing is ever sent upstream.
	readCtx, cancelRead := context.WithCancel(r.Context())
	defer cancelRead()
	go func() {
		defer cancelRead()
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()

	events, unsub := s.updates.subscribe()
	defer unsub()
	for event := range events {
		writeCtx, cancel := context.WithTimeout(r.Context(), updateWriteTimeout)
		err := conn.Write(writeCtx, websocket.MessageText, event)
		cancel()
		if err != nil {
			return
		}
	}
	_ = conn.Close(websocket.StatusNormalClosure, "connection closed")
}

// handleUpdatesSSE serves the same live-update channel as Server-Sent
// Events, which needs no connection hijack and therefore works over every
// HTTP version and intermediary.
func (s *Server) handleUpdatesSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "api_error", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsub := s.updates.subscribe()
	defer unsub()
	for {
		select {
		case event, open := <-events:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
