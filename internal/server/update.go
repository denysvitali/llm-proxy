package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const updateWriteTimeout = 5 * time.Second

var statsUpdatedEvent = []byte(`{"type":"stats-updated"}`)

// updateHub broadcasts lightweight stats-change notifications to dashboard
// WebSocket clients. Clients refetch the existing JSON APIs, so events carry
// no request data.
type updateHub struct {
	mu      sync.Mutex
	clients map[*updateClient]struct{}
	closed  bool
}

type updateClient struct {
	conn   *websocket.Conn
	events chan []byte
	cancel context.CancelFunc
}

func newUpdateHub() *updateHub {
	return &updateHub{clients: make(map[*updateClient]struct{})}
}

func (h *updateHub) add(conn *websocket.Conn) *updateClient {
	ctx, cancel := context.WithCancel(context.Background())
	client := &updateClient{
		conn:   conn,
		events: make(chan []byte, 1),
		cancel: cancel,
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(client.events)
		cancel()
		return nil
	}
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	go client.writeEvents(ctx)
	return client
}

func (h *updateHub) remove(client *updateClient) {
	if client == nil {
		return
	}

	h.mu.Lock()
	if _, ok := h.clients[client]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, client)
	h.mu.Unlock()

	close(client.events)
	client.cancel()
	_ = client.conn.Close(websocket.StatusNormalClosure, "connection closed")
}

func (h *updateHub) notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for client := range h.clients {
		select {
		case client.events <- statsUpdatedEvent:
		default:
		}
	}
}

func (h *updateHub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	clients := make([]*updateClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.clients = make(map[*updateClient]struct{})
	h.mu.Unlock()

	for _, client := range clients {
		client.cancel()
		_ = client.conn.Close(websocket.StatusGoingAway, "server stopping")
	}
}

func (c *updateClient) writeEvents(ctx context.Context) {
	for {
		select {
		case event := <-c.events:
			writeCtx, cancel := context.WithTimeout(ctx, updateWriteTimeout)
			err := c.conn.Write(writeCtx, websocket.MessageText, event)
			cancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handleUpdatesWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(1)

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

	client := s.updates.add(conn)
	defer s.updates.remove(client)
	<-readCtx.Done()
}
