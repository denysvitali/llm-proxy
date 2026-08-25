package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/coder/websocket"
)

func acceptWebSocket(t *testing.T, w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	t.Helper()
	return websocket.Accept(w, r, nil)
}

func dialWebSocket(t *testing.T, ctx context.Context, url string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.Dial(ctx, url, nil)
}

func readWebSocketMessage(conn *websocket.Conn, ctx context.Context) ([]byte, error) {
	_, data, err := conn.Read(ctx)
	return data, err
}

func closeWebSocket(conn *websocket.Conn) error {
	return conn.Close(websocket.StatusNormalClosure, "")
}
