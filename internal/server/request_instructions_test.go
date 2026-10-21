package server

import (
	"net/http"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

// Developer and late system messages translate onto Messages (folded into
// system, or kept in position) and stay byte-identical on native routes.
func TestInstructionAuthorityBoundary(t *testing.T) {
	for _, input := range []struct {
		path, field string
		kind        backend.Kind
	}{
		{"/v1/chat/completions", "messages", backend.KindOpenAIChat},
		{"/v1/responses", "input", backend.KindOpenAIResponses},
	} {
		for _, messages := range []string{
			`[{"role":"developer","content":"rules"},{"role":"user","content":"hi"}]`,
			`[{"role":"user","content":"hi"},{"role":"system","content":"rules"}]`,
		} {
			for _, native := range []bool{false, true} {
				name := string(input.kind) + "/translated"
				wire := backend.KindAnthropic
				response := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`
				if native {
					name = string(input.kind) + "/native"
					wire = input.kind
					response = `{"id":"response","output":[]}`
				}
				t.Run(name, func(t *testing.T) {
					fb := &fakeOABackend{name: "upstream", body: response, kinds: map[backend.Kind]bool{wire: true}}
					s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "upstream", Model: "alias"}})
					body := `{"model":"alias","` + input.field + `":` + messages + `}`
					rec := postOpenAI(t, s, input.path, body)
					if native {
						request := fb.lastRequest()
						if rec.Code != http.StatusOK || request == nil {
							t.Fatalf("native request rejected: %d %s", rec.Code, rec.Body.String())
						}
						if string(request.RawBody) != body {
							t.Fatalf("native instructions changed: %s", request.RawBody)
						}
					} else if rec.Code != http.StatusOK || fb.lastRequest() == nil {
						t.Fatalf("instruction mapping rejected: %d %s", rec.Code, rec.Body.String())
					}
				})
			}
		}
	}
}
