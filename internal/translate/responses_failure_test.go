package translate

import "testing"

func TestResponsesFailure(t *testing.T) {
	for _, tc := range []struct {
		name, payload, message, kind string
		invalid                      bool
	}{
		{"nested", `{"type":"response.failed","response":{"error":{"message":"too long","code":"context_length_exceeded"}}}`, "too long", "invalid_request_error", true},
		{"flat", `{"type":"error","message":"too long","code":"context_length_exceeded"}`, "too long", "invalid_request_error", true},
		{"numeric code", `{"error":{"type":"server_error","message":"unavailable","code":500}}`, "unavailable", "server_error", false},
		{"no error", `{"type":"response.failed","response":{"instructions":"private"}}`, "upstream stream failed", "api_error", false},
		{"malformed", `{`, "upstream stream failed", "api_error", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ResponsesFailure([]byte(tc.payload))
			if err.Message != tc.message || err.Type != tc.kind || err.InvalidRequest() != tc.invalid {
				t.Fatalf("error=%+v invalid=%t", err, err.InvalidRequest())
			}
		})
	}
}
