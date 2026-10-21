package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// Recorded transcripts are real client requests (Claude Code CLI, the Agent
// SDK, Codex CLI) and the upstream Responses streams that answered them,
// anonymized by scripts/fixtures/anonymize_transcripts.py. They pin the
// shapes real clients and the ChatGPT Codex endpoint send today, which the
// hand-written fixtures elsewhere tend to lag behind.

type transcript struct {
	name     string
	path     string // client endpoint
	request  map[string]json.RawMessage
	upstream string          // recorded upstream SSE
	final    json.RawMessage // response object from response.completed
}

type transcriptOutput struct {
	text  string
	calls map[string]string // call name -> arguments JSON
}

func loadTranscripts(t *testing.T) []transcript {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("testdata", "transcripts", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no transcripts: %v", err)
	}
	var out []transcript
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		var meta struct {
			ClientPath string `json:"client_path"`
		}
		tr := transcript{name: filepath.Base(dir)}
		readJSON(t, filepath.Join(dir, "meta.json"), &meta)
		readJSON(t, filepath.Join(dir, "request.json"), &tr.request)
		tr.path = meta.ClientPath
		sse, err := os.ReadFile(filepath.Join(dir, "upstream.sse"))
		if err != nil {
			t.Fatal(err)
		}
		tr.upstream = string(sse)
		for _, data := range sseData(tr.upstream) {
			var event struct {
				Type     string          `json:"type"`
				Response json.RawMessage `json:"response"`
			}
			if json.Unmarshal([]byte(data), &event) == nil && event.Type == "response.completed" {
				tr.final = event.Response
			}
		}
		if tr.final == nil {
			t.Fatalf("%s: upstream stream has no response.completed", tr.name)
		}
		out = append(out, tr)
	}
	return out
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func sseData(stream string) []string {
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			out = append(out, data)
		}
	}
	return out
}

func (tr transcript) body(stream bool) string {
	request := make(map[string]json.RawMessage, len(tr.request))
	for k, v := range tr.request {
		request[k] = v
	}
	request["stream"] = json.RawMessage("false")
	if stream {
		request["stream"] = json.RawMessage("true")
	}
	encoded, _ := json.Marshal(request)
	return string(encoded)
}

// expected is what the upstream answered: visible text and function calls.
func (tr transcript) expected(t *testing.T) transcriptOutput {
	t.Helper()
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(tr.final, &response); err != nil {
		t.Fatal(err)
	}
	out := transcriptOutput{calls: map[string]string{}}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				out.text += part.Text
			}
		case "function_call":
			out.calls[item.Name] = item.Arguments
		}
	}
	return out
}

func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal([]byte(got), &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ea, _ := json.Marshal(a)
	eb, _ := json.Marshal(b)
	return string(ea) == string(eb)
}

// messagesOutput collects text and tool calls from an Anthropic response,
// buffered or streamed.
func messagesOutput(t *testing.T, body string, stream bool) transcriptOutput {
	t.Helper()
	out := transcriptOutput{calls: map[string]string{}}
	if !stream {
		var message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal([]byte(body), &message); err != nil {
			t.Fatalf("decode message: %v: %s", err, body)
		}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				out.text += block.Text
			case "tool_use":
				out.calls[block.Name] = string(block.Input)
			}
		}
		return out
	}
	names := map[int]string{}
	args := map[int]string{}
	stopped := false
	for _, data := range sseData(body) {
		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("decode event %s: %v", data, err)
		}
		switch event.Type {
		case "error":
			t.Fatalf("stream carried an error: %s", data)
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				names[event.Index] = event.ContentBlock.Name
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				out.text += event.Delta.Text
			case "input_json_delta":
				args[event.Index] += event.Delta.PartialJSON
			}
		case "message_stop":
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("stream ended without message_stop: %s", body)
	}
	for index, name := range names {
		out.calls[name] = args[index]
	}
	return out
}

// responsesOutput collects text and calls from a Responses client answer.
func responsesOutput(t *testing.T, body string, stream bool) transcriptOutput {
	t.Helper()
	final := json.RawMessage(body)
	if stream {
		final = nil
		for _, data := range sseData(body) {
			var event struct {
				Type     string          `json:"type"`
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				t.Fatalf("decode event %s: %v", data, err)
			}
			switch event.Type {
			case "error", "response.failed":
				t.Fatalf("stream carried a failure: %s", data)
			case "response.completed":
				final = event.Response
			}
		}
		if final == nil {
			t.Fatalf("stream ended without response.completed: %s", body)
		}
	}
	return transcript{final: final}.expected(t)
}

func TestRecordedTranscriptsReplay(t *testing.T) {
	for _, tr := range loadTranscripts(t) {
		for _, stream := range []bool{true, false} {
			mode := "buffered"
			if stream {
				mode = "streaming"
			}
			t.Run(tr.name+"/"+mode, func(t *testing.T) {
				fb := &fakeOABackend{
					name:  "codex",
					kinds: map[backend.Kind]bool{backend.KindOpenAIResponses: true},
				}
				// Streaming replays the recorded SSE; buffered gets the
				// completed response, as the codex backend aggregates it.
				if stream {
					fb.header = http.Header{"Content-Type": []string{"text/event-stream"}}
					fb.body = tr.upstream
				} else {
					fb.header = http.Header{"Content-Type": []string{"application/json"}}
					fb.body = string(tr.final)
				}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "codex", Model: "gpt-6-luna"}})
				rec := postOpenAI(t, s, tr.path, tr.body(stream))
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
				}
				var got transcriptOutput
				if tr.path == "/v1/messages" {
					got = messagesOutput(t, rec.Body.String(), stream)
				} else {
					got = responsesOutput(t, rec.Body.String(), stream)
				}
				want := tr.expected(t)
				if got.text != want.text {
					t.Errorf("text = %q, want %q", got.text, want.text)
				}
				if len(got.calls) != len(want.calls) {
					t.Errorf("calls = %v, want %v", got.calls, want.calls)
				}
				for name, args := range want.calls {
					if !sameJSON(t, got.calls[name], args) {
						t.Errorf("call %s arguments = %s, want %s", name, got.calls[name], args)
					}
				}
			})
		}
	}
}

// Every recorded request must also translate onto the wires it was not
// recorded against, since any client can be routed to any backend.
func TestRecordedTranscriptsCrossWire(t *testing.T) {
	replies := map[backend.Kind]string{
		backend.KindOpenAIChat:      `{"id":"chat_1","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		backend.KindAnthropic:       `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`,
		backend.KindOpenAIResponses: "",
	}
	for _, tr := range loadTranscripts(t) {
		for kind, reply := range replies {
			t.Run(tr.name+"/"+string(kind), func(t *testing.T) {
				if reply == "" {
					reply = string(tr.final)
				}
				fb := &fakeOABackend{
					name:   "upstream",
					kinds:  map[backend.Kind]bool{kind: true},
					header: http.Header{"Content-Type": []string{"application/json"}},
					body:   reply,
				}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "m"}})
				rec := postOpenAI(t, s, tr.path, tr.body(false))
				if rec.Code != http.StatusOK || fb.lastRequest() == nil {
					t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}
