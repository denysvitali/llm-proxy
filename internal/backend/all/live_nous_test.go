package all_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/denysvitali/llm-proxy/internal/backend/all"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// TestLiveNous exercises the Nous Portal backend against the real API.
// Opt-in only: set NOUS_LIVE_TEST=1 and NOUS_API_KEY=<key>.
func TestLiveNous(t *testing.T) {
	if os.Getenv("NOUS_LIVE_TEST") != "1" {
		t.Skip("set NOUS_LIVE_TEST=1 and NOUS_API_KEY to run")
	}
	key := os.Getenv("NOUS_API_KEY")
	if key == "" {
		t.Fatal("NOUS_LIVE_TEST=1 requires NOUS_API_KEY")
	}

	if !backend.Has("nous") {
		t.Fatal(`backend registry does not know "nous"; all import broken`)
	}

	b, err := backend.New("nous", backend.Options{APIKey: key})
	if err != nil {
		t.Fatalf("registry New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	models, err := b.Models(ctx)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("Models returned empty catalog")
	}
	t.Logf("catalog: %d models, first=%q", len(models), models[0])

	body, _ := json.Marshal(map[string]any{
		"model":    "stealth/ox-alpha",
		"messages": []map[string]string{{"role": "user", "content": "Reply with the single word: pong"}},
	})
	resp, err := b.Send(ctx, &backend.Request{
		Kind:    backend.KindOpenAIChat,
		Model:   "stealth/ox-alpha",
		RawBody: body,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		data = append(data, buf[:n]...)
		if err != nil {
			break
		}
		if len(data) > 1<<20 {
			break
		}
	}
	t.Logf("status=%d body=%.512s", resp.Status, data)
	if resp.Status != 200 {
		t.Fatalf("expected HTTP 200, got %d", resp.Status)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &completion); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if len(completion.Choices) == 0 {
		t.Fatalf("no choices in response: %.512s", data)
	}
	t.Logf("completion content: %q", completion.Choices[0].Message.Content)
}
