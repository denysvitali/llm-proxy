package all_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/denysvitali/llm-proxy/internal/backend/all"

	"github.com/denysvitali/llm-proxy/internal/backend"
)

// TestLivePaperInstruments exercises the Paper Instruments backend against the
// real API over all three native endpoints. Opt-in only: set
// PAPERINSTRUMENTS_LIVE_TEST=1 and PAPERINSTRUMENTS_API_KEY=<key>.
func TestLivePaperInstruments(t *testing.T) {
	if os.Getenv("PAPERINSTRUMENTS_LIVE_TEST") != "1" {
		t.Skip("set PAPERINSTRUMENTS_LIVE_TEST=1 and PAPERINSTRUMENTS_API_KEY to run")
	}
	key := os.Getenv("PAPERINSTRUMENTS_API_KEY")
	if key == "" {
		t.Fatal("PAPERINSTRUMENTS_LIVE_TEST=1 requires PAPERINSTRUMENTS_API_KEY")
	}

	b, err := backend.New("paperinstruments", backend.Options{APIKey: key})
	if err != nil {
		t.Fatalf("registry New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const model = "ultramarine-1-flash"
	models, err := b.Models(ctx)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	found := false
	for _, m := range models {
		found = found || m == model
	}
	if !found {
		t.Fatalf("catalog %v does not contain %q", models, model)
	}

	const prompt = "Reply with the single word: pong"
	for _, test := range []struct {
		name      string
		kind      backend.Kind
		body      string
		streaming bool
	}{
		{"chat", backend.KindOpenAIChat, `{"model":"` + model + `","max_completion_tokens":300,"messages":[{"role":"user","content":"` + prompt + `"}]}`, false},
		{"chat stream", backend.KindOpenAIChat, `{"model":"` + model + `","max_completion_tokens":300,"stream":true,"messages":[{"role":"user","content":"` + prompt + `"}]}`, true},
		{"messages", backend.KindAnthropic, `{"model":"` + model + `","max_tokens":300,"messages":[{"role":"user","content":"` + prompt + `"}]}`, false},
		{"messages stream", backend.KindAnthropic, `{"model":"` + model + `","max_tokens":300,"stream":true,"messages":[{"role":"user","content":"` + prompt + `"}]}`, true},
		{"responses", backend.KindOpenAIResponses, `{"model":"` + model + `","max_output_tokens":300,"input":"` + prompt + `"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp, err := b.Send(ctx, &backend.Request{
				Kind:      test.kind,
				Model:     model,
				RawBody:   []byte(test.body),
				Streaming: test.streaming,
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.Status != 200 {
				t.Fatalf("status = %d", resp.Status)
			}
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !strings.Contains(strings.ToLower(string(data)), "pong") {
				t.Fatalf("body missing %q: %.400s", "pong", data)
			}
		})
	}
}
