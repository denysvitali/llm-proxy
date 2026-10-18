package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/denysvitali/llm-proxy/internal/backend"
	"github.com/denysvitali/llm-proxy/internal/config"
)

func TestRequestMessageInputBoundary(t *testing.T) {
	for _, api := range []struct {
		path string
		kind backend.Kind
	}{
		{"/v1/messages", backend.KindAnthropic},
		{"/v1/chat/completions", backend.KindOpenAIChat},
	} {
		for _, tc := range []struct {
			name, fields string
			valid        bool
		}{
			{"missing", ``, false},
			{"null", `,"messages":null`, false},
			{"empty", `,"messages":[]`, false},
			{"object", `,"messages":{}`, false},
			{"null entry", `,"messages":[null]`, false},
			{"string entry", `,"messages":["hello"]`, false},
			{"empty text", `,"messages":[{"role":"user","content":""}]`, true},
			{"whitespace", `,"messages":[{"role":"user","content":"  "}]`, true},
		} {
			t.Run(string(api.kind)+"/"+tc.name, func(t *testing.T) {
				fb := &fakeOABackend{name: "native", body: `{"ok":true}`, kinds: map[backend.Kind]bool{api.kind: true}}
				s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "native", Model: "upstream"}})
				body := `{"model":"alias"` + tc.fields + `}`
				rec := postOpenAI(t, s, api.path, body)
				if tc.valid {
					if rec.Code != http.StatusOK {
						t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
					}
					got := fb.lastRequest()
					if got == nil || string(got.RawBody) != strings.Replace(body, `"alias"`, `"upstream"`, 1) {
						t.Fatalf("native bytes changed: %+v", got)
					}
					return
				}
				if rec.Code != http.StatusBadRequest || fb.lastRequest() != nil {
					t.Fatalf("invalid input dispatched: %d %s", rec.Code, rec.Body.String())
				}
				var envelope struct {
					Type  string
					Error struct{ Type string }
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Type != "invalid_request_error" || api.kind == backend.KindAnthropic && envelope.Type != "error" {
					t.Fatalf("wrong dialect: %s", rec.Body.String())
				}
			})
		}
	}
}

func TestNativeResponsesContinuationWithoutInput(t *testing.T) {
	fb := &fakeOABackend{name: "native", body: `{"ok":true}`, kinds: map[backend.Kind]bool{backend.KindOpenAIResponses: true}}
	s := newOATestServer(t, fb, map[string]config.ModelRoute{"alias": {Backend: "native", Model: "upstream"}})
	body := `{"model":"alias","previous_response_id":"resp_previous"}`
	rec := postOpenAI(t, s, "/v1/responses", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := fb.lastRequest(); got == nil || string(got.RawBody) != strings.Replace(body, `"alias"`, `"upstream"`, 1) {
		t.Fatalf("continuation changed: %+v", got)
	}
}
